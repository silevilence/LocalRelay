package relay

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"localrelay/internal/capabilities"
	"localrelay/internal/store"
)

type headerRoundTripper func(*http.Request) (*http.Response, error)

func (f headerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func clientTestHeaders() http.Header {
	return http.Header{
		"Idempotency-Key":       {"request-123"},
		"X-Session-Id":          {"session-123"},
		"X-Opencode-Session":    {"opencode-123"},
		"X-Arbitrary-Extension": {"first", "second"},
		"User-Agent":            {"header-test/1.0"},
		"Accept":                {"application/json, text/event-stream"},
		"Authorization":         {"Bearer gateway-secret"},
		"X-Api-Key":             {"gateway-anthropic-secret"},
		"X-Goog-Api-Key":        {"gateway-gemini-secret"},
		"Anthropic-Version":     {"client-version"},
		"Content-Type":          {"application/client-json"},
		"Connection":            {"keep-alive, X-Hop-One", "x-hop-two, Content-Type, Authorization, Anthropic-Version"},
		"X-Hop-One":             {"hop-one"},
		"X-Hop-Two":             {"hop-two"},
		"Keep-Alive":            {"timeout=5"},
		"Proxy-Connection":      {"keep-alive"},
		"Te":                    {"trailers"},
		"Trailer":               {"X-Trailer"},
		"Transfer-Encoding":     {"chunked"},
		"Upgrade":               {"websocket"},
		"Proxy-Authenticate":    {"Basic realm=local"},
		"Proxy-Authorization":   {"Basic client-secret"},
		"Cookie":                {"session=client-secret"},
		"Content-Length":        {"999999"},
		"Content-Encoding":      {"gzip"}, // Stale metadata on a plain JSON body.
		"Expect":                {"100-continue"},
		"Accept-Encoding":       {"br, gzip"},
	}
}

func assertForwardedHeaders(t *testing.T, got http.Header) {
	t.Helper()
	for name, want := range map[string][]string{
		"Idempotency-Key": {"request-123"}, "X-Session-Id": {"session-123"},
		"X-Opencode-Session": {"opencode-123"}, "X-Arbitrary-Extension": {"first", "second"},
		"User-Agent": {"header-test/1.0"}, "Accept": {"application/json, text/event-stream"},
		"Content-Type": {"application/json"},
	} {
		if !reflect.DeepEqual(got.Values(name), want) {
			t.Errorf("%s = %q, want %q", name, got.Values(name), want)
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade", "Proxy-Authenticate", "Proxy-Authorization", "Cookie", "X-Hop-One", "X-Hop-Two", "Content-Encoding", "Expect"} {
		for actual, values := range got {
			if strings.EqualFold(actual, name) {
				t.Errorf("excluded %s leaked: %q", actual, values)
			}
		}
	}
}

func TestPostProviderHeaders(t *testing.T) {
	for _, protocol := range []string{capabilities.ProtocolOpenAIChat, capabilities.ProtocolOpenAIResponse, capabilities.ProtocolAnthropic, capabilities.ProtocolGemini} {
		for _, key := range []string{"", "upstream-secret"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/key=%t/stream=%t", protocol, key != "", stream), func(t *testing.T) {
					headers := clientTestHeaders()
					// Exercise noncanonical names as well as normal net/http input.
					for _, name := range []string{"Authorization", "Cookie", "Content-Type", "Connection", "X-Hop-One", "Content-Encoding", "Expect"} {
						headers[strings.ToLower(name)] = headers[name]
						delete(headers, name)
					}
					headers["Host"] = []string{"client-host.invalid"}
					original := headers.Clone()
					server := &Server{client: &http.Client{Transport: headerRoundTripper(func(r *http.Request) (*http.Response, error) {
						assertForwardedHeaders(t, r.Header)
						wantAuth := http.Header{}
						if protocol == capabilities.ProtocolAnthropic {
							wantAuth.Set("Anthropic-Version", "2023-06-01")
						}
						if key != "" {
							switch protocol {
							case capabilities.ProtocolAnthropic:
								wantAuth.Set("X-Api-Key", key)
							case capabilities.ProtocolGemini:
								wantAuth.Set("X-Goog-Api-Key", key)
							default:
								wantAuth.Set("Authorization", "Bearer "+key)
							}
						}
						for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Anthropic-Version"} {
							if !reflect.DeepEqual(r.Header.Values(name), wantAuth.Values(name)) {
								t.Errorf("%s = %q, want %q", name, r.Header.Values(name), wantAuth.Values(name))
							}
						}
						for name := range r.Header {
							if strings.Contains(strings.Join(r.Header[name], ","), "gateway-") || strings.EqualFold(name, "Host") || strings.EqualFold(name, "Accept-Encoding") || strings.EqualFold(name, "Content-Length") {
								t.Errorf("unexpected client header %s: %q", name, r.Header[name])
							}
						}
						body, err := io.ReadAll(r.Body)
						if err != nil || r.ContentLength != int64(len(body)) {
							t.Errorf("body length = %d/%d, err=%v", r.ContentLength, len(body), err)
						}
						// Even a transport modifying a value slice must not affect retries.
						r.Header["X-Arbitrary-Extension"][0] = "changed"
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
					})}}
					for range 2 {
						resp, err := server.postProvider(context.Background(), store.Provider{BaseURL: "http://upstream.test/v1", APIKey: key}, capabilities.Provider{Protocol: protocol}, "m", stream, []byte(`{"model":"m"}`), headers)
						if err != nil {
							t.Fatal(err)
						}
						resp.Body.Close()
					}
					if !reflect.DeepEqual(headers, original) {
						t.Fatal("postProvider mutated the inbound snapshot")
					}
				})
			}
		}
	}
}

