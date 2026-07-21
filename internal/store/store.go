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

	"localrelay/internal/capabilities"

	_ "modernc.org/sqlite"
)

var (
	ErrInvalidModelID = errors.New("model must use providerId/modelId")
	ErrModelDisabled  = errors.New("model is disabled")
)

type Store struct {
	db  *sql.DB
	key [32]byte
}

type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey,omitempty"`
	// CapabilityConfig records provider-specific protocol wrinkles such as
	// reasoning_effort and thinking fields. Unknown provider quirks should live
	// here instead of being scattered through relay conversion branches.
	CapabilityConfig string `json:"capabilityConfig"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

type ProviderInput struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	BaseURL          string `json:"baseUrl"`
	APIKey           string `json:"apiKey"`
	CapabilityConfig string `json:"capabilityConfig"`
}

type Model struct {
	ID            string `json:"id"`
	ProviderID    string `json:"providerId"`
	Name          string `json:"name"`
	Capabilities  string `json:"capabilities"`
	ContextLength int    `json:"contextLength"`
	MaxTokens     int    `json:"maxTokens"`
	Enabled       bool   `json:"enabled"`
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
	Enabled       *bool  `json:"enabled,omitempty"`
}

type RoutedModel struct {
	Provider Provider `json:"provider"`
	Model    Model    `json:"model"`
}

type CallLog struct {
	ProviderID               string `json:"providerId"`
	ModelID                  string `json:"modelId"`
	Protocol                 string `json:"protocol"`
	StartedAt                string `json:"startedAt"`
	EndedAt                  string `json:"endedAt,omitempty"`
	StatusCode               int    `json:"statusCode"`
	Error                    string `json:"error,omitempty"`
	InputTokens              int    `json:"inputTokens"`
	OutputTokens             int    `json:"outputTokens"`
	CacheCreationInputTokens int    `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int    `json:"cacheReadInputTokens"`
}

type TokenStatsFilter struct {
	From       string `json:"from"`
	To         string `json:"to"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
}

type TokenStats struct {
	InputTokens              int              `json:"inputTokens"`
	OutputTokens             int              `json:"outputTokens"`
	CacheCreationInputTokens int              `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int              `json:"cacheReadInputTokens"`
	Calls                    int              `json:"calls"`
	Points                   []TokenStatPoint `json:"points"`
}

