package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"localrelay/internal/capabilities"

	_ "modernc.org/sqlite"
)

var (
	ErrInvalidModelID = errors.New("model must use providerId/modelId")
	ErrModelDisabled  = errors.New("model is disabled")
	newAPIKey         = generateAPIKey
)

const NoAppName = "无应用"

// DefaultRelayPort is the TCP port used by the local gateway when the user has
// not selected another port.
const DefaultRelayPort = 8718

// DesktopSettings contains user-configurable application and gateway state.
// Values are stored in app_settings so existing installations receive the
// defaults without requiring a data migration for each newly added setting.
type DesktopSettings struct {
	GatewayEnabled bool `json:"gatewayEnabled"`
	HideOnMinimize bool `json:"hideOnMinimize"`
	HideOnClose    bool `json:"hideOnClose"`
	LaunchAtLogin  bool `json:"launchAtLogin"`
	StartMinimized bool `json:"startMinimized"`
	TrayHintShown  bool `json:"-"`
}

func defaultDesktopSettings() DesktopSettings {
	return DesktopSettings{
		GatewayEnabled: true,
		HideOnMinimize: true,
		HideOnClose:    true,
		StartMinimized: true,
	}
}

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
	ID            string             `json:"id"`
	ProviderID    string             `json:"providerId"`
	Name          string             `json:"name"`
	Capabilities  string             `json:"capabilities"`
	ContextLength int                `json:"contextLength"`
	MaxTokens     int                `json:"maxTokens"`
	Enabled       bool               `json:"enabled"`
	CreatedAt     string             `json:"createdAt"`
	UpdatedAt     string             `json:"updatedAt"`
	Aggregation   *AggregationConfig `json:"aggregation,omitempty"`
}

type ModelInput struct {
	ID            string             `json:"id"`
	ProviderID    string             `json:"providerId"`
	Name          string             `json:"name"`
	Capabilities  string             `json:"capabilities"`
	ContextLength int                `json:"contextLength"`
	MaxTokens     int                `json:"maxTokens"`
	Enabled       *bool              `json:"enabled,omitempty"`
	Aggregation   *AggregationConfig `json:"aggregation,omitempty"`
}

type RoutedModel struct {
	Provider Provider `json:"provider"`
	Model    Model    `json:"model"`
}

type APIKey struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Key         string `json:"key"`
	DeletedAt   string `json:"deletedAt,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type APIKeyInput struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type CallLog struct {
	ID                       int64  `json:"id"`
	ProviderID               string `json:"providerId"`
	ModelID                  string `json:"modelId"`
	AppName                  string `json:"appName"`
	Protocol                 string `json:"protocol"`
	StartedAt                string `json:"startedAt"`
	EndedAt                  string `json:"endedAt,omitempty"`
	StatusCode               int    `json:"statusCode"`
	Error                    string `json:"error,omitempty"`
	DurationMs               int64  `json:"durationMs"`
	Stream                   bool   `json:"stream"`
	InputTokens              int    `json:"inputTokens"`
	OutputTokens             int    `json:"outputTokens"`
	CacheCreationInputTokens int    `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int    `json:"cacheReadInputTokens"`
	TokenEstimated           bool   `json:"tokenEstimated"`
	AggregationSource        string `json:"aggregationSource,omitempty"`
	AggAttempts              string `json:"aggAttempts,omitempty"`
}

type TokenStatsFilter struct {
	From              string `json:"from"`
	To                string `json:"to"`
	ProviderID        string `json:"providerId"`
	ModelID           string `json:"modelId"`
	AppName           string `json:"appName"`
	AggregationSource string `json:"aggregationSource"`
}

type TokenStats struct {
	InputTokens              int              `json:"inputTokens"`
	OutputTokens             int              `json:"outputTokens"`
	CacheCreationInputTokens int              `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int              `json:"cacheReadInputTokens"`
	Calls                    int              `json:"calls"`
	EstimatedCalls           int              `json:"estimatedCalls"`
	Points                   []TokenStatPoint `json:"points"`
}

type TokenStatPoint struct {
	Date                     string `json:"date"`
	InputTokens              int    `json:"inputTokens"`
	OutputTokens             int    `json:"outputTokens"`
	CacheCreationInputTokens int    `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int    `json:"cacheReadInputTokens"`
	Calls                    int    `json:"calls"`
	EstimatedCalls           int    `json:"estimatedCalls"`
}

