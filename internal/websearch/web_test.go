package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func webConfig(search, fetch string) Config {
	return Config{search, fetch, map[string]string{"tavily": "tavily-secret", "deepseek": "deepseek-secret"}}
}

func TestDeepSeekMappingAndFilters(t *testing.T) {
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.deepseek.com/v1/tools/search" || r.Header.Get("Authorization") != "Bearer deepseek-secret" {
			t.Fatal("wrong upstream")
		}
		var sent struct {
			Query   string `json:"query"`
			Count   int    `json:"count"`
			Content bool   `json:"need_content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(sent.Query, "Go site:go.dev OR site:github.com -site:example.com after:") || sent.Count != 3 || sent.Content {
			t.Fatal(sent)
		}
		return result(200, `{"results":[{"title":"Go","url":"https://go.dev","summary":"long","snippet":"short","rerank_score":2.4},{"title":"Other","snippet":"fallback","rerank_score":0}]}`)
	})
	s.load = func() (Config, error) { return webConfig("deepseek", "tavily"), nil }
	w := call(s, "POST", "/search", `{"query":"Go","max_results":3,"include_domains":["go.dev","github.com"],"exclude_domains":["example.com"],"time_range":"week"}`)
	var got Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || got.Provider != "deepseek" || got.Query != "Go" || got.Results[0].Content != "long" || got.Results[0].Score != 2.4 || got.Results[1].Content != "fallback" {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{`{"query":"` + strings.Repeat("中", 129) + `"}`, `{"query":"Go","include_domains":["evil)"]}`, `{"query":"` + strings.Repeat("a", 120) + `","include_domains":["go.dev"]}`} {
		checkError(t, call(s, "POST", "/search", body), 400, "invalid_request")
	}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for period, date := range map[string]string{"day": "2026-10-18", "week": "2026-10-12", "month": "2026-09-19", "year": "2025-10-19"} {
		query, err := deepseekQuery(Request{Query: "Go", TimeRange: period}, now)
		if err != nil || query != "Go after:"+date {
			t.Fatal(query, err)
		}
	}
	for _, body := range []string{`{}`, `null`, `{"results":null}`, `{`} {
		s.clients[0].Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return result(200, body) })
		checkError(t, call(s, "POST", "/search", `{"query":"Go"}`), 502, "search_upstream_error")
	}
	s.clients[0].Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return result(200, `{"results":[]}`) })
	if w := call(s, "POST", "/search", `{"query":"Go"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"results":[]`) {
		t.Fatal(w.Body.String())
	}
}

func TestFetchProvidersAndIndependentSelection(t *testing.T) {
	for _, id := range []string{"tavily", "deepseek"} {
		t.Run(id, func(t *testing.T) {
			s := fixture(t, func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer "+id+"-secret" || r.Header.Get("Cookie") != "" {
					t.Fatal("wrong credentials")
				}
				var input map[string]any
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				if id == "tavily" {
					if r.URL.String() != "https://api.tavily.com/extract" || input["format"] != "markdown" || input["extract_depth"] != "basic" || input["query"] != nil || input["urls"].([]any)[0] != "https://go.dev" {
						t.Fatal(input)
					}
					return result(200, `{"results":[{"url":"https://go.dev","raw_content":"你好🌍正文"}],"failed_results":[]}`)
				}
				if r.URL.String() != "https://api.deepseek.com/v1/tools/fetch" || input["url"] != "https://go.dev" || len(input) != 1 {
					t.Fatal(input)
				}
				return result(200, `{"title":"Go","content":"你好🌍正文"}`)
			})
			s.load = func() (Config, error) { return webConfig("deepseek", id), nil }
			out, err := s.Fetch(context.Background(), FetchRequest{URL: " https://go.dev ", MaxChars: 3})
			if err != nil || out.Provider != id || out.URL != "https://go.dev" || out.Content != "你好🌍" || !out.Truncated {
				t.Fatal(out, err)
			}
			out, err = s.Fetch(context.Background(), FetchRequest{URL: "https://go.dev", MaxChars: 100})
			if err != nil || out.Truncated || out.Content != "你好🌍正文" {
				t.Fatal(out, err)
			}
		})
	}
}

func TestFetchValidationDefaults(t *testing.T) {
	input, err := decodeFetchRequest([]byte(`{"url":"https://go.dev"}`))
	if err != nil || input.MaxChars != 20000 {
		t.Fatal(input, err)
	}
	for _, body := range []string{
		`{}`, `null`, `[]`, `{`, `{"url":null}`, `{"url":"https://go.dev","max_chars":null}`,
		`{"url":"https://go.dev","max_chars":0}`, `{"url":"https://go.dev","max_chars":100001}`,
		`{"url":"https://go.dev","max_chars":1.1}`, `{"url":"https://go.dev","provider":"deepseek"}`,
		`{"url":"file:///secret"}`, `{"url":"http://user:password@go.dev"}`, `{"url":"http://"}`,
		`{"url":"http://127.0.0.1"}`, `{"url":"http://[::ffff:127.0.0.1]"}`, `{"url":"http://[::1]"}`,
		`{"url":"http://10.0.0.1"}`, `{"url":"http://169.254.169.254"}`, `{"url":"http://localhost."}`,
		`{"url":"http://x.localhost"}`, `{"url":"http://%"}`, `{"url":"https://go.dev/` + strings.Repeat("a", 8192) + `"}`,
	} {
		if _, err := decodeFetchRequest([]byte(body)); err == nil {
			t.Fatal("accepted", body)
		}
	}
	s := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid reached upstream"); return nil, nil })
	if _, err := s.Fetch(context.Background(), FetchRequest{}); err == nil {
		t.Fatal("validation bypass")
	}
	if _, err := s.Search(context.Background(), Request{}); err == nil {
		t.Fatal("validation bypass")
	}
}

func TestFetchErrorsAndConfiguration(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) { return result(200, `{}`) })
	input := FetchRequest{URL: "https://go.dev", MaxChars: 100}
	for _, id := range []string{"tavily", "deepseek"} {
		s.load = func() (Config, error) { return webConfig("tavily", id), nil }
		for _, status := range []int{401, 402, 403, 404, 408, 429, 432, 433, 500, 504} {
			s.clients[0].Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return result(status, "secret") })
			_, err := s.Fetch(context.Background(), input)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		}
		for _, body := range []string{`{`, `{}`, `null`, strings.Repeat("x", (8<<20)+1)} {
			s.clients[0].Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return result(200, body) })
			if _, err := s.Fetch(context.Background(), input); err == nil {
				t.Fatal("malformed success")
			}
		}
		s.clients[0].Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
		_, err := s.Fetch(context.Background(), input)
		var failure *apiError
		if !errors.As(err, &failure) || failure.Code != "fetch_timeout" {
			t.Fatal(err)
		}
	}
	s.load = func() (Config, error) { return webConfig("deepseek", "tavily"), nil }
	for _, body := range []string{`{"results":[],"failed_results":[{"url":"https://go.dev","error":"secret"}]}`, `{"results":[{"raw_content":" "}],"failed_results":[{"url":"https://other.dev"}]}`} {
		s.clients[0].Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return result(200, body) })
		_, err := s.Fetch(context.Background(), input)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
	s.load = func() (Config, error) { return DefaultConfig(), nil }
	_, err := s.Fetch(context.Background(), input)
	var failure *apiError
	if !errors.As(err, &failure) || failure.Code != "fetch_not_configured" {
		t.Fatal(err)
	}
	s.load = func() (Config, error) { return Config{}, errors.New("private database") }
	_, err = s.Fetch(context.Background(), input)
	if !errors.As(err, &failure) || failure.Code != "fetch_unavailable" {
		t.Fatal(err)
	}
	cfg := webConfig("tavily", "missing")
	if cfg.Validate() == nil {
		t.Fatal("bad fetch provider")
	}
	cfg = webConfig("tavily", "tavily")
	cfg.APIKeys["missing"] = "key"
	if cfg.Validate() == nil {
		t.Fatal("unknown key")
	}
}

func TestSharedTransportFailures(t *testing.T) {
	if _, err := postJSON(context.Background(), nil, "key", "https://example.com", func() {}); err == nil {
		t.Fatal("marshal")
	}
	if _, err := postJSON(context.Background(), nil, "key", "https://example.com", nil); err == nil {
		t.Fatal("no transport")
	}
	if _, err := postJSON(context.Background(), []*http.Client{{}}, "key", "://", nil); err == nil {
		t.Fatal("bad endpoint")
	}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}
	var target map[string]any
	if err := readJSON(resp, "fetch", 1, &target); err == nil {
		t.Fatal("limit ignored")
	}
}