type TokenStatPoint struct {
	Date                     string `json:"date"`
	InputTokens              int    `json:"inputTokens"`
	OutputTokens             int    `json:"outputTokens"`
	CacheCreationInputTokens int    `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int    `json:"cacheReadInputTokens"`
	Calls                    int    `json:"calls"`
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
	capability_config TEXT NOT NULL DEFAULT '',
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
	enabled INTEGER NOT NULL DEFAULT 1,
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
	if err != nil {
		return err
	}
	if err := s.ensureProviderCapabilityConfigColumn(); err != nil {
		return err
	}
	return s.ensureModelEnabledColumn()
}

func (s *Store) ListProviders() ([]Provider, error) {
	rows, err := s.db.Query(`SELECT id, name, type, base_url, api_key_encrypted, capability_config, created_at, updated_at FROM providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []Provider
	for rows.Next() {
		var p Provider
		var encrypted string
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &encrypted, &p.CapabilityConfig, &p.CreatedAt, &p.UpdatedAt); err != nil {
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
	in = withDefaultCapabilityConfig(in)
	if err := validateProvider(in); err != nil {
		return Provider{}, err
	}
	now := timestamp()
	encrypted, err := s.encrypt(in.APIKey)
	if err != nil {
		return Provider{}, err
	}
	_, err = s.db.Exec(
		`INSERT INTO providers(id, name, type, base_url, api_key_encrypted, capability_config, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.Name, in.Type, in.BaseURL, encrypted, in.CapabilityConfig, now, now,
	)
	if err != nil {
		return Provider{}, err
	}
	return Provider{ID: in.ID, Name: in.Name, Type: in.Type, BaseURL: in.BaseURL, APIKey: in.APIKey, CapabilityConfig: in.CapabilityConfig, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) UpdateProvider(in ProviderInput) (Provider, error) {
	in = withDefaultCapabilityConfig(in)
	if err := validateProvider(in); err != nil {
		return Provider{}, err
	}
	now := timestamp()
	encrypted, err := s.encrypt(in.APIKey)
	if err != nil {
		return Provider{}, err
	}
	result, err := s.db.Exec(
		`UPDATE providers SET name = ?, type = ?, base_url = ?, api_key_encrypted = ?, capability_config = ?, updated_at = ? WHERE id = ?`,
		in.Name, in.Type, in.BaseURL, encrypted, in.CapabilityConfig, now, in.ID,
	)
	if err != nil {
		return Provider{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Provider{}, sql.ErrNoRows
	}
	return Provider{ID: in.ID, Name: in.Name, Type: in.Type, BaseURL: in.BaseURL, APIKey: in.APIKey, CapabilityConfig: in.CapabilityConfig, UpdatedAt: now}, nil
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
		rows, err = s.db.Query(`SELECT id, provider_id, name, capabilities, context_length, max_tokens, enabled, created_at, updated_at FROM models ORDER BY provider_id, name`)
	} else {
		rows, err = s.db.Query(`SELECT id, provider_id, name, capabilities, context_length, max_tokens, enabled, created_at, updated_at FROM models WHERE provider_id = ? ORDER BY name`, providerID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []Model
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.Name, &m.Capabilities, &m.ContextLength, &m.MaxTokens, &m.Enabled, &m.CreatedAt, &m.UpdatedAt); err != nil {
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
	enabled := inputEnabled(in)
	_, err := s.db.Exec(
		`INSERT INTO models(id, provider_id, name, capabilities, context_length, max_tokens, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.ProviderID, in.Name, in.Capabilities, in.ContextLength, in.MaxTokens, enabled, now, now,
	)
	if err != nil {
		return Model{}, err
	}
	return Model{ID: in.ID, ProviderID: in.ProviderID, Name: in.Name, Capabilities: in.Capabilities, ContextLength: in.ContextLength, MaxTokens: in.MaxTokens, Enabled: enabled, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) UpdateModel(in ModelInput) (Model, error) {
	if err := validateModel(in); err != nil {
		return Model{}, err
	}
	now := timestamp()
	enabled := inputEnabled(in)
	result, err := s.db.Exec(
		`UPDATE models SET name = ?, capabilities = ?, context_length = ?, max_tokens = ?, enabled = ?, updated_at = ? WHERE provider_id = ? AND id = ?`,
		in.Name, in.Capabilities, in.ContextLength, in.MaxTokens, enabled, now, in.ProviderID, in.ID,
	)
	if err != nil {
		return Model{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Model{}, sql.ErrNoRows
	}
	return Model{ID: in.ID, ProviderID: in.ProviderID, Name: in.Name, Capabilities: in.Capabilities, ContextLength: in.ContextLength, MaxTokens: in.MaxTokens, Enabled: enabled, UpdatedAt: now}, nil
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

func (s *Store) GetRoutedModel(publicModel string) (RoutedModel, error) {
	providerID, modelID, ok := strings.Cut(strings.TrimSpace(publicModel), "/")
	if !ok || strings.TrimSpace(providerID) == "" || strings.TrimSpace(modelID) == "" {
		return RoutedModel{}, ErrInvalidModelID
	}
	var routed RoutedModel
	var encrypted string
	err := s.db.QueryRow(`
SELECT p.id, p.name, p.type, p.base_url, p.api_key_encrypted, p.capability_config, p.created_at, p.updated_at,
       m.id, m.provider_id, m.name, m.capabilities, m.context_length, m.max_tokens, m.enabled, m.created_at, m.updated_at
FROM models m
JOIN providers p ON p.id = m.provider_id
WHERE m.provider_id = ? AND m.id = ?`, providerID, modelID).Scan(
		&routed.Provider.ID, &routed.Provider.Name, &routed.Provider.Type, &routed.Provider.BaseURL, &encrypted, &routed.Provider.CapabilityConfig, &routed.Provider.CreatedAt, &routed.Provider.UpdatedAt,
		&routed.Model.ID, &routed.Model.ProviderID, &routed.Model.Name, &routed.Model.Capabilities, &routed.Model.ContextLength, &routed.Model.MaxTokens, &routed.Model.Enabled, &routed.Model.CreatedAt, &routed.Model.UpdatedAt,
	)
	if err != nil {
		return RoutedModel{}, err
	}
	routed.Provider.APIKey, err = s.decrypt(encrypted)
	if err != nil {
		return RoutedModel{}, err
	}
	if !routed.Model.Enabled {
		return RoutedModel{}, ErrModelDisabled
	}
	return routed, nil
}

func (s *Store) ListEnabledModels() ([]Model, error) {
	rows, err := s.db.Query(`SELECT id, provider_id, name, capabilities, context_length, max_tokens, enabled, created_at, updated_at FROM models WHERE enabled = 1 ORDER BY provider_id, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var models []Model
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.Name, &m.Capabilities, &m.ContextLength, &m.MaxTokens, &m.Enabled, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

func (s *Store) CreateCallLog(log CallLog) error {
	return s.CreateCallLogs([]CallLog{log})
}

func (s *Store) CreateCallLogs(logs []CallLog) error {
	if len(logs) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`
INSERT INTO call_logs(provider_id, model_id, protocol, started_at, ended_at, status_code, error, input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens)
VALUES `)
	args := make([]any, 0, len(logs)*11)
	for i, log := range logs {
		if log.StartedAt == "" {
			log.StartedAt = timestamp()
		}
		if i > 0 {
			query.WriteString(",")
		}
		query.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			nullable(log.ProviderID), nullable(log.ModelID), log.Protocol, log.StartedAt, nullable(log.EndedAt), log.StatusCode, nullable(log.Error),
			log.InputTokens, log.OutputTokens, log.CacheCreationInputTokens, log.CacheReadInputTokens,
		)
	}
	_, err := s.db.Exec(query.String(), args...)
	return err
}

func (s *Store) TokenStats(filter TokenStatsFilter) (TokenStats, error) {
	filter = normalizeStatsFilter(filter)
	where, args := statsWhere(filter)
	rows, err := s.db.Query(`
SELECT substr(started_at, 1, 10), COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
       COALESCE(SUM(cache_creation_input_tokens), 0), COALESCE(SUM(cache_read_input_tokens), 0)
FROM call_logs`+where+`
GROUP BY substr(started_at, 1, 10)
ORDER BY substr(started_at, 1, 10)`, args...)
	if err != nil {
		return TokenStats{}, err
	}
	defer rows.Close()

	var stats TokenStats
	for rows.Next() {
		var p TokenStatPoint
		if err := rows.Scan(&p.Date, &p.Calls, &p.InputTokens, &p.OutputTokens, &p.CacheCreationInputTokens, &p.CacheReadInputTokens); err != nil {
			return TokenStats{}, err
		}
		stats.Calls += p.Calls
		stats.InputTokens += p.InputTokens
		stats.OutputTokens += p.OutputTokens
		stats.CacheCreationInputTokens += p.CacheCreationInputTokens
		stats.CacheReadInputTokens += p.CacheReadInputTokens
		stats.Points = append(stats.Points, p)
	}
	return stats, rows.Err()
}

func normalizeStatsFilter(filter TokenStatsFilter) TokenStatsFilter {
	if len(filter.From) == len("2006-01-02") {
		filter.From += "T00:00:00Z"
	}
	if len(filter.To) == len("2006-01-02") {
		filter.To += "T23:59:59Z"
	}
	return filter
}

func statsWhere(filter TokenStatsFilter) (string, []any) {
	var clauses []string
	var args []any
	if filter.From != "" {
		clauses = append(clauses, "started_at >= ?")
		args = append(args, filter.From)
	}
	if filter.To != "" {
		clauses = append(clauses, "started_at <= ?")
		args = append(args, filter.To)
	}
	if strings.TrimSpace(filter.ProviderID) != "" {
		clauses = append(clauses, "provider_id = ?")
		args = append(args, filter.ProviderID)
	}
	if strings.TrimSpace(filter.ModelID) != "" {
		clauses = append(clauses, "model_id = ?")
		args = append(args, filter.ModelID)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
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
	if err := capabilities.Validate(in.CapabilityConfig); err != nil {
		return fmt.Errorf("provider capabilityConfig is invalid: %w", err)
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

func (s *Store) ensureModelEnabledColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(models)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "enabled" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE models ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`)
	return err
}

func (s *Store) ensureProviderCapabilityConfigColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(providers)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "capability_config" {
			return s.backfillProviderCapabilityConfig()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE providers ADD COLUMN capability_config TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	return s.backfillProviderCapabilityConfig()
}

func (s *Store) backfillProviderCapabilityConfig() error {
	rows, err := s.db.Query(`SELECT id, type FROM providers WHERE capability_config = ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type providerType struct {
		id  string
		typ string
	}
	var items []providerType
	for rows.Next() {
		var item providerType
		if err := rows.Scan(&item.id, &item.typ); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := s.db.Exec(`UPDATE providers SET capability_config = ? WHERE id = ?`, capabilities.DefaultJSON(item.typ), item.id); err != nil {
			return err
		}
	}
	return nil
}

func withDefaultCapabilityConfig(in ProviderInput) ProviderInput {
	if strings.TrimSpace(in.CapabilityConfig) == "" {
		in.CapabilityConfig = capabilities.DefaultJSON(in.Type)
	}
	return in
}

func inputEnabled(in ModelInput) bool {
	return in.Enabled == nil || *in.Enabled
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
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
	return sha256.Sum256(fmt.Appendf(nil, "localrelay:%s:%s", hostname, configDir))
}

func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
