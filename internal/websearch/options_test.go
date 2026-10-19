package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSearchOptionsAcrossProvidersAndEntrypoints(t *testing.T) {
	for _, id := range []string{"tavily", "deepseek"} {
		for _, endpoint := range []string{"rest", "mcp"} {
			t.Run(id+"/"+endpoint, func(t *testing.T) {
				lookups, searches := 0, 0
				s := fixture(t, func(r *http.Request) (*http.Response, error) {
					if r.URL.Host == "api64.ipify.org" {
						lookups++
						if id != "deepseek" || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "format=json" {
							t.Fatal("localization credential or provider isolation failed")
						}
						return result(200, `{"ip":"8.8.8.8"}`)
					}
					searches++
					var sent map[string]any
					if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
						t.Fatal(err)
					}
					if sent["add_localization"] != nil || sent["after"] != nil || sent["before"] != nil || sent["time_range"] != nil {
						t.Fatal("local options leaked upstream", sent)
					}
					if id == "deepseek" {
						if sent["query"] != "Go after:2025-01-01 before:2026-06-01" || sent["ori_question"] != "Go 文档有哪些更新？" || sent["need_content"] != true || sent["localization"] != "8.8.8.8" || sent["search_depth"] != nil {
							t.Fatal(sent)
						}
						return result(200, `{"results":[{"title":"Go","url":"https://go.dev","summary":"summary","content":"# Full text","rerank_score":2}]}`)
					}
					if sent["query"] != "Go" || sent["start_date"] != "2025-01-01" || sent["end_date"] != "2026-06-01" || sent["include_raw_content"] != true || sent["search_depth"] != "advanced" || sent["ori_question"] != nil || sent["need_content"] != nil || sent["localization"] != nil {
						t.Fatal(sent)
					}
					return result(200, `{"results":[{"title":"Go","url":"https://go.dev","content":"summary","raw_content":"# Full text","score":0.9}]}`)
				})
				s.load = func() (Config, error) { return webConfig(id, id), nil }
				args := `{"query":"Go","ori_question":"Go 文档有哪些更新？","need_content":true,"add_localization":true,"after":"2025","before":"2026-06-01","time_range":"week","search_depth":"advanced"}`
				var got Response
				if endpoint == "rest" {
					w := call(s, "POST", "/search", args)
					if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
						t.Fatal(w.Code, w.Body.String())
					}
				} else {
					w := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":`+args+`}}`)
					var envelope struct {
						Result struct {
							IsError    bool     `json:"isError"`
							Structured Response `json:"structuredContent"`
							Content    []struct {
								Text string `json:"text"`
							} `json:"content"`
						} `json:"result"`
					}
					if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Result.IsError {
						t.Fatal(w.Body.String())
					}
					got = envelope.Result.Structured
					if len(envelope.Result.Content) != 1 || !strings.Contains(envelope.Result.Content[0].Text, `"warnings"`) {
						t.Fatal(w.Body.String())
					}
				}
				if searches != 1 || got.Provider != id || got.Query != "Go" || len(got.Results) != 1 || got.Results[0].Content != "summary" || got.Results[0].RawContent != "# Full text" {
					t.Fatal(got)
				}
				want := map[string]string{"time_range": "parameter_overridden"}
				if id == "deepseek" {
					want["search_depth"] = "unsupported_parameter"
					if lookups != 1 {
						t.Fatal(lookups)
					}
				} else {
					want["ori_question"], want["add_localization"] = "unsupported_parameter", "unsupported_parameter"
					if lookups != 0 {
						t.Fatal(lookups)
					}
				}
				if len(got.Warnings) != len(want) {
					t.Fatal(got.Warnings)
				}
				for _, warning := range got.Warnings {
					if want[warning.Parameter] != warning.Code || warning.Message == "" {
						t.Fatal(warning)
					}
				}
			})
		}
	}
}

