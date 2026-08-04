package relay

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
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
	apiKey, err := s.CreateAPIKey(store.APIKeyInput{Name: "Raycast"})
	if err != nil {
		t.Fatal(err)
	}

	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewBufferString(`{
		"model":"p1/gpt-test",
		"messages":[{"role":"user","content":"ping"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey.Key)
	resp, err := http.DefaultClient.Do(req)
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
		stats, err = s.TokenStats(store.TokenStatsFilter{ProviderID: "p1", ModelID: "gpt-test", AppName: "Raycast"})
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

func TestNativeInboundProtocolsRelayAndAttributeAPIKeys(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_test","model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m1", ProviderID: "p1", Name: "M1"}); err != nil {
		t.Fatal(err)
	}
	apiKey, err := s.CreateAPIKey(store.APIKeyInput{Name: "Native client"})
	if err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	server := httptest.NewServer(relay)
	defer server.Close()

	tests := []struct {
		name       string
		path       string
		body       string
		header     string
		expectJSON string
	}{
		{name: "anthropic", path: "/v1/messages", header: "X-Api-Key", body: `{"model":"p1/m1","max_tokens":8,"messages":[{"role":"user","content":[{"type":"text","text":"ping"}]}]}`, expectJSON: `"type":"message"`},
		{name: "gemini header key", path: "/v1beta/models/p1%2Fm1:generateContent", header: "X-Goog-Api-Key", body: `{"contents":[{"role":"user","parts":[{"text":"ping"}]}]}`, expectJSON: `"candidates"`},
		{name: "gemini query key", path: "/v1beta/models/p1%2Fm1:generateContent?key=", body: `{"contents":[{"role":"user","parts":[{"text":"ping"}]}]}`, expectJSON: `"candidates"`},
		{name: "responses", path: "/v1/responses", header: "Authorization", body: `{"model":"p1/m1","input":"ping"}`, expectJSON: `"object":"response"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if strings.HasSuffix(path, "?key=") {
				path += apiKey.Key
			}
			req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tt.header != "" {
				value := apiKey.Key
				if tt.header == "Authorization" {
					value = "Bearer " + value
				}
				req.Header.Set(tt.header, value)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), tt.expectJSON) {
				t.Fatalf("status/body = %d/%s", resp.StatusCode, body)
			}
		})
	}
	server.Close()
	relay.Close()
	stats, err := s.TokenStats(store.TokenStatsFilter{AppName: "Native client"})
	if err != nil || stats.Calls != len(tests) {
		t.Fatalf("stats/err = %#v/%v", stats, err)
	}
}

func TestNativeInboundProtocolsStreamInTheirOwnFormats(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"id":"stream_1","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			``,
			`data: {"id":"stream_1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":"stop"}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer upstream.Close()
	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m1", ProviderID: "p1", Name: "M1"}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()
	tests := []struct {
		name    string
		path    string
		body    string
		expects []string
	}{
		{name: "anthropic", path: "/v1/messages", body: `{"model":"p1/m1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"ping"}]}`, expects: []string{"event: message_start", "event: message_stop"}},
		{name: "gemini", path: "/v1beta/models/p1%2Fm1:streamGenerateContent?alt=sse", body: `{"contents":[{"role":"user","parts":[{"text":"ping"}]}]}`, expects: []string{`"candidates"`, `"finishReason":"STOP"`}},
		{name: "responses", path: "/v1/responses", body: `{"model":"p1/m1","stream":true,"input":"ping"}`, expects: []string{"event: response.created", "event: response.completed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(server.URL+tt.path, "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status/body = %d/%s", resp.StatusCode, body)
			}
			for _, expected := range tt.expects {
				if !strings.Contains(string(body), expected) {
					t.Fatalf("missing %q in stream: %s", expected, body)
				}
			}
		})
	}
}