type TokenStatRow struct {
	Name                     string  `json:"name"`
	Calls                    int     `json:"calls"`
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	TotalTokens              int     `json:"totalTokens"`
	Share                    float64 `json:"share"`
}

type TokenTrendPoint struct {
	Bucket                   string `json:"bucket"`
	Name                     string `json:"name"`
	Calls                    int    `json:"calls"`
	InputTokens              int    `json:"inputTokens"`
	OutputTokens             int    `json:"outputTokens"`
	CacheCreationInputTokens int    `json:"cacheCreationInputTokens"`
	CacheReadInputTokens     int    `json:"cacheReadInputTokens"`
}

type CallLogPage struct {
	Items []CallLog `json:"items"`
	Total int       `json:"total"`
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
	app_name TEXT NOT NULL DEFAULT '无应用',
	protocol TEXT NOT NULL,
	started_at TEXT NOT NULL,
	ended_at TEXT,
	status_code INTEGER,
	error TEXT,
	is_stream INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
	token_estimated INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	key TEXT NOT NULL UNIQUE,
	deleted_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS app_settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_models_provider_id ON models(provider_id);
CREATE INDEX IF NOT EXISTS idx_call_logs_started_at ON call_logs(started_at);
CREATE INDEX IF NOT EXISTS idx_call_logs_provider_model ON call_logs(provider_id, model_id);

INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, CURRENT_TIMESTAMP);
`)
	if err != nil {
		return err
	}
	return s.applyMigrations()
}

func (s *Store) applyMigrations() error {
	migrations := []struct {
		version int
		run     func() error
	}{
		{2, s.ensureProviderCapabilityConfigColumn},
		{3, s.ensureModelEnabledColumn},
		{4, s.ensureAPIKeyStatsSchema},
		{5, s.ensureAppSettingsSchema},
		{6, s.enableVolcengineCodingStreamUsage},
		{7, s.ensureTokenEstimateColumn},
		{8, s.ensureAggregationSchema},
		{9, s.migrateAggregationConfigKeys},
	}
	for _, migration := range migrations {
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, migration.version).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if err := migration.run(); err != nil {
			return err
		}
		if _, err := s.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, CURRENT_TIMESTAMP)`, migration.version); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureTokenEstimateColumn() error {
	has, err := s.hasColumn("call_logs", "token_estimated")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE call_logs ADD COLUMN token_estimated INTEGER NOT NULL DEFAULT 0`)
	return err
}

func (s *Store) ensureAggregationSchema() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS aggregation_configs (model_id TEXT PRIMARY KEY, config_json TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		return err
	}
	for _, column := range []struct{ name, sql string }{
		{"aggregation_source", `ALTER TABLE call_logs ADD COLUMN aggregation_source TEXT NOT NULL DEFAULT ''`},
		{"agg_attempts", `ALTER TABLE call_logs ADD COLUMN agg_attempts TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := s.hasColumn("call_logs", column.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := s.db.Exec(column.sql); err != nil {
				return err
			}
		}
	}
	_, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_call_logs_aggregation_source ON call_logs(aggregation_source)`)
	return err
}

// migrateAggregationConfigKeys makes the existing model_id key unambiguous.
// The column remains the primary key required by the original schema, but now
// stores the public providerId/modelId route rather than a provider-local id.
func (s *Store) migrateAggregationConfigKeys() error {
	rows, err := s.db.Query(`SELECT model_id FROM aggregation_configs`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var legacy []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return err
		}
		legacy = append(legacy, key)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, key := range legacy {
		var providerID, modelID string
		err := s.db.QueryRow(`SELECT m.provider_id, m.id FROM models m JOIN providers p ON p.id = m.provider_id WHERE p.type = ? AND m.id = ?`, AggregationProviderType, key).Scan(&providerID, &modelID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE aggregation_configs SET model_id = ? WHERE model_id = ?`, aggregationConfigKey(providerID, modelID), key); err != nil {
			return err
		}
	}
	return nil
}

