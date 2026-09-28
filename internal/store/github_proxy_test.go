package store

import (
	"path/filepath"
	"testing"
)

func TestGitHubProxySettingsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.GitHubProxySettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.Config.Port = 8720
	if err := s.SetGitHubProxySettings(settings); err != nil {
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
	got, err := s.GitHubProxySettings()
	if err != nil || got != settings {
		t.Fatal(got, err)
	}
}

func TestGitHubProxySettings(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	settings, err := s.GitHubProxySettings()
	if err != nil || settings.Enabled || settings.Config.Port != 8719 {
		t.Fatal(settings, err)
	}
	settings.Enabled = true
	settings.Config.Mode = "proxy"
	settings.Config.ProxyType = "socks5"
	settings.Config.ProxyAddress = "127.0.0.1:1080"
	if err := s.SetGitHubProxySettings(settings); err != nil {
		t.Fatal(err)
	}
	got, err := s.GitHubProxySettings()
	if err != nil || got != settings {
		t.Fatal(got, err)
	}
	desktop, err := s.DesktopSettings()
	if err != nil || !desktop.GatewayEnabled {
		t.Fatal("desktop settings affected", err)
	}
	settings.Config.Port = 0
	if s.SetGitHubProxySettings(settings) == nil {
		t.Fatal("invalid config saved")
	}
	for _, raw := range []string{"{", `{"config":{"port":0}}`} {
		if _, err := s.db.Exec(`UPDATE app_settings SET value=? WHERE key='github_proxy'`, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GitHubProxySettings(); err == nil {
			t.Fatal("corruption accepted")
		}
	}
	s.Close()
	if _, err := s.GitHubProxySettings(); err == nil {
		t.Fatal("closed db read")
	}
	settings.Config.Port = 8719
	if s.SetGitHubProxySettings(settings) == nil {
		t.Fatal("closed db write")
	}
}