func TestChatCompletionRelayFallsBackToEstimatedUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "gpt-test", ProviderID: "p1", Name: "GPT Test"}); err != nil {
		t.Fatal(err)
	}

	relay := New(s)
	server := httptest.NewServer(relay)
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{
		"model":"p1/gpt-test",
		"messages":[{"role":"user","content":"please reply with pong"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	server.Close()
	relay.Close()

	stats, err := s.TokenStats(store.TokenStatsFilter{ProviderID: "p1", ModelID: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 || stats.EstimatedCalls != 1 || stats.InputTokens == 0 || stats.OutputTokens == 0 {
		t.Fatalf("fallback stats = %#v", stats)
	}
	logs, err := s.CallLogs(store.TokenStatsFilter{ProviderID: "p1", ModelID: "gpt-test"}, 1, 50)
	if err != nil || len(logs.Items) != 1 || !logs.Items[0].TokenEstimated {
		t.Fatalf("fallback logs = %#v, %v", logs, err)
	}
}

func TestChatCompletionStreamFallsBackToEstimatedUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"id":"stream_1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			``,
			`data: {"id":"stream_1","choices":[{"index":0,"delta":{"content":"pong"}}]}`,
			``,
			`data: {"id":"stream_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "gpt-test", ProviderID: "p1", Name: "GPT Test"}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	server := httptest.NewServer(relay)
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{
		"model":"p1/gpt-test",
		"stream":true,
		"messages":[{"role":"user","content":"please reply with pong"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	server.Close()
	relay.Close()

	stats, err := s.TokenStats(store.TokenStatsFilter{ProviderID: "p1", ModelID: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 || stats.EstimatedCalls != 1 || stats.InputTokens == 0 || stats.OutputTokens == 0 {
		t.Fatalf("stream fallback stats = %#v", stats)
	}
}

func TestProviderStreamUsageMappings(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		stream   string
	}{
		{
			name:     "anthropic messages",
			protocol: capabilities.ProtocolAnthropic,
			stream:   "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}\n\n",
		},
		{
			name:     "gemini",
			protocol: capabilities.ProtocolGemini,
			stream:   "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"pong\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1}}\n\n",
		},
		{
			name:     "openai responses",
			protocol: capabilities.ProtocolOpenAIResponse,
			stream:   "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var usage ir.Usage
			err := forEachProviderStreamEvent(strings.NewReader(tt.stream), capabilities.Provider{Protocol: tt.protocol}, func(event ir.StreamEvent) error {
				if event.Usage != (ir.Usage{}) {
					usage = event.Usage
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if usage.InputTokens != 2 || usage.OutputTokens != 1 {
				t.Fatalf("usage = %#v", usage)
			}
		})
	}
}

func TestChatCompletionStreamRelayAndLogs(t *testing.T) {
	var upstreamModel string
	var upstreamStream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model         string `json:"model"`
			Stream        bool   `json:"stream"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		upstreamModel = req.Model
		upstreamStream = req.Stream && req.StreamOptions.IncludeUsage
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"id":"chatcmpl_stream","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			``,
			`data: {"id":"chatcmpl_stream","model":"gpt-test","choices":[{"index":0,"delta":{"content":"pong"}}]}`,
			``,
			`data: {"id":"chatcmpl_stream","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: {"id":"chatcmpl_stream","model":"gpt-test","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":2}}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"))
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "volcengine-coding", BaseURL: upstream.URL + "/v1"}); err != nil {
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
		"stream":true,
		"messages":[{"role":"user","content":"ping"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status/content-type = %d/%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if upstreamModel != "gpt-test" || !upstreamStream {
		t.Fatalf("upstream request = %q/%v", upstreamModel, upstreamStream)
	}
	out := string(body)
	if !strings.Contains(out, `"model":"p1/gpt-test"`) || !strings.Contains(out, `"content":"pong"`) || !strings.Contains(out, `data: [DONE]`) {
		t.Fatalf("stream response = %s", out)
	}
	if !strings.Contains(out, `"choices":[],"usage"`) {
		t.Fatalf("usage-only chunk must keep choices array: %s", out)
	}
	relay.Close()

	stats, err := s.TokenStats(store.TokenStatsFilter{ProviderID: "p1", ModelID: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 || stats.InputTokens != 7 || stats.OutputTokens != 3 || stats.CacheReadInputTokens != 2 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestChatCompletionRelayUsesConfiguredUpstreamProtocols(t *testing.T) {
	tests := []struct {
		name        string
		protocol    string
		providerTyp string
		path        string
		authHeader  string
		authValue   string
		response    string
	}{
		{
			name:        "anthropic",
			protocol:    capabilities.ProtocolAnthropic,
			providerTyp: "anthropic",
			path:        "/v1/messages",
			authHeader:  "X-Api-Key",
			authValue:   "sk-test",
			response:    `{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`,
		},
		{
			name:        "gemini",
			protocol:    capabilities.ProtocolGemini,
			providerTyp: "gemini",
			path:        "/v1beta/models/m1:generateContent",
			authHeader:  "X-Goog-Api-Key",
			authValue:   "sk-test",
			response:    `{"responseId":"gem_1","modelVersion":"gemini-test","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"pong"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}`,
		},
		{
			name:        "openai responses",
			protocol:    capabilities.ProtocolOpenAIResponse,
			providerTyp: "openai-responses",
			path:        "/v1/responses",
			authHeader:  "Authorization",
			authValue:   "Bearer sk-test",
			response:    `{"id":"resp_1","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"pong"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var upstreamPath, auth string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamPath = r.URL.RequestURI()
				auth = r.Header.Get(tt.authHeader)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.response)
			}))
			defer upstream.Close()

			s := openRelayStore(t)
			defer s.Close()
			cfg := `{"protocol":"` + tt.protocol + `"}`
			baseURL := upstream.URL + "/v1"
			if tt.protocol == capabilities.ProtocolGemini {
				baseURL = upstream.URL + "/v1beta"
			}
			if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: tt.providerTyp, BaseURL: baseURL, APIKey: "sk-test", CapabilityConfig: cfg}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateModel(store.ModelInput{ID: "m1", ProviderID: "p1", Name: "M1"}); err != nil {
				t.Fatal(err)
			}
			relay := New(s)
			defer relay.Close()
			server := httptest.NewServer(relay)
			defer server.Close()

			resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{"model":"p1/m1","messages":[{"role":"user","content":"ping"}],"max_tokens":16}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
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
			if resp.StatusCode != http.StatusOK || upstreamPath != tt.path || auth != tt.authValue || out.Model != "p1/m1" || out.Choices[0].Message.Content != "pong" {
				t.Fatalf("status/path/auth/out = %d/%q/%q/%#v", resp.StatusCode, upstreamPath, auth, out)
			}
		})
	}
}

func TestChatCompletionStreamMalformedUpstreamSendsErrorAndLogsFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\n\n")
	}))
	defer upstream.Close()

	path := filepath.Join(t.TempDir(), "localrelay.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL + "/v1"}); err != nil {
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
		"stream":true,
		"messages":[{"role":"user","content":"ping"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"error"`) {
		t.Fatalf("status/body = %d/%s", resp.StatusCode, body)
	}
	relay.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var statusCode int
	var logError string
	if err := db.QueryRow(`SELECT status_code, error FROM call_logs LIMIT 1`).Scan(&statusCode, &logError); err != nil {
		t.Fatal(err)
	}
	if statusCode != http.StatusBadGateway || logError == "" {
		t.Fatalf("log status/error = %d/%q", statusCode, logError)
	}
}

func TestStreamProviderResponseStopsOnClientCancel(t *testing.T) {
	cfg, err := capabilities.Parse(capabilities.DefaultJSON("openai"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = (&Server{}).streamProviderResponse(ctx, httptest.NewRecorder(), strings.NewReader("data: [DONE]\n\n"), cfg, "p1/gpt-test", ir.Request{}, &store.CallLog{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stream err = %v", err)
	}
}

func TestChatCompletionStreamRequiresFlusher(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "gpt-test", ProviderID: "p1", Name: "GPT Test"}); err != nil {
		t.Fatal(err)
	}

	relay := New(s)
	defer relay.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"p1/gpt-test",
		"stream":true,
		"messages":[{"role":"user","content":"ping"}]
	}`))
	w := &noFlushWriter{header: http.Header{}}
	relay.ServeHTTP(w, req)
	if w.status != http.StatusInternalServerError || !strings.Contains(w.body.String(), "streaming_unavailable") {
		t.Fatalf("status/body = %d/%s", w.status, w.body.String())
	}
}

func TestStreamProviderResponseReturnsWriteError(t *testing.T) {
	cfg, err := capabilities.Parse(capabilities.DefaultJSON("openai"))
	if err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("write boom")
	err = (&Server{}).streamProviderResponse(context.Background(), failWriteFlusher{header: http.Header{}, err: writeErr}, strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl_1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"x"}}]}`,
		``,
	}, "\n")), cfg, "p1/gpt-test", ir.Request{}, &store.CallLog{})
	if !errors.Is(err, writeErr) {
		t.Fatalf("stream err = %v", err)
	}
}