// enableVolcengineCodingStreamUsage updates only the known Coding Plan
// endpoint. Its streamed responses return usage as null unless the
// OpenAI-compatible stream_options.include_usage flag is requested.
func (s *Store) enableVolcengineCodingStreamUsage() error {
	rows, err := s.db.Query(`SELECT id, capability_config FROM providers WHERE base_url LIKE '%ark.cn-beijing.volces.com/api/coding/v3%'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		id  string
		cfg capabilities.Provider
	}
	var items []item
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		cfg, err := capabilities.Parse(raw)
		if err != nil {
			return fmt.Errorf("parse Coding Plan capabilities for %q: %w", id, err)
		}
		if cfg.Protocol == capabilities.ProtocolOpenAIChat && !cfg.Streaming.IncludeUsage {
			cfg.Streaming.IncludeUsage = true
			items = append(items, item{id: id, cfg: cfg})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		raw, err := json.MarshalIndent(item.cfg, "", "  ")
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE providers SET capability_config = ?, updated_at = ? WHERE id = ?`, string(raw), timestamp(), item.id); err != nil {
			return err
		}
	}
	return nil
}

// RelayPort returns the persisted gateway port, falling back to the default
// for installations created before the setting existed.
func (s *Store) RelayPort() (int, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'relay_port'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultRelayPort, nil
	}
	if err != nil {
		return 0, err
	}
	var port int
	if _, err := fmt.Sscanf(value, "%d", &port); err != nil {
		return 0, fmt.Errorf("stored relay port is invalid: %w", err)
	}
	if err := ValidateRelayPort(port); err != nil {
		return 0, fmt.Errorf("stored relay port is invalid: %w", err)
	}
	return port, nil
}

