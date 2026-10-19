package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"localrelay/internal/websearch"
)

func TestSearchSettingsEncryptedAndPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.SearchSettings()
	if err != nil || cfg.SearchProvider != "tavily" || cfg.APIKeys["tavily"] != "" {
		t.Fatal(cfg, err)
	}
	cfg.APIKeys["tavily"] = "tvly-private-test-key"
	if err := s.SetSearchSettings(cfg); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key='web_search'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, cfg.APIKeys["tavily"]) || !strings.Contains(raw, "apiKeyEncrypted") {
		t.Fatal("key was not encrypted")
	}
	// Resource/desktop saves must not erase or disclose the independent key.
	resource, err := s.GitHubProxySettings()
	if err != nil {
		t.Fatal(err)
	}
	resource.Enabled = true
	if err := s.SetGitHubProxySettings(resource); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.SearchSettings()
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatal("settings did not survive restart", err)
	}
	cfg.APIKeys["tavily"] = ""
	if err := s.SetSearchSettings(cfg); err != nil {
		t.Fatal(err)
	}
	got, err = s.SearchSettings()
	if err != nil || got.APIKeys["tavily"] != "" {
		t.Fatal("key not cleared", err)
	}
	if err := s.SetSearchSettings(websearch.Config{SearchProvider: "unknown"}); err == nil {
		t.Fatal("unknown provider saved")
	}
	for _, raw := range []string{`{`, `{"provider":"unknown"}`, `{"provider":"tavily","apiKeyEncrypted":"invalid"}`, `{"provider":"tavily","apiKeyEncrypted":"YQ=="}`} {
		if _, err := s.db.Exec(`UPDATE app_settings SET value=? WHERE key='web_search'`, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SearchSettings(); err == nil {
			t.Fatal("corruption ignored")
		}
	}
	s.Close()
	if _, err := s.SearchSettings(); err == nil {
		t.Fatal("closed store read")
	}
	if err := s.SetSearchSettings(cfg); err == nil {
		t.Fatal("closed store write")
	}
}

func TestWebSettingsMigrationAndSharedCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := s.encrypt("legacy-tavily-secret")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"provider": "tavily", "apiKeyEncrypted": encrypted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO app_settings(key,value) VALUES ('web_search',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DELETE FROM schema_migrations WHERE version=12`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, err := s.SearchSettings()
	if err != nil || cfg.SearchProvider != "tavily" || cfg.FetchProvider != "tavily" || cfg.APIKeys["tavily"] != "legacy-tavily-secret" {
		t.Fatal("legacy migration failed", err)
	}
	var persisted string
	if err = s.db.QueryRow(`SELECT value FROM app_settings WHERE key='web_search'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, "legacy-tavily-secret") || !strings.Contains(persisted, `"version":2`) {
		t.Fatal("migration did not encrypt/version settings")
	}
	cfg.APIKeys["deepseek"] = "deepseek-private-secret"
	cfg.SearchProvider = "deepseek"
	if err = s.SetSearchSettings(cfg); err != nil {
		t.Fatal(err)
	}
	if err = s.migrateWebSettings(); err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchSettings()
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatal("migration not idempotent", err)
	}
	if err = s.db.QueryRow(`SELECT value FROM app_settings WHERE key='web_search'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, "deepseek-private-secret") || strings.Contains(persisted, "legacy-tavily-secret") {
		t.Fatal("plaintext credentials stored")
	}
	// Independent reads must not share a mutable credential map.
	got.APIKeys["tavily"] = "changed"
	got, err = s.SearchSettings()
	if err != nil || got.APIKeys["tavily"] != "legacy-tavily-secret" {
		t.Fatal("mutable settings alias")
	}
	for _, bad := range []string{`{"version":99}`, `{"version":2,"searchProvider":"tavily","fetchProvider":"deepseek","providers":{"unknown":{"apiKeyEncrypted":""}}}`, `{"version":2,"searchProvider":"tavily","fetchProvider":"deepseek","providers":{"tavily":{"apiKeyEncrypted":"invalid"}}}`} {
		if _, err = s.db.Exec(`UPDATE app_settings SET value=? WHERE key='web_search'`, bad); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SearchSettings(); err == nil {
			t.Fatal("bad config accepted")
		}
		if err = s.migrateWebSettings(); err == nil {
			t.Fatal("bad migration accepted")
		}
	}
	s.Close()
	if err = s.migrateWebSettings(); err == nil {
		t.Fatal("closed migration")
	}
}