func TestDeepSeekReasoningContentRoundTrip(t *testing.T) {
	var upstreamReasoning any
	var upstreamContent any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		upstreamReasoning = req.Messages[1]["reasoning_content"]
		upstreamContent = req.Messages[1]["content"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_test",
			"model":"deepseek-v4-pro",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done","reasoning_content":"new thinking"},"finish_reason":"stop"}]
		}`))
	}))
	defer upstream.Close()

	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "deepseek", Name: "DeepSeek", Type: "deepseek", BaseURL: upstream.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "deepseek-v4-pro", ProviderID: "deepseek", Name: "DeepSeek V4 Pro"}); err != nil {
		t.Fatal(err)
	}

	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()
	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{
		"model":"deepseek/deepseek-v4-pro",
		"messages":[
			{"role":"user","content":"weather?"},
			{"role":"assistant","content":"","reasoning_content":"need a tool","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if upstreamReasoning != "need a tool" || upstreamContent != "" {
		t.Fatalf("upstream assistant history reasoning/content = %#v/%#v", upstreamReasoning, upstreamContent)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Choices[0].Message.Content != "done" || out.Choices[0].Message.ReasoningContent != "new thinking" {
		t.Fatalf("client response = %#v", out.Choices[0].Message)
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

func TestBadProviderCapabilityConfigIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localrelay.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p1", Name: "P1", Type: "openai", BaseURL: "https://example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE providers SET capability_config = ? WHERE id = 'p1'`, `{`); err != nil {
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
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "bad_provider_capabilities" {
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

	resp, err = http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{"model":"p1/m1","stream":true,"messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot || resp.Header.Get("Content-Type") != "text/plain" {
		t.Fatalf("stream status/content-type = %d/%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestChatRequestErrorBranches(t *testing.T) {
	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "badtype", Name: "Bad Type", Type: "anthropic", BaseURL: "https://example.test", CapabilityConfig: `{"protocol":"anthropic_messages"}`}); err != nil {
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
		{name: "bad request after route", body: `{"model":"badtype/m1","messages":[{"role":"developer","content":"x"}]}`, status: http.StatusBadRequest, code: "bad_request"},
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

func TestQueueLogAfterCloseFallsBackToDirectWrite(t *testing.T) {
	s := openRelayStore(t)
	defer s.Close()
	relay := New(s)
	relay.Close()

	relay.queueLog(store.CallLog{Protocol: "openai_chat", StartedAt: "2026-07-22T00:00:00Z", InputTokens: 2})
	stats, err := s.TokenStats(store.TokenStatsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 || stats.InputTokens != 2 {
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

type noFlushWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *noFlushWriter) Header() http.Header {
	return w.header
}

func (w *noFlushWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *noFlushWriter) WriteHeader(status int) {
	w.status = status
}

type failWriteFlusher struct {
	header http.Header
	err    error
}

func (w failWriteFlusher) Header() http.Header {
	return w.header
}

func (w failWriteFlusher) Write([]byte) (int, error) {
	return 0, w.err
}

func (w failWriteFlusher) WriteHeader(int) {}

func (w failWriteFlusher) Flush() {}