func TestSearchOptionValidationAndDefaults(t *testing.T) {
	for _, fragment := range []string{`"after":"2026-02-30"`, `"before":"2025-1-1"`, `"after":"tomorrow"`, `"after":"2026","before":"2025"`, `"after":"2025","before":"2025"`, `"need_content":"yes"`, `"add_localization":1`, `"ori_question":null`, `"localization":"8.8.8.8"`} {
		if _, err := decodeRequest([]byte(`{"query":"Go",` + fragment + `}`)); err == nil {
			t.Fatal("accepted invalid option", fragment)
		}
	}
	for _, id := range []string{"deepseek", "tavily"} {
		s := fixture(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "api64.ipify.org" {
				t.Fatal("unexpected IP lookup")
			}
			return result(200, `{"results":[{"content":"snippet","raw_content":"unexpected full text"}]}`)
		})
		s.load = func() (Config, error) { return webConfig(id, id), nil }
		for _, args := range []string{`{"query":"Go"}`, `{"query":"Go","add_localization":false}`} {
			w := call(s, "POST", "/search", args)
			var got Response
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Results) != 1 || got.Results[0].RawContent != "" {
				t.Fatal(w.Body.String())
			}
			warn := id == "tavily" && strings.Contains(args, "false")
			if (len(got.Warnings) == 1) != warn {
				t.Fatal(got.Warnings)
			}
		}
		for _, question := range []string{"", "  ", strings.Repeat("中", 129)} {
			args, _ := json.Marshal(map[string]any{"query": "Go", "ori_question": question})
			w := call(s, "POST", "/search", string(args))
			if id == "deepseek" {
				checkError(t, w, 400, "invalid_request")
			} else if w.Code != 200 || !strings.Contains(w.Body.String(), "unsupported_parameter") {
				t.Fatal(w.Body.String())
			}
		}
	}
}

func TestLocalizationFailureContinuesSearchAndCaches(t *testing.T) {
	lookups, searches := 0, 0
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api64.ipify.org" {
			lookups++
			return nil, errors.New("private failure details")
		}
		searches++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["localization"] != nil {
			t.Fatal(body)
		}
		return result(200, `{"results":[]}`)
	})
	s.load = func() (Config, error) { return webConfig("deepseek", "deepseek"), nil }
	for i := 0; i < 2; i++ {
		w := call(s, "POST", "/search", `{"query":"Go","add_localization":true}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "localization_unavailable") || strings.Contains(w.Body.String(), "private failure") {
			t.Fatal(w.Body.String())
		}
	}
	if lookups != 1 || searches != 2 {
		t.Fatal(lookups, searches)
	}
}

func TestPublicIPResolver(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"ip":"8.8.8.8"}`, "8.8.8.8"}, {`{"ip":"2606:4700:4700::1111"}`, "2606:4700:4700::1111"},
		{`{"ip":"::ffff:8.8.8.8"}`, "8.8.8.8"}, {`{"ip":"::ffff:192.168.1.1"}`, ""},
		{`{"ip":"127.0.0.1"}`, ""}, {`{"ip":"10.0.0.1"}`, ""}, {`{"ip":"169.254.1.1"}`, ""},
		{`{"ip":"0.0.0.0"}`, ""}, {`{"ip":"224.0.0.1"}`, ""}, {`{"ip":"::1"}`, ""},
		{`{"ip":"fd00::1"}`, ""}, {`{"ip":"fe80::1%eth0"}`, ""}, {`{"ip":"bad"}`, ""},
		{`{}`, ""}, {`{`, ""}, {strings.Repeat("x", 257), ""},
	} {
		t.Run(tc.body, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 3*time.Second {
					t.Fatal("unbounded lookup")
				}
				return result(200, tc.body)
			})}
			resolver := localizationResolver{}
			for i := 0; i < 2; i++ {
				ip, err := resolver.resolve(context.Background(), []*http.Client{client})
				if ip != tc.want || (err != nil) != (tc.want == "") {
					t.Fatal(ip, err)
				}
			}
			if calls != 1 {
				t.Fatal("cache not used")
			}
			resolver.expires = time.Now().Add(-time.Second)
			_, _ = resolver.resolve(context.Background(), []*http.Client{client})
			if calls != 2 {
				t.Fatal("cache did not expire")
			}
		})
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return result(503, "private details") })}
	if _, err := lookupPublicIP(context.Background(), []*http.Client{client}); err == nil {
		t.Fatal("status ignored")
	}
	if _, err := lookupPublicIP(context.Background(), nil); err == nil {
		t.Fatal("no transports")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resolver := localizationResolver{}
	_, _ = resolver.resolve(ctx, nil)
	if !resolver.expires.IsZero() {
		t.Fatal("caller cancellation cached")
	}
}