func TestUpstreamHeadersNil(t *testing.T) {
	headers := upstreamHeaders(nil)
	if headers == nil || len(headers) != 0 {
		t.Fatalf("nil input should produce an empty writable header map, got %#v", headers)
	}
	headers.Set("Content-Type", "application/json")
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("header write = %q", got)
	}
	if next := upstreamHeaders(nil); len(next) != 0 {
		t.Fatalf("nil inputs must not share headers: %#v", next)
	}
}

func TestConnectionHeaderExclusionsAreRequestLocal(t *testing.T) {
	first := http.Header{
		"Connection":   {"x-session-id"},
		"X-Session-Id": {"first-session"},
	}
	if got := upstreamHeaders(first); got.Get("X-Session-Id") != "" {
		t.Fatalf("Connection-nominated session header leaked: %#v", got)
	}
	second := http.Header{"X-Session-Id": {"second-session"}}
	if got := upstreamHeaders(second).Get("X-Session-Id"); got != "second-session" {
		t.Fatalf("a previous request changed filtering: %q", got)
	}
}

// These tests use real TCP requests through the gateway to mock upstreams.
// Gzip JSON and separately flushed gzip SSE chunks exercise Go's decompression,
// the protocol parsers, and the client response writers end to end.
func TestClientHeadersHTTPEndToEnd(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, protocol := range []string{"chat", "anthropic", "responses", "gemini"} {
				t.Run(fmt.Sprintf("%s/aggregation=%t/stream=%t", protocol, aggregate, stream), func(t *testing.T) {
					seen := make(chan http.Header, 2)
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						seen <- r.Header.Clone()
						assertForwardedHeaders(t, r.Header)
						if r.Header.Get("Accept-Encoding") != "gzip" {
							t.Errorf("transport should negotiate gzip, got %q", r.Header.Get("Accept-Encoding"))
						}
						body, err := io.ReadAll(r.Body)
						if err != nil || r.ContentLength != int64(len(body)) || !json.Valid(body) {
							t.Errorf("invalid upstream body/length: %s / %d, err=%v", body, r.ContentLength, err)
						}
						if r.URL.Query().Get("key") != "" || r.Host == "client-host.invalid" {
							t.Errorf("unexpected upstream target: %s host=%s", r.URL, r.Host)
						}
						if r.URL.Path == "/primary/chat/completions" {
							if r.Header.Get("Authorization") != "Bearer primary-secret" {
								t.Error("primary credentials missing")
							}
							if stream {
								w.Header().Set("Content-Type", "text/event-stream")
								return // Force failover before the first event.
							}
							w.WriteHeader(http.StatusServiceUnavailable)
							return
						}
						for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Anthropic-Version"} {
							if r.Header.Get(name) != "" {
								t.Errorf("keyless upstream received %s", name)
							}
						}
						w.Header().Set("Content-Encoding", "gzip")
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
						} else {
							w.Header().Set("Content-Type", "application/json")
						}
						compressed := gzip.NewWriter(w)
						defer compressed.Close()
						if !stream {
							_, _ = io.WriteString(compressed, `{"id":"ok","model":"m","choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
							return
						}
						for _, chunk := range []string{
							`{"id":"ok","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
							`{"id":"ok","choices":[{"index":0,"delta":{"content":"pong"}}]}`,
							`{"id":"ok","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
							`[DONE]`,
						} {
							_, _ = fmt.Fprintf(compressed, "data: %s\n\n", chunk)
							if err := compressed.Flush(); err != nil {
								t.Error(err)
							}
							w.(http.Flusher).Flush()
						}
					}))
					defer upstream.Close()
					s := openRelayStore(t)
					defer s.Close()
					for _, p := range []store.ProviderInput{
						{ID: "p", Name: "Keyless", Type: "openai", BaseURL: upstream.URL + "/v1"},
						{ID: "primary", Name: "Primary", Type: "openai", BaseURL: upstream.URL + "/primary", APIKey: "primary-secret"},
						{ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType},
					} {
						if _, err := s.CreateProvider(p); err != nil {
							t.Fatal(err)
						}
					}
					for _, p := range []string{"p", "primary"} {
						if _, err := s.CreateModel(store.ModelInput{ProviderID: p, ID: "m", Name: "M"}); err != nil {
							t.Fatal(err)
						}
					}
					model := "p/m"
					if aggregate {
						if _, err := s.CreateModel(store.ModelInput{ProviderID: "agg", ID: "route", Name: "Route", Aggregation: &store.AggregationConfig{
							Members:  []store.AggregationMember{{ProviderID: "primary", ModelID: "m"}, {ProviderID: "p", ModelID: "m"}},
							Strategy: store.AggregationStrategy{Type: store.AggregationPrimaryBackup},
						}}); err != nil {
							t.Fatal(err)
						}
						model = "agg/route"
					}
					relay := New(s)
					defer relay.Close()
					gateway := httptest.NewServer(relay)
					defer gateway.Close()
					path := "/v1/chat/completions"
					body := fmt.Sprintf(`{"model":%q,"stream":%t,"max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`, model, stream)
					completion := "[DONE]"
					switch protocol {
					case "anthropic":
						path, completion = "/v1/messages", "event: message_stop"
					case "responses":
						path, completion = "/v1/responses", "event: response.completed"
						body = fmt.Sprintf(`{"model":%q,"stream":%t,"input":"ping"}`, model, stream)
					case "gemini":
						method := "generateContent"
						if stream {
							method = "streamGenerateContent"
						}
						path = "/v1beta/models/" + strings.ReplaceAll(model, "/", "%2F") + ":" + method + "?key=gateway-query-secret&alt=sse"
						body, completion = `{"contents":[{"role":"user","parts":[{"text":"ping"}]}]}`, `"finishReason":"STOP"`
					}
					req, err := http.NewRequest(http.MethodPost, gateway.URL+path, strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					req.Header = clientTestHeaders()
					// Wire framing belongs to net/http. The unit test checks explicit
					// Content-Length/Transfer-Encoding removal before transport.
					req.Header.Del("Content-Length")
					req.Header.Del("Transfer-Encoding")
					req.Host = "client-host.invalid"
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					var result strings.Builder
					if stream {
						scanner := bufio.NewScanner(resp.Body)
						dataEvents := 0
						for scanner.Scan() {
							line := scanner.Text()
							result.WriteString(line + "\n")
							if strings.HasPrefix(line, "data: ") {
								data := strings.TrimPrefix(line, "data: ")
								if data != "[DONE]" && !json.Valid([]byte(data)) {
									t.Errorf("invalid SSE data: %s", data)
								}
								dataEvents++
							}
						}
						if err := scanner.Err(); err != nil {
							t.Fatal(err)
						}
						if dataEvents < 2 || !strings.Contains(result.String(), completion) {
							t.Errorf("incomplete stream: %s", result.String())
						}
					} else {
						if _, err := io.Copy(&result, resp.Body); err != nil {
							t.Fatal(err)
						}
						if !json.Valid([]byte(result.String())) {
							t.Errorf("invalid JSON: %s", result.String())
						}
					}
					if resp.StatusCode != http.StatusOK || !strings.Contains(result.String(), "pong") {
						t.Fatalf("status/body=%d/%s", resp.StatusCode, result.String())
					}
					wantCalls := 1
					if aggregate {
						wantCalls = 2
					}
					if len(seen) != wantCalls {
						t.Fatalf("upstream calls=%d, want %d", len(seen), wantCalls)
					}
				})
			}
		}
	}
}
