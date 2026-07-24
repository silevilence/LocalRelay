package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"localrelay/internal/store"
)

func TestProviderModelTestSendsChatRequest(t *testing.T) {
	var upstreamModel, auth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		upstreamModel = req.Model
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer upstream.Close()

	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: upstream.URL + "/v1", APIKey: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m", ProviderID: "p", Name: "M"}); err != nil {
		t.Fatal(err)
	}

	result, err := (&App{store: s}).TestProviderModel("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	if upstreamModel != "m" || auth != "Bearer sk-test" || result.Model != "p/m" || result.Content != "OK" {
		t.Fatalf("test result=%#v upstream=%q auth=%q", result, upstreamModel, auth)
	}
}
