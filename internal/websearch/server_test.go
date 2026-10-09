package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(t *testing.T, transport roundTripFunc) *Server {
	t.Helper()
	s := New(func() (Config, error) { return Config{"tavily", "test-upstream-key"}, nil }, "direct", nil)
	s.clients[0].Transport = transport
	t.Cleanup(s.Close)
	return s
}

func result(status int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}

func call(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	r.Header.Set("Authorization", "Bearer client-secret")
	r.Header.Set("Cookie", "session=client-secret")
	r.Header.Set("X-Api-Key", "client-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func checkError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if w.Code != status || body.Error.Code != code {
		t.Fatalf("got %d %s, want %d %s", w.Code, w.Body.String(), status, code)
	}
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "test-upstream-key") {
		t.Fatal("credential leaked")
	}
}

func TestSearchMapsRequestAndNormalizesResults(t *testing.T) {
	var sent Request
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.tavily.com/search" || r.Method != "POST" {
			t.Fatal(r.URL, r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-upstream-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("missing upstream authentication")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Api-Key") != "" {
			t.Fatal("forwarded client credentials")
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Fatal(err)
		}
		var flags map[string]any
		if err := json.Unmarshal(body, &flags); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"include_answer", "include_raw_content", "auto_parameters"} {
			if flags[key] != false {
				t.Fatal(key, flags[key])
			}
		}
		return result(200, `{"results":[{"title":"Go","url":"https://go.dev","content":"文档","score":0.9,"raw_content":"ignored"}],"response_time":"1.2","unknown":true}`)
	})
	w := call(s, "POST", "/search", `{"query":"  Go  ","max_results":3,"search_depth":"advanced","time_range":"week","include_domains":["go.dev"],"exclude_domains":["example.com"]}`)
	var response Response
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response.Version != "1" || response.Provider != "tavily" || response.Query != "Go" || len(response.Results) != 1 || response.Results[0].Content != "文档" {
		t.Fatal(w.Code, w.Body.String())
	}
	if sent.Query != "Go" || sent.MaxResults != 3 || sent.SearchDepth != "advanced" || sent.TimeRange != "week" || sent.IncludeDomains[0] != "go.dev" || sent.ExcludeDomains[0] != "example.com" {
		t.Fatal(sent)
	}
	w = call(s, "POST", "/search", `{"query":"Go"}`)
	if w.Code != 200 || sent.MaxResults != 5 || sent.SearchDepth != "basic" {
		t.Fatal(w.Code, sent)
	}
}

func TestSearchValidationAndDocumentation(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached upstream")
		return nil, nil
	})
	for _, body := range []string{
		`{`, `null`, `[]`, `{}`, `{"query":" "}`, `{"query":4}`, `{"query":"q"} {}`, `{"query":"q","provider":"evil"}`,
		`{"query":"q","max_results":0}`, `{"query":"q","max_results":21}`, `{"query":"q","max_results":2.5}`,
		`{"query":"q","search_depth":"fast"}`, `{"query":"q","time_range":"forever"}`, `{"query":"q","max_results":null}`,
		`{"query":"q","include_domains":["https://go.dev"]}`, `{"query":"q","exclude_domains":[""]}`,
		`{"query":"` + strings.Repeat("a", 4097) + `"}`,
		`{"query":"q","include_domains":[` + strings.Repeat(`"go.dev",`, 100) + `"go.dev"]}`,
	} {
		checkError(t, call(s, "POST", "/search", body), 400, "invalid_request")
	}
	checkError(t, call(s, "POST", "/search", strings.Repeat(" ", 33<<10)), 413, "request_too_large")
	checkError(t, call(s, "GET", "/search", ""), 405, "method_not_allowed")
	checkError(t, call(s, "POST", "/docs/search", ""), 405, "method_not_allowed")
	checkError(t, call(s, "GET", "/search/extra", ""), 404, "not_found")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/search", strings.NewReader(`{"query":"q"}`)))
	checkError(t, w, 415, "unsupported_media_type")
	for _, method := range []string{"GET", "HEAD"} {
		w := call(s, method, "/docs/search", "")
		if w.Code != 200 || w.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w)
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
		if method == "GET" {
			for _, text := range []string{"POST /search", "OMP", "max_results", "search_not_configured", "fetch", "60 秒"} {
				if !strings.Contains(w.Body.String(), text) {
					t.Fatal("docs missing", text)
				}
			}
		}
	}
}

func TestSearchUnavailableAndHotConfig(t *testing.T) {
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer updated" {
			t.Fatal("stale key")
		}
		return result(200, `{"results":[]}`)
	})
	cfg := Config{Provider: "tavily"}
	s.load = func() (Config, error) { return cfg, nil }
	checkError(t, call(s, "POST", "/search", `{"query":"q"}`), 503, "search_not_configured")
	cfg.APIKey = "updated"
	w := call(s, "POST", "/search", `{"query":"q"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"results":[]`) {
		t.Fatal(w.Body.String())
	}
	cfg.Provider = "missing"
	checkError(t, call(s, "POST", "/search", `{"query":"q"}`), 503, "search_unavailable")
	s.load = func() (Config, error) { return Config{}, errors.New("secret database error") }
	checkError(t, call(s, "POST", "/search", `{"query":"q"}`), 503, "search_unavailable")
	if call(s, "GET", "/docs/search", "").Code != 200 {
		t.Fatal("docs require configuration")
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout secret" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read secret") }
func (brokenReader) Close() error             { return nil }

func TestSearchUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		upstream, status int
		code             string
	}{
		{401, 502, "search_auth_failed"}, {403, 502, "search_auth_failed"}, {429, 429, "search_rate_limited"},
		{432, 429, "search_quota_exceeded"}, {433, 429, "search_quota_exceeded"}, {500, 502, "search_upstream_error"}, {400, 502, "search_upstream_error"},
	} {
		t.Run(tc.code+http.StatusText(tc.upstream), func(t *testing.T) {
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				return result(tc.upstream, `{"secret":"test-upstream-key"}`)
			})
			checkError(t, call(s, "POST", "/search", `{"query":"q"}`), tc.status, tc.code)
		})
	}
	for _, body := range []string{`{`, `{}`, `null`, `{"results":null}`, `{"results":"invalid"}`, strings.Repeat("x", (4<<20)+1)} {
		s := fixture(t, func(*http.Request) (*http.Response, error) { return result(200, body) })
		checkError(t, call(s, "POST", "/search", `{"query":"q"}`), 502, "search_upstream_error")
	}
	for _, err := range []error{context.DeadlineExceeded, timeoutError{}, errors.New("network secret")} {
		s := fixture(t, func(*http.Request) (*http.Response, error) { return nil, err })
		status, code := 504, "search_timeout"
		if err.Error() == "network secret" {
			status, code = 502, "search_upstream_error"
		}
		checkError(t, call(s, "POST", "/search", `{"query":"q"}`), status, code)
	}
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: brokenReader{}}, nil
	})
	checkError(t, call(s, "POST", "/search", `{"query":"q"}`), 502, "search_upstream_error")
	r := httptest.NewRequest("POST", "/search", brokenReader{})
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	checkError(t, w, 400, "invalid_request")
}

func TestSearchOnlyFailsOverBeforeRequestSent(t *testing.T) {
	for _, phase := range []string{"connect", "headers", "body", "status"} {
		t.Run(phase, func(t *testing.T) {
			var fallback int
			s := fixture(t, func(r *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(r.Context())
				if phase == "headers" {
					trace.WroteHeaders()
				}
				if phase == "body" {
					trace.WroteRequest(httptrace.WroteRequestInfo{})
				}
				if phase == "status" {
					return result(503, "failed")
				}
				return nil, errors.New("network failed")
			})
			s.clients = append(s.clients, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { fallback++; return result(200, `{"results":[]}`) })})
			w := call(s, "POST", "/search", `{"query":"q"}`)
			if phase == "connect" {
				if w.Code != 200 || fallback != 1 {
					t.Fatal(w.Code, fallback)
				}
			} else if w.Code != 502 || fallback != 0 {
				t.Fatal("submitted search retried", phase, w.Code, fallback)
			}
		})
	}
}

func TestSearchCancellationReachesUpstream(t *testing.T) {
	started, finished := make(chan struct{}), make(chan struct{})
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(finished)
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/search", strings.NewReader(`{"query":"q"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() { defer close(done); s.ServeHTTP(httptest.NewRecorder(), r) }()
	<-started
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("upstream not cancelled")
	}
	<-done
}

func TestSearchTransportPolicy(t *testing.T) {
	proxyURL, _ := url.Parse("http://127.0.0.1:7890")
	for _, mode := range []string{"direct", "auto", "proxy"} {
		clients := newClients(mode, proxyURL)
		want := 1
		if mode == "auto" {
			want = 2
		}
		if len(clients) != want {
			t.Fatal(mode, len(clients))
		}
		for i, c := range clients {
			tr := c.Transport.(*http.Transport)
			if mode == "proxy" || i == 1 {
				u, err := tr.Proxy(httptest.NewRequest("GET", "https://api.tavily.com/search", nil))
				if err != nil || u.String() != proxyURL.String() {
					t.Fatal(u, err)
				}
			} else if tr.Proxy != nil {
				t.Fatal("direct inherited system proxy")
			}
			if c.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
				t.Fatal("redirect allowed")
			}
			c.CloseIdleConnections()
		}
	}
	if len(newClients("auto", nil)) != 1 {
		t.Fatal("auto without proxy")
	}
	for _, cfg := range []Config{{"invalid", ""}, {"tavily", "key\r\n"}, {"tavily", strings.Repeat("k", 4097)}} {
		if cfg.Validate() == nil {
			t.Fatal("bad config accepted")
		}
	}
	list := Providers()
	if len(list) != 1 || list[0].ID != "tavily" {
		t.Fatal(list)
	}
	list[0].ID = "mutated"
	if Providers()[0].ID != "tavily" {
		t.Fatal("registry mutated")
	}
}

// Exercise real HTTP downstream and TLS upstream sockets without external
// network access. The request still targets api.tavily.com through a test dialer.
func TestSearchHTTPEndToEnd(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != "api.tavily.com" || r.URL.Path != "/search" || r.Header.Get("Authorization") != "Bearer test-upstream-key" {
			t.Error("unexpected upstream request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"title":"Local fixture","url":"https://example.com","content":"search works","score":1}]}`)
	}))
	defer upstream.Close()
	s := New(func() (Config, error) { return Config{"tavily", "test-upstream-key"}, nil }, "direct", nil)
	defer s.Close()
	tr := upstream.Client().Transport.(*http.Transport).Clone()
	// Test certificate belongs to example.com; test-only trust from httptest.
	tr.TLSClientConfig.ServerName = "example.com"
	dial := tr.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dial(ctx, network, upstream.Listener.Addr().String())
	}
	s.clients[0].Transport = tr
	server := httptest.NewServer(s)
	defer server.Close()
	resp, err := http.Post(server.URL+"/search", "application/json", strings.NewReader(`{"query":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body Response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || len(body.Results) != 1 || calls.Load() != 1 {
		t.Fatal(resp.StatusCode, body, calls.Load())
	}
}
