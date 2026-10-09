package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localrelay/internal/store"
)

func TestSearchSettingsAndResourceListener(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := NewApp()
	a.store = s
	t.Cleanup(func() { a.stopGitHubLocked() })
	state, err := a.SearchSettings()
	if err != nil || state.Provider != "tavily" || state.HasAPIKey || len(state.Providers) != 1 {
		t.Fatal(state, err)
	}
	key := " tvly-test-secret "
	if err := a.SaveSearchSettings(SearchSettingsInput{"tavily", &key}); err != nil {
		t.Fatal(err)
	}
	state, err = a.SearchSettings()
	if err != nil || !state.HasAPIKey {
		t.Fatal(state, err)
	}
	encoded, err := json.Marshal(state)
	if err != nil || strings.Contains(string(encoded), "secret") {
		t.Fatal("key exposed in desktop state")
	}
	if err := a.SaveSearchSettings(SearchSettingsInput{Provider: "tavily"}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SearchSettings()
	if err != nil || saved.APIKey != "tvly-test-secret" {
		t.Fatal("blank draft did not preserve key", err)
	}
	if err := a.SaveSearchSettings(SearchSettingsInput{Provider: "invalid"}); err == nil {
		t.Fatal("invalid selection saved")
	}
	empty := ""
	if err := a.SaveSearchSettings(SearchSettingsInput{"tavily", &empty}); err != nil {
		t.Fatal(err)
	}
	state, _ = a.SearchSettings()
	if state.HasAPIKey {
		t.Fatal("key not cleared")
	}
	resource, err := s.GitHubProxySettings()
	if err != nil {
		t.Fatal(err)
	}
	resource.Config.Port = availableGitHubPort(t)
	if err := a.SaveGitHubProxyConfig(resource.Config); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGitHubProxyEnabled(true); err != nil {
		t.Fatal(err)
	}
	status, err := a.GitHubProxyStatus()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(status.Addresses[0].URL, "/github")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/docs/search")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/markdown; charset=utf-8" || !strings.Contains(string(body), "OMP") {
		t.Fatal(resp.Status, err)
	}
	resp, err = client.Post(base+"/search", "application/json", strings.NewReader(`{"query":"Go"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 503 || !strings.Contains(string(body), "search_not_configured") {
		t.Fatal(resp.Status, string(body), err)
	}
	for _, path := range []string{"/search/extra", "/docs/search/extra", "/github/invalid", "/npm/-/user/org.couchdb.user:x"} {
		resp, err = client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatal("resource allowlist broadened", path)
		}
	}
	old := a.githubServer
	if err := a.SaveSearchSettings(SearchSettingsInput{"tavily", &key}); err != nil {
		t.Fatal(err)
	}
	if a.githubServer != old {
		t.Fatal("saving search restarted resource listener")
	}
	resource.Config.Mode = "proxy"
	resource.Config.ProxyAddress = "127.0.0.1:7890"
	if err := a.SaveGitHubProxyConfig(resource.Config); err != nil {
		t.Fatal(err)
	}
	state, _ = a.SearchSettings()
	if !state.HasAPIKey {
		t.Fatal("resource save erased search key")
	}
	if err := a.SetGitHubProxyEnabled(false); err != nil {
		t.Fatal(err)
	}
	if resp, err := client.Get(base + "/docs/search"); err == nil {
		resp.Body.Close()
		t.Fatal("docs listener survived shutdown")
	}
	resource.Config.Port = 0
	if _, err := a.newResourceProxyHandler(resource.Config); err == nil {
		t.Fatal("invalid resource configuration accepted")
	}
	s.Close()
	if _, err := a.SearchSettings(); err == nil {
		t.Fatal("closed store read")
	}
	if err := a.SaveSearchSettings(SearchSettingsInput{Provider: "tavily"}); err == nil {
		t.Fatal("closed store save")
	}
}
