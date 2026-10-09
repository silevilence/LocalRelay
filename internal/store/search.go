package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"localrelay/internal/websearch"
)

type storedSearch struct {
	Provider        string `json:"provider"`
	APIKeyEncrypted string `json:"apiKeyEncrypted"`
}

// Search settings have a separate key so saving resource routing or its switch
// cannot overwrite search credentials. Reuse the store's AES-GCM encryption.
func (s *Store) SearchSettings() (websearch.Config, error) {
	cfg := websearch.Config{Provider: "tavily"}
	var raw string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'web_search'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	var saved storedSearch
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return cfg, err
	}
	cfg.Provider = saved.Provider
	cfg.APIKey, err = s.decrypt(saved.APIKeyEncrypted)
	if err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func (s *Store) SetSearchSettings(cfg websearch.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	encrypted, err := s.encrypt(cfg.APIKey)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(storedSearch{cfg.Provider, encrypted})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO app_settings(key,value) VALUES ('web_search',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw))
	return err
}
