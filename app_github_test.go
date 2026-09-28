package main

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"localrelay/internal/relay"
	"localrelay/internal/store"
)

func availableGitHubPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestGitHubSaveFailureKeepsExistingListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := NewApp()
	a.store = s
	t.Cleanup(func() { a.stopGitHubLocked() })
	settings, _ := s.GitHubProxySettings()
	settings.Config.Port = availableGitHubPort(t)
	if err := a.SaveGitHubProxyConfig(settings.Config); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGitHubProxyEnabled(true); err != nil {
		t.Fatal(err)
	}
	old := a.githubServer
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_github_save BEFORE INSERT ON app_settings WHEN NEW.key='github_proxy' BEGIN SELECT RAISE(ABORT, 'save failed'); END`); err != nil {
		t.Fatal(err)
	}
	next := settings.Config
	next.Port = availableGitHubPort(t)
	if a.SaveGitHubProxyConfig(next) == nil {
		t.Fatal("expected storage failure")
	}
	if a.githubServer != old {
		t.Fatal("listener changed after failed save")
	}
	l, err := net.Listen("tcp", relayListenAddress(next.Port))
	if err != nil {
		t.Fatal("reserved port leaked", err)
	}
	l.Close()
	if a.SetGitHubProxyEnabled(false) == nil || a.githubServer != old {
		t.Fatal("failed disable stopped running service")
	}
}

func TestGitHubShutdownEndsStalledRequests(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(finished) }))
	defer server.Close()
	a := NewApp()
	a.githubServer = server.Config
	go func() {
		resp, err := http.Get(server.URL)
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	a.stopGitHubLocked()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stalled request not canceled")
	}
	if a.githubServer != nil {
		t.Fatal("service still running")
	}
}

func TestGitHubAndLLMSwitchesAreIndependent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := NewApp()
	a.store = s
	a.relay = relay.New(s)
	defer a.shutdown(context.Background())
	if err := s.SetRelayPort(availableGitHubPort(t)); err != nil {
		t.Fatal(err)
	}
	desktop, err := s.DesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	desktop.GatewayEnabled = false
	if err := s.SetDesktopSettings(desktop); err != nil {
		t.Fatal(err)
	}
	state, _ := a.GitHubProxyStatus()
	config := state.Settings.Config
	config.Port = availableGitHubPort(t)
	if err := a.SaveGitHubProxyConfig(config); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGitHubProxyEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetRelayServiceEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetRelayServiceEnabled(false); err != nil {
		t.Fatal(err)
	}
	state, _ = a.GitHubProxyStatus()
	if !state.Running {
		t.Fatal("LLM stop affected GitHub")
	}
	if _, err := a.SetRelayServiceEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGitHubProxyEnabled(false); err != nil {
		t.Fatal(err)
	}
	if a.relayServer == nil {
		t.Fatal("GitHub stop affected LLM")
	}
	url := a.RelayBaseURL()
	resp, err := http.Get(url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
}

func TestGitHubSettingsErrors(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.store = s
	settings, _ := s.GitHubProxySettings()
	s.Close()
	if _, err := a.GitHubProxyStatus(); err == nil {
		t.Fatal("closed db status")
	}
	if a.SaveGitHubProxyConfig(settings.Config) == nil {
		t.Fatal("closed db save")
	}
	if a.SetGitHubProxyEnabled(true) == nil {
		t.Fatal("closed db enable")
	}
	if a.applyGitHubLocked(settings) == nil {
		t.Fatal("closed db apply")
	}
	a.startGitHubProxy()
	if a.githubError == "" {
		t.Fatal("startup error hidden")
	}
}

func TestGitHubLifecycle(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := NewApp()
	a.store = s
	t.Cleanup(func() { a.githubMu.Lock(); a.stopGitHubLocked(); a.githubMu.Unlock() })
	state, err := a.GitHubProxyStatus()
	if err != nil || state.Running || state.Settings.Enabled || len(state.Addresses) == 0 {
		t.Fatal(state, err)
	}
	config := state.Settings.Config
	config.Port = availableGitHubPort(t)
	if err := a.SaveGitHubProxyConfig(config); err != nil {
		t.Fatal(err)
	}
	state, _ = a.GitHubProxyStatus()
	if state.Running {
		t.Fatal("save enabled service")
	}
	if err := a.SetGitHubProxyEnabled(true); err != nil {
		t.Fatal(err)
	}
	state, _ = a.GitHubProxyStatus()
	if !state.Running || !state.Settings.Enabled {
		t.Fatal(state)
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(state.Addresses[0].URL + "/invalid")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
	oldServer := a.githubServer
	config.Mode = "direct"
	if err := a.SaveGitHubProxyConfig(config); err != nil || a.githubServer != oldServer {
		t.Fatal("same-port reconfigure", err)
	}
	blocker, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	bad := config
	bad.Port = blocker.Addr().(*net.TCPAddr).Port
	if a.SaveGitHubProxyConfig(bad) == nil {
		t.Fatal("occupied port accepted")
	}
	state, _ = a.GitHubProxyStatus()
	if !state.Running || state.Settings.Config != config {
		t.Fatal("failed save changed state")
	}
	bad.Port = 0
	if a.SaveGitHubProxyConfig(bad) == nil {
		t.Fatal("invalid port")
	}
	oldURL := state.Addresses[0].URL
	config.Port = availableGitHubPort(t)
	if err := a.SaveGitHubProxyConfig(config); err != nil {
		t.Fatal(err)
	}
	if resp, err := client.Get(oldURL); err == nil {
		resp.Body.Close()
		t.Fatal("old port still listening")
	}
	if err := a.SetGitHubProxyEnabled(false); err != nil {
		t.Fatal(err)
	}
	state, _ = a.GitHubProxyStatus()
	if state.Running || state.Settings.Enabled {
		t.Fatal(state)
	}
	if resp, err := client.Get(state.Addresses[0].URL); err == nil {
		resp.Body.Close()
		t.Fatal("disabled port listening")
	}
	desktop, _ := s.DesktopSettings()
	if !desktop.GatewayEnabled {
		t.Fatal("LLM setting affected")
	}
	// Startup failure is reported without crashing the LLM application.
	settings := state.Settings
	settings.Enabled = true
	settings.Config.Port = blocker.Addr().(*net.TCPAddr).Port
	if err := s.SetGitHubProxySettings(settings); err != nil {
		t.Fatal(err)
	}
	a.startGitHubProxy()
	state, _ = a.GitHubProxyStatus()
	if state.Running || state.Error == "" {
		t.Fatal(state)
	}
	blocker.Close()
	a.startGitHubProxy()
	state, _ = a.GitHubProxyStatus()
	if !state.Running || state.Error != "" {
		t.Fatal(state)
	}
}
