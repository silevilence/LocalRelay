package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"localrelay/internal/githubproxy"
)

// GitHubProxySettings uses its own app_settings key so independent saves cannot
// overwrite desktop/LLM settings. No credentials are accepted in proxy URLs.
type GitHubProxySettings struct {
	Config  githubproxy.Config `json:"config"`
	Enabled bool               `json:"enabled"`
}

func (s *Store) GitHubProxySettings() (GitHubProxySettings, error) {
	settings := GitHubProxySettings{Config: githubproxy.DefaultConfig()}
	var raw string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'github_proxy'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err = json.Unmarshal([]byte(raw), &settings); err != nil {
		return settings, err
	}
	return settings, settings.Config.Validate()
}

func (s *Store) SetGitHubProxySettings(settings GitHubProxySettings) error {
	if err := settings.Config.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO app_settings(key, value) VALUES ('github_proxy', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(raw))
	return err
}
