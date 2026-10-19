package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"localrelay/internal/websearch"
)

type storedWebCredential struct {
	APIKeyEncrypted string `json:"apiKeyEncrypted"`
}
type storedSearch struct {
	Version        int                            `json:"version"`
	SearchProvider string                         `json:"searchProvider"`
	FetchProvider  string                         `json:"fetchProvider"`
	Providers      map[string]storedWebCredential `json:"providers"`
}

// web_search remains independent from resource routing and its enabled switch.
// Version 2 separates capability selections from per-provider AES-GCM secrets.
func (s *Store) SearchSettings() (websearch.Config, error) {
	cfg := websearch.DefaultConfig()
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
	if saved.Version == 0 {
		// Also accept legacy JSON during migration/recovery; never silently lose its key.
		var legacy struct {
			Provider        string `json:"provider"`
			APIKeyEncrypted string `json:"apiKeyEncrypted"`
		}
		if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
			return cfg, err
		}
		saved = storedSearch{2, legacy.Provider, legacy.Provider, map[string]storedWebCredential{legacy.Provider: {legacy.APIKeyEncrypted}}}
	}
	if saved.Version != 2 {
		return cfg, fmt.Errorf("不支持的网页服务配置版本")
	}
	cfg.SearchProvider, cfg.FetchProvider = saved.SearchProvider, saved.FetchProvider
	for id, credential := range saved.Providers {
		key, err := s.decrypt(credential.APIKeyEncrypted)
		if err != nil {
			return cfg, err
		}
		cfg.APIKeys[id] = key
	}
	return cfg, cfg.Validate()
}

func (s *Store) SetSearchSettings(cfg websearch.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	saved := storedSearch{2, cfg.SearchProvider, cfg.FetchProvider, map[string]storedWebCredential{}}
	for id, key := range cfg.APIKeys {
		encrypted, err := s.encrypt(key)
		if err != nil {
			return err
		}
		saved.Providers[id] = storedWebCredential{encrypted}
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO app_settings(key,value) VALUES ('web_search',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw))
	return err
}

// Idempotent migration: only rewrite an existing record, without creating a
// configuration for users who have never enabled search.
func (s *Store) migrateWebSettings() error {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key='web_search'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	cfg, err := s.SearchSettings()
	if err != nil {
		return err
	}
	return s.SetSearchSettings(cfg)
}
