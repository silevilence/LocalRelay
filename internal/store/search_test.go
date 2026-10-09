package store

import (
	"path/filepath"
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
	if err != nil || cfg.Provider != "tavily" || cfg.APIKey != "" {
		t.Fatal(cfg, err)
	}
	cfg.APIKey = "tvly-private-test-key"
	if err := s.SetSearchSettings(cfg); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key='web_search'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, cfg.APIKey) || !strings.Contains(raw, "apiKeyEncrypted") {
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
	if err != nil || got != cfg {
		t.Fatal("settings did not survive restart", err)
	}
	cfg.APIKey = ""
	if err := s.SetSearchSettings(cfg); err != nil {
		t.Fatal(err)
	}
	got, err = s.SearchSettings()
	if err != nil || got.APIKey != "" {
		t.Fatal("key not cleared", err)
	}
	if err := s.SetSearchSettings(websearch.Config{Provider: "unknown"}); err == nil {
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
