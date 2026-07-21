package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db  *sql.DB
	key [32]byte
}

type Provider struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	BaseURL   string `json:"baseUrl"`
	APIKey    string `json:"apiKey,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type ProviderInput struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

type Model struct {
	ID            string `json:"id"`
	ProviderID    string `json:"providerId"`
	Name          string `json:"name"`
	Capabilities  string `json:"capabilities"`
	ContextLength int    `json:"contextLength"`
	MaxTokens     int    `json:"maxTokens"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type ModelInput struct {
	ID            string `json:"id"`
	ProviderID    string `json:"providerId"`
	Name          string `json:"name"`
	Capabilities  string `json:"capabilities"`
	ContextLength int    `json:"contextLength"`
	MaxTokens     int    `json:"maxTokens"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, key: localKey()}
	if _, err = db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS providers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	base_url TEXT NOT NULL,
	api_key_encrypted TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS models (
	id TEXT NOT NULL,
	provider_id TEXT NOT NULL,
	name TEXT NOT NULL,
	capabilities TEXT NOT NULL DEFAULT '',
	context_length INTEGER NOT NULL DEFAULT 0,
	max_tokens INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (provider_id, id),
	FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS call_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider_id TEXT,
	model_id TEXT,
	protocol TEXT NOT NULL,
	started_at TEXT NOT NULL,
	ended_at TEXT,
	status_code INTEGER,
	error TEXT,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_models_provider_id ON models(provider_id);
CREATE INDEX IF NOT EXISTS idx_call_logs_started_at ON call_logs(started_at);
CREATE INDEX IF NOT EXISTS idx_call_logs_provider_model ON call_logs(provider_id, model_id);

INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, CURRENT_TIMESTAMP);
`)
	return err
}

func (s *Store) ListProviders() ([]Provider, error) {
	rows, err := s.db.Query(`SELECT id, name, type, base_url, api_key_encrypted, created_at, updated_at FROM providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []Provider
	for rows.Next() {
		var p Provider
		var encrypted string
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &encrypted, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.APIKey, err = s.decrypt(encrypted)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

func (s *Store) CreateProvider(in ProviderInput) (Provider, error) {
	if err := validateProvider(in); err != nil {
		return Provider{}, err
	}
	now := timestamp()
	encrypted, err := s.encrypt(in.APIKey)
	if err != nil {
		return Provider{}, err
	}
	_, err = s.db.Exec(
		`INSERT INTO providers(id, name, type, base_url, api_key_encrypted, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.Name, in.Type, in.BaseURL, encrypted, now, now,
	)
	if err != nil {
		return Provider{}, err
	}
	return Provider{ID: in.ID, Name: in.Name, Type: in.Type, BaseURL: in.BaseURL, APIKey: in.APIKey, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) UpdateProvider(in ProviderInput) (Provider, error) {
	if err := validateProvider(in); err != nil {
		return Provider{}, err
	}
	now := timestamp()
	encrypted, err := s.encrypt(in.APIKey)
	if err != nil {
		return Provider{}, err
	}
	result, err := s.db.Exec(
		`UPDATE providers SET name = ?, type = ?, base_url = ?, api_key_encrypted = ?, updated_at = ? WHERE id = ?`,
		in.Name, in.Type, in.BaseURL, encrypted, now, in.ID,
	)
	if err != nil {
		return Provider{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Provider{}, sql.ErrNoRows
	}
	return Provider{ID: in.ID, Name: in.Name, Type: in.Type, BaseURL: in.BaseURL, APIKey: in.APIKey, UpdatedAt: now}, nil
}

func (s *Store) DeleteProvider(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("provider id is required")
	}
	_, err := s.db.Exec(`DELETE FROM providers WHERE id = ?`, id)
	return err
}

func (s *Store) ListModels(providerID string) ([]Model, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(providerID) == "" {
		rows, err = s.db.Query(`SELECT id, provider_id, name, capabilities, context_length, max_tokens, created_at, updated_at FROM models ORDER BY provider_id, name`)
	} else {
		rows, err = s.db.Query(`SELECT id, provider_id, name, capabilities, context_length, max_tokens, created_at, updated_at FROM models WHERE provider_id = ? ORDER BY name`, providerID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []Model
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.Name, &m.Capabilities, &m.ContextLength, &m.MaxTokens, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

func (s *Store) CreateModel(in ModelInput) (Model, error) {
	if err := validateModel(in); err != nil {
		return Model{}, err
	}
	now := timestamp()
	_, err := s.db.Exec(
		`INSERT INTO models(id, provider_id, name, capabilities, context_length, max_tokens, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.ProviderID, in.Name, in.Capabilities, in.ContextLength, in.MaxTokens, now, now,
	)
	if err != nil {
		return Model{}, err
	}
	return Model{ID: in.ID, ProviderID: in.ProviderID, Name: in.Name, Capabilities: in.Capabilities, ContextLength: in.ContextLength, MaxTokens: in.MaxTokens, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) UpdateModel(in ModelInput) (Model, error) {
	if err := validateModel(in); err != nil {
		return Model{}, err
	}
	now := timestamp()
	result, err := s.db.Exec(
		`UPDATE models SET name = ?, capabilities = ?, context_length = ?, max_tokens = ?, updated_at = ? WHERE provider_id = ? AND id = ?`,
		in.Name, in.Capabilities, in.ContextLength, in.MaxTokens, now, in.ProviderID, in.ID,
	)
	if err != nil {
		return Model{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Model{}, sql.ErrNoRows
	}
	return Model{ID: in.ID, ProviderID: in.ProviderID, Name: in.Name, Capabilities: in.Capabilities, ContextLength: in.ContextLength, MaxTokens: in.MaxTokens, UpdatedAt: now}, nil
}

func (s *Store) DeleteModel(providerID, id string) error {
	providerID = strings.TrimSpace(providerID)
	id = strings.TrimSpace(id)
	if providerID == "" || id == "" {
		return errors.New("provider id and model id are required")
	}
	_, err := s.db.Exec(`DELETE FROM models WHERE provider_id = ? AND id = ?`, providerID, id)
	return err
}

func validateProvider(in ProviderInput) error {
	if strings.TrimSpace(in.ID) == "" {
		return errors.New("provider id is required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("provider name is required")
	}
	if strings.TrimSpace(in.Type) == "" {
		return errors.New("provider type is required")
	}
	if strings.TrimSpace(in.BaseURL) == "" {
		return errors.New("provider baseUrl is required")
	}
	return nil
}

func validateModel(in ModelInput) error {
	if strings.TrimSpace(in.ID) == "" {
		return errors.New("model id is required")
	}
	if strings.TrimSpace(in.ProviderID) == "" {
		return errors.New("provider id is required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("model name is required")
	}
	if in.ContextLength < 0 || in.MaxTokens < 0 {
		return errors.New("token limits must be >= 0")
	}
	return nil
}

func (s *Store) encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (s *Store) decrypt(encrypted string) (string, error) {
	if encrypted == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("encrypted value is too short")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func localKey() [32]byte {
	hostname, _ := os.Hostname()
	configDir, _ := os.UserConfigDir()
	return sha256.Sum256([]byte(fmt.Sprintf("localrelay:%s:%s", hostname, configDir)))
}

func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
