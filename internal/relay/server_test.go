package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"localrelay/internal/store"
)

func TestChatCompletionRelayAndLogs(t *testing.T) {
	var upstreamModel, auth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		upstreamModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_test",
			"model":"gpt-test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}
		}`))
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL + "/v1", APIKey: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "gpt-test", ProviderID: "p1", Name: "GPT Test"}); err != nil {
		t.Fatal(err)
	}

	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{
		"model":"p1/gpt-test",
		"messages":[{"role":"user","content":"ping"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if upstreamModel != "gpt-test" || auth != "Bearer sk-test" {
		t.Fatalf("upstream model/auth = %q/%q", upstreamModel, auth)
	}
	if out.Model != "p1/gpt-test" || out.Choices[0].Message.Content != "pong" {
		t.Fatalf("client response = %#v", out)
	}
	relay.Close()

	var stats store.TokenStats
	for i := 0; i < 20; i++ {
		stats, err = s.TokenStats(store.TokenStatsFilter{ProviderID: "p1", ModelID: "gpt-test"})
		if err != nil {
			t.Fatal(err)
		}
		if stats.Calls == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stats.Calls != 1 || stats.InputTokens != 11 || stats.OutputTokens != 4 || stats.CacheReadInputTokens != 3 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestDisabledModelIsRejected(t *testing.T) {
	s := openRelayStore(t)
	defer s.Close()
	enabled := false
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "off", ProviderID: "p1", Name: "Off", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{"model":"p1/off","messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "model_disabled" {
		t.Fatalf("code = %q", code)
	}
}

func TestServeHTTPHealthModelsAndNotFound(t *testing.T) {
	s := openRelayStore(t)
	defer s.Close()
	relay := New(s)
	defer relay.Close()
	enabled := false
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p2", Name: "P2", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "on", ProviderID: "p1", Name: "On"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "off", ProviderID: "p1", Name: "Off", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "also-on", ProviderID: "p2", Name: "Also On"}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(relay)
	defer server.Close()
	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 2 || out.Data[0].ID != "p1/on" || out.Data[1].ID != "p2/also-on" {
		t.Fatalf("models = %#v", out.Data)
	}

	resp, err = http.Get(server.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d", resp.StatusCode)
	}
}

func TestModelRouteErrorCodes(t *testing.T) {
	s := openRelayStore(t)
	defer s.Close()
	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()

	tests := []struct {
		name   string
		model  string
		status int
		code   string
	}{
		{name: "invalid", model: "no-slash", status: http.StatusBadRequest, code: "invalid_model_id"},
		{name: "missing", model: "p1/missing", status: http.StatusNotFound, code: "model_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{"model":"`+tt.model+`","messages":[{"role":"user","content":"x"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if code := errorCode(t, resp); code != tt.code {
				t.Fatalf("code = %q", code)
			}
		})
	}
}

func TestUpstreamErrorIsPassedThroughAndLoggedTruncated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL + "/chat/completions"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m1", ProviderID: "p1", Name: "M1"}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{"model":"p1/m1","messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot || resp.Header.Get("Content-Type") != "text/plain" {
		t.Fatalf("status/content-type = %d/%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if len(truncateError(bytes.Repeat([]byte("x"), 5000))) != 4096 {
		t.Fatal("expected upstream log error truncation")
	}
}

func TestChatRequestErrorBranches(t *testing.T) {
	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "badtype", Name: "Bad Type", Type: "anthropic", BaseURL: "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m1", ProviderID: "badtype", Name: "M1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProvider(store.ProviderInput{ID: "badurl", Name: "Bad URL", Type: "openai", BaseURL: ":"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m1", ProviderID: "badurl", Name: "M1"}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()

	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "bad json", body: `{`, status: http.StatusBadRequest, code: "bad_request"},
		{name: "stream", body: `{"model":"p/m","stream":true}`, status: http.StatusBadRequest, code: "unsupported_stream"},
		{name: "bad request after route", body: `{"model":"badtype/m1","messages":[{"role":"developer","content":"x"}]}`, status: http.StatusBadRequest, code: "bad_request"},
		{name: "unsupported provider", body: `{"model":"badtype/m1","messages":[{"role":"user","content":"x"}]}`, status: http.StatusBadRequest, code: "unsupported_provider"},
		{name: "upstream error", body: `{"model":"badurl/m1","messages":[{"role":"user","content":"x"}]}`, status: http.StatusBadGateway, code: "upstream_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if code := errorCode(t, resp); code != tt.code {
				t.Fatalf("code = %q", code)
			}
		})
	}
}

func TestBadUpstreamResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m1", ProviderID: "p1", Name: "M1"}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{"model":"p1/m1","messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "bad_upstream_response" {
		t.Fatalf("code = %q", code)
	}
}

func TestRequestTooLargeAndModelsStoreError(t *testing.T) {
	s := openRelayStore(t)
	relay := New(s)
	server := httptest.NewServer(relay)
	defer server.Close()
	defer relay.Close()

	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(make([]byte, 20<<20+1)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("large status = %d", resp.StatusCode)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("models status = %d", resp.StatusCode)
	}
}

func TestHelperBranches(t *testing.T) {
	if routeStatus(errors.New("boom")) != http.StatusInternalServerError {
		t.Fatal("expected default route status")
	}
	if routeCode(errors.New("boom")) != "store_error" {
		t.Fatal("expected default route code")
	}
	if contentType("") != "application/json" {
		t.Fatal("expected default content type")
	}
	if truncateError([]byte("short")) != "short" {
		t.Fatal("expected short error unchanged")
	}

	s := openRelayStore(t)
	defer s.Close()
	relay := &Server{store: s, logs: make(chan store.CallLog, 1)}
	relay.logs <- store.CallLog{Protocol: "openai_chat"}
	relay.queueLog(store.CallLog{Protocol: "openai_chat", StartedAt: "2026-07-21T00:00:00Z"})
	stats, err := s.TokenStats(store.TokenStatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}

func openRelayStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Error.Code
}