// SetRelayPort saves the TCP port selected for the gateway.
func (s *Store) SetRelayPort(port int) error {
	if err := ValidateRelayPort(port); err != nil {
		return err
	}
	_, err := s.db.Exec(`
INSERT INTO app_settings(key, value) VALUES ('relay_port', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprintf("%d", port))
	return err
}

// DesktopSettings returns persisted desktop settings, using sensible defaults
// for databases created before a setting was introduced.
func (s *Store) DesktopSettings() (DesktopSettings, error) {
	settings := defaultDesktopSettings()
	rows, err := s.db.Query(`SELECT key, value FROM app_settings WHERE key IN (
        'gateway_enabled', 'hide_on_minimize', 'hide_on_close',
        'launch_at_login', 'start_minimized', 'tray_hint_shown'
    )`)
	if err != nil {
		return DesktopSettings{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return DesktopSettings{}, err
		}
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return DesktopSettings{}, fmt.Errorf("stored desktop setting %q is invalid: %w", key, err)
		}
		switch key {
		case "gateway_enabled":
			settings.GatewayEnabled = parsed
		case "hide_on_minimize":
			settings.HideOnMinimize = parsed
		case "hide_on_close":
			settings.HideOnClose = parsed
		case "launch_at_login":
			settings.LaunchAtLogin = parsed
		case "start_minimized":
			settings.StartMinimized = parsed
		case "tray_hint_shown":
			settings.TrayHintShown = parsed
		}
	}
	if err := rows.Err(); err != nil {
		return DesktopSettings{}, err
	}
	return settings, nil
}

// SetDesktopSettings persists the complete desktop-settings snapshot. Keeping
// the values together avoids a partial state when a UI updates several toggles.
func (s *Store) SetDesktopSettings(settings DesktopSettings) error {
	values := map[string]bool{
		"gateway_enabled":  settings.GatewayEnabled,
		"hide_on_minimize": settings.HideOnMinimize,
		"hide_on_close":    settings.HideOnClose,
		"launch_at_login":  settings.LaunchAtLogin,
		"start_minimized":  settings.StartMinimized,
		"tray_hint_shown":  settings.TrayHintShown,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.Exec(`
INSERT INTO app_settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, strconv.FormatBool(value)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ValidateRelayPort rejects ports that cannot be used as a TCP listen port.
func ValidateRelayPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("relay port must be between 1 and 65535")
	}
	return nil
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
	if in.Type != AggregationProviderType {
		in = withDefaultCapabilityConfig(in)
	}
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
	if in.Type != AggregationProviderType {
		in = withDefaultCapabilityConfig(in)
	}
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
	if _, err := s.db.Exec(`DELETE FROM aggregation_configs WHERE model_id IN (SELECT provider_id || '/' || id FROM models WHERE provider_id = ?)`, id); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM models WHERE provider_id = ?`, id); err != nil {
		return err
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
		var providerType string
		if err := s.db.QueryRow(`SELECT type FROM providers WHERE id = ?`, m.ProviderID).Scan(&providerType); err != nil {
			return nil, err
		}
		if providerType == AggregationProviderType {
			cfg, err := s.GetAggregationConfig(m.ProviderID, m.ID)
			if err != nil {
				return nil, err
			}
			m.Aggregation = &cfg
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

// ListAggregationMemberModels returns only real models that may be selected as
// aggregation members. It deliberately avoids reading aggregation_configs, so
// a damaged aggregation draft cannot hide otherwise healthy member models.
func (s *Store) ListAggregationMemberModels() ([]Model, error) {
	rows, err := s.db.Query(`
SELECT m.id, m.provider_id, m.name, m.capabilities, m.context_length, m.max_tokens, m.enabled, m.created_at, m.updated_at
FROM models m JOIN providers p ON p.id = m.provider_id
WHERE p.type != ?
ORDER BY m.provider_id, m.name`, AggregationProviderType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var models []Model
	for rows.Next() {
		var model Model
		if err := rows.Scan(&model.ID, &model.ProviderID, &model.Name, &model.Capabilities, &model.ContextLength, &model.MaxTokens, &model.Enabled, &model.CreatedAt, &model.UpdatedAt); err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, rows.Err()
}

func (s *Store) CreateModel(in ModelInput) (Model, error) {
	if err := validateModel(in); err != nil {
		return Model{}, err
	}
	var providerType string
	if err := s.db.QueryRow(`SELECT type FROM providers WHERE id = ?`, in.ProviderID).Scan(&providerType); err != nil {
		return Model{}, err
	}
	if providerType == AggregationProviderType {
		if in.Aggregation == nil {
			return Model{}, errors.New("aggregation model configuration is required")
		}
		if err := s.validateAggregationConfig(*in.Aggregation); err != nil {
			return Model{}, err
		}
		in.ContextLength, in.MaxTokens, in.Capabilities = 0, 0, ""
	} else if in.Aggregation != nil {
		return Model{}, errors.New("only aggregation providers accept aggregation configuration")
	}
	now := timestamp()
	enabled := inputEnabled(in)
	tx, err := s.db.Begin()
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(
		`INSERT INTO models(id, provider_id, name, capabilities, context_length, max_tokens, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.ProviderID, in.Name, in.Capabilities, in.ContextLength, in.MaxTokens, enabled, now, now,
	)
	if err != nil {
		return Model{}, err
	}
	if in.Aggregation != nil {
		if err := s.saveAggregationConfig(tx, in.ProviderID, in.ID, *in.Aggregation); err != nil {
			return Model{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Model{}, err
	}
	return Model{ID: in.ID, ProviderID: in.ProviderID, Name: in.Name, Capabilities: in.Capabilities, ContextLength: in.ContextLength, MaxTokens: in.MaxTokens, Enabled: enabled, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) UpdateModel(in ModelInput) (Model, error) {
	if err := validateModel(in); err != nil {
		return Model{}, err
	}
	var providerType string
	if err := s.db.QueryRow(`SELECT type FROM providers WHERE id = ?`, in.ProviderID).Scan(&providerType); err != nil {
		return Model{}, err
	}
	if providerType == AggregationProviderType {
		if in.Aggregation == nil {
			return Model{}, errors.New("aggregation model configuration is required")
		}
		if err := s.validateAggregationConfig(*in.Aggregation); err != nil {
			return Model{}, err
		}
		in.ContextLength, in.MaxTokens, in.Capabilities = 0, 0, ""
	} else if in.Aggregation != nil {
		return Model{}, errors.New("only aggregation providers accept aggregation configuration")
	}
	now := timestamp()
	enabled := inputEnabled(in)
	tx, err := s.db.Begin()
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(
		`UPDATE models SET name = ?, capabilities = ?, context_length = ?, max_tokens = ?, enabled = ?, updated_at = ? WHERE provider_id = ? AND id = ?`,
		in.Name, in.Capabilities, in.ContextLength, in.MaxTokens, enabled, now, in.ProviderID, in.ID,
	)
	if err != nil {
		return Model{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Model{}, sql.ErrNoRows
	}
	if in.Aggregation != nil {
		if err := s.saveAggregationConfig(tx, in.ProviderID, in.ID, *in.Aggregation); err != nil {
			return Model{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Model{}, err
	}
	return Model{ID: in.ID, ProviderID: in.ProviderID, Name: in.Name, Capabilities: in.Capabilities, ContextLength: in.ContextLength, MaxTokens: in.MaxTokens, Enabled: enabled, UpdatedAt: now}, nil
}

func (s *Store) DeleteModel(providerID, id string) error {
	providerID = strings.TrimSpace(providerID)
	id = strings.TrimSpace(id)
	if providerID == "" || id == "" {
		return errors.New("provider id and model id are required")
	}
	_, _ = s.db.Exec(`DELETE FROM aggregation_configs WHERE model_id = ?`, aggregationConfigKey(providerID, id))
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
	rows, err := s.db.Query(`
SELECT m.id, m.provider_id, m.name, m.capabilities, m.context_length, m.max_tokens, m.enabled, m.created_at, m.updated_at
FROM models m
JOIN providers p ON p.id = m.provider_id
WHERE m.enabled = 1
ORDER BY m.provider_id, m.name`)
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

func (s *Store) ListAPIKeys() ([]APIKey, error) {
	rows, err := s.db.Query(`SELECT id, name, description, key, COALESCE(deleted_at, ''), created_at, updated_at FROM api_keys WHERE deleted_at IS NULL ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []APIKey
	for rows.Next() {
		var key APIKey
		if err := rows.Scan(&key.ID, &key.Name, &key.Description, &key.Key, &key.DeletedAt, &key.CreatedAt, &key.UpdatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) CreateAPIKey(in APIKeyInput) (APIKey, error) {
	if err := validateAPIKeyInput(in); err != nil {
		return APIKey{}, err
	}
	now := timestamp()
	for i := 0; i < 3; i++ {
		key, err := newAPIKey()
		if err != nil {
			return APIKey{}, err
		}
		result, err := s.db.Exec(
			`INSERT INTO api_keys(name, description, key, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), key, now, now,
		)
		if err == nil {
			id, _ := result.LastInsertId()
			return APIKey{ID: id, Name: strings.TrimSpace(in.Name), Description: strings.TrimSpace(in.Description), Key: key, CreatedAt: now, UpdatedAt: now}, nil
		}
		if !strings.Contains(err.Error(), "UNIQUE") {
			return APIKey{}, err
		}
	}
	return APIKey{}, errors.New("api key generation collided")
}

func (s *Store) UpdateAPIKey(in APIKeyInput) (APIKey, error) {
	if in.ID <= 0 {
		return APIKey{}, errors.New("api key id is required")
	}
	if err := validateAPIKeyInput(in); err != nil {
		return APIKey{}, err
	}
	now := timestamp()
	result, err := s.db.Exec(
		`UPDATE api_keys SET name = ?, description = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), now, in.ID,
	)
	if err != nil {
		return APIKey{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return APIKey{}, sql.ErrNoRows
	}
	var key APIKey
	err = s.db.QueryRow(`SELECT id, name, description, key, COALESCE(deleted_at, ''), created_at, updated_at FROM api_keys WHERE id = ?`, in.ID).
		Scan(&key.ID, &key.Name, &key.Description, &key.Key, &key.DeletedAt, &key.CreatedAt, &key.UpdatedAt)
	return key, err
}

func (s *Store) DeleteAPIKey(id int64) error {
	if id <= 0 {
		return errors.New("api key id is required")
	}
	now := timestamp()
	result, err := s.db.Exec(`UPDATE api_keys SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) AppNameForAuthorization(header string) string {
	value := strings.TrimSpace(header)
	if value == "" {
		return NoAppName
	}
	prefix, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(prefix, "Bearer") || strings.TrimSpace(token) == "" {
		return NoAppName
	}
	return s.AppNameForAPIKey(token)
}

// AppNameForAPIKey resolves a raw gateway API key. Native Anthropic and
// Gemini clients pass the key outside the Authorization: Bearer header.
func (s *Store) AppNameForAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return NoAppName
	}
	var name string
	err := s.db.QueryRow(`SELECT name FROM api_keys WHERE key = ? AND deleted_at IS NULL`, apiKey).Scan(&name)
	if err != nil || strings.TrimSpace(name) == "" {
		return NoAppName
	}
	return name
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
INSERT INTO call_logs(provider_id, model_id, app_name, protocol, started_at, ended_at, status_code, error, is_stream, input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens, token_estimated, aggregation_source, agg_attempts)
VALUES `)
	args := make([]any, 0, len(logs)*16)
	for i, log := range logs {
		if log.StartedAt == "" {
			log.StartedAt = timestamp()
		}
		if strings.TrimSpace(log.AppName) == "" {
			log.AppName = NoAppName
		}
		if i > 0 {
			query.WriteString(",")
		}
		query.WriteString("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			nullable(log.ProviderID), nullable(log.ModelID), log.AppName, log.Protocol, log.StartedAt, nullable(log.EndedAt), log.StatusCode, nullable(log.Error), log.Stream,
			log.InputTokens, log.OutputTokens, log.CacheCreationInputTokens, log.CacheReadInputTokens, log.TokenEstimated, log.AggregationSource, log.AggAttempts,
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
       COALESCE(SUM(cache_creation_input_tokens), 0), COALESCE(SUM(cache_read_input_tokens), 0), COALESCE(SUM(token_estimated), 0)
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
		if err := rows.Scan(&p.Date, &p.Calls, &p.InputTokens, &p.OutputTokens, &p.CacheCreationInputTokens, &p.CacheReadInputTokens, &p.EstimatedCalls); err != nil {
			return TokenStats{}, err
		}
		stats.Calls += p.Calls
		stats.EstimatedCalls += p.EstimatedCalls
		stats.InputTokens += p.InputTokens
		stats.OutputTokens += p.OutputTokens
		stats.CacheCreationInputTokens += p.CacheCreationInputTokens
		stats.CacheReadInputTokens += p.CacheReadInputTokens
		stats.Points = append(stats.Points, p)
	}
	return stats, rows.Err()
}

func (s *Store) TokenStatModels(filter TokenStatsFilter) ([]string, error) {
	filter = normalizeStatsFilter(TokenStatsFilter{
		From:              filter.From,
		To:                filter.To,
		ProviderID:        filter.ProviderID,
		AggregationSource: filter.AggregationSource,
	})
	where, args := statsWhere(filter)
	if where == "" {
		where = " WHERE model_id IS NOT NULL AND model_id != ''"
	} else {
		where += " AND model_id IS NOT NULL AND model_id != ''"
	}
	rows, err := s.db.Query(`
SELECT DISTINCT model_id
FROM call_logs`+where+`
ORDER BY model_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var models []string
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, rows.Err()
}

func (s *Store) TokenStatApps(filter TokenStatsFilter) ([]string, error) {
	filter = normalizeStatsFilter(TokenStatsFilter{From: filter.From, To: filter.To, ProviderID: filter.ProviderID, ModelID: filter.ModelID, AggregationSource: filter.AggregationSource})
	where, args := statsWhere(filter)
	rows, err := s.db.Query(`
SELECT DISTINCT app_name
FROM call_logs`+where+`
ORDER BY app_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var apps []string
	for rows.Next() {
		var app string
		if err := rows.Scan(&app); err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

func (s *Store) TokenStatRows(filter TokenStatsFilter, groupBy string) ([]TokenStatRow, error) {
	filter = normalizeStatsFilter(filter)
	column, fallback, err := statGroupColumn(groupBy)
	if err != nil {
		return nil, err
	}
	where, args := statsWhere(filter)
	rows, err := s.db.Query(fmt.Sprintf(`
SELECT COALESCE(NULLIF(%s, ''), ?), COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
       COALESCE(SUM(cache_creation_input_tokens), 0), COALESCE(SUM(cache_read_input_tokens), 0)
FROM call_logs%s
GROUP BY COALESCE(NULLIF(%s, ''), ?)
ORDER BY COALESCE(SUM(input_tokens + output_tokens), 0) DESC`, column, where, column), append([]any{fallback}, append(args, fallback)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rowsOut []TokenStatRow
	var total int
	for rows.Next() {
		var row TokenStatRow
		if err := rows.Scan(&row.Name, &row.Calls, &row.InputTokens, &row.OutputTokens, &row.CacheCreationInputTokens, &row.CacheReadInputTokens); err != nil {
			return nil, err
		}
		row.TotalTokens = row.InputTokens + row.OutputTokens
		total += row.TotalTokens
		rowsOut = append(rowsOut, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range rowsOut {
		if total > 0 {
			rowsOut[i].Share = float64(rowsOut[i].TotalTokens) / float64(total)
		}
	}
	return rowsOut, nil
}

func (s *Store) TokenTrend(filter TokenStatsFilter, grain string, groupBy string) ([]TokenTrendPoint, error) {
	if err := validateStatsGrain(filter, grain); err != nil {
		return nil, err
	}
	filter = normalizeStatsFilter(filter)
	bucket, err := statBucketExpr(grain)
	if err != nil {
		return nil, err
	}
	column, fallback, err := statGroupColumn(groupBy)
	if err != nil {
		return nil, err
	}
	where, args := statsWhere(filter)
	rows, err := s.db.Query(fmt.Sprintf(`
SELECT %s, COALESCE(NULLIF(%s, ''), ?), COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
       COALESCE(SUM(cache_creation_input_tokens), 0), COALESCE(SUM(cache_read_input_tokens), 0)
FROM call_logs%s
GROUP BY %s, COALESCE(NULLIF(%s, ''), ?)
ORDER BY %s`, bucket, column, where, bucket, column, bucket), append([]any{fallback}, append(args, fallback)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var points []TokenTrendPoint
	for rows.Next() {
		var point TokenTrendPoint
		if err := rows.Scan(&point.Bucket, &point.Name, &point.Calls, &point.InputTokens, &point.OutputTokens, &point.CacheCreationInputTokens, &point.CacheReadInputTokens); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

func (s *Store) CallLogs(filter TokenStatsFilter, page int, pageSize int) (CallLogPage, error) {
	filter = normalizeStatsFilter(filter)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 10000 {
		pageSize = 50
	}
	where, args := statsWhere(filter)
	var out CallLogPage
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM call_logs`+where, args...).Scan(&out.Total); err != nil {
		return CallLogPage{}, err
	}
	rows, err := s.db.Query(`
SELECT id, COALESCE(provider_id, ''), COALESCE(model_id, ''), app_name, protocol, started_at, COALESCE(ended_at, ''),
       COALESCE(status_code, 0), COALESCE(error, ''), is_stream, input_tokens, output_tokens,
       cache_creation_input_tokens, cache_read_input_tokens, token_estimated, aggregation_source, agg_attempts
FROM call_logs`+where+`
ORDER BY started_at DESC, id DESC
LIMIT ? OFFSET ?`, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return CallLogPage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var log CallLog
		if err := rows.Scan(&log.ID, &log.ProviderID, &log.ModelID, &log.AppName, &log.Protocol, &log.StartedAt, &log.EndedAt, &log.StatusCode, &log.Error, &log.Stream, &log.InputTokens, &log.OutputTokens, &log.CacheCreationInputTokens, &log.CacheReadInputTokens, &log.TokenEstimated, &log.AggregationSource, &log.AggAttempts); err != nil {
			return CallLogPage{}, err
		}
		log.DurationMs = durationMs(log.StartedAt, log.EndedAt)
		out.Items = append(out.Items, log)
	}
	return out, rows.Err()
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
	if strings.TrimSpace(filter.AppName) != "" {
		clauses = append(clauses, "app_name = ?")
		args = append(args, filter.AppName)
	}
	if strings.TrimSpace(filter.AggregationSource) != "" {
		clauses = append(clauses, "aggregation_source = ?")
		args = append(args, filter.AggregationSource)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func statGroupColumn(groupBy string) (string, string, error) {
	switch groupBy {
	case "", "provider":
		return "provider_id", "未知平台", nil
	case "model":
		return "model_id", "未知模型", nil
	case "app":
		return "app_name", NoAppName, nil
	case "aggregation":
		return "aggregation_source", "直接调用", nil
	default:
		return "", "", errors.New("unsupported stat group")
	}
}

func statBucketExpr(grain string) (string, error) {
	switch grain {
	case "", "day":
		return "substr(started_at, 1, 10)", nil
	case "hour":
		return "substr(started_at, 1, 13) || ':00'", nil
	case "week":
		return "strftime('%Y-W%W', substr(started_at, 1, 10))", nil
	default:
		return "", errors.New("unsupported stat grain")
	}
}

func validateStatsGrain(filter TokenStatsFilter, grain string) error {
	if grain == "" || grain == "hour" {
		return nil
	}
	if filter.From == "" || filter.To == "" {
		return nil
	}
	from, err := time.Parse("2006-01-02", filter.From[:min(len(filter.From), len("2006-01-02"))])
	if err != nil {
		return err
	}
	to, err := time.Parse("2006-01-02", filter.To[:min(len(filter.To), len("2006-01-02"))])
	if err != nil {
		return err
	}
	days := int(to.Sub(from).Hours() / 24)
	if grain == "day" && days < 1 {
		return errors.New("day grain requires a range of at least 1 day")
	}
	if grain == "week" && days < 7 {
		return errors.New("week grain requires a range of at least 7 days")
	}
	return nil
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
	if in.Type == AggregationProviderType {
		if strings.TrimSpace(in.BaseURL) != "" || strings.TrimSpace(in.APIKey) != "" || strings.TrimSpace(in.CapabilityConfig) != "" {
			return errors.New("aggregation provider cannot have baseUrl, apiKey, or capabilityConfig")
		}
		return nil
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

func validateAPIKeyInput(in APIKeyInput) error {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return errors.New("api key name is required")
	}
	if name == NoAppName {
		return errors.New("无应用 is a reserved api key name")
	}
	return nil
}

func (s *Store) ensureModelEnabledColumn() error {
	has, err := s.hasColumn("models", "enabled")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE models ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`)
	return err
}

func (s *Store) ensureProviderCapabilityConfigColumn() error {
	has, err := s.hasColumn("providers", "capability_config")
	if err != nil {
		return err
	}
	if has {
		return s.backfillProviderCapabilityConfig()
	}
	if _, err := s.db.Exec(`ALTER TABLE providers ADD COLUMN capability_config TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	return s.backfillProviderCapabilityConfig()
}

func (s *Store) ensureAPIKeyStatsSchema() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS api_keys (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	key TEXT NOT NULL UNIQUE,
	deleted_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
`); err != nil {
		return err
	}
	for _, column := range []struct {
		name string
		sql  string
	}{
		{"app_name", `ALTER TABLE call_logs ADD COLUMN app_name TEXT NOT NULL DEFAULT '无应用'`},
		{"is_stream", `ALTER TABLE call_logs ADD COLUMN is_stream INTEGER NOT NULL DEFAULT 0`},
	} {
		has, err := s.hasColumn("call_logs", column.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := s.db.Exec(column.sql); err != nil {
				return err
			}
		}
	}
	_, err := s.db.Exec(`
CREATE INDEX IF NOT EXISTS idx_api_keys_key_active ON api_keys(key, deleted_at);
CREATE INDEX IF NOT EXISTS idx_call_logs_app_name ON call_logs(app_name);
`)
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureAppSettingsSchema() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS app_settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
)`)
	return err
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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

func generateAPIKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return "sk-" + hex.EncodeToString(buf), nil
}

func durationMs(startedAt, endedAt string) int64 {
	start, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return 0
	}
	end, err := time.Parse(time.RFC3339, endedAt)
	if err != nil {
		return 0
	}
	return end.Sub(start).Milliseconds()
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
