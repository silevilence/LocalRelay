package websearch

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in only: this request uses one Tavily basic search credit. Credentials
// come from the environment and are never logged or saved by this test.
func TestTavilyIntegration(t *testing.T) {
	if os.Getenv("LOCALRELAY_TAVILY_E2E") != "1" {
		t.Skip("set LOCALRELAY_TAVILY_E2E=1 to run a live Tavily search")
	}
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		t.Fatal("TAVILY_API_KEY is required for the opt-in test")
	}
	s := New(func() (Config, error) { return Config{"tavily", "tavily", map[string]string{"tavily": key}}, nil }, "direct", nil)
	defer s.Close()
	server := httptest.NewServer(s)
	defer server.Close()
	client := &http.Client{Timeout: 70 * time.Second}
	resp, err := client.Post(server.URL+"/search", "application/json", strings.NewReader(`{"query":"Go programming language official documentation","max_results":1}`))
	if err != nil {
		t.Fatal("search HTTP request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search returned HTTP %d", resp.StatusCode)
	}
	var body Response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal("invalid search JSON")
	}
	if body.Version != "1" || body.Provider != "tavily" || len(body.Results) == 0 || body.Results[0].URL == "" {
		t.Fatal("missing search results")
	}
}

// Opt-in only: each provider performs one billable search and one extraction.
func TestWebMCPIntegration(t *testing.T) {
	for _, id := range []string{"tavily", "deepseek"} {
		t.Run(id, func(t *testing.T) {
			upper := strings.ToUpper(id)
			if os.Getenv("LOCALRELAY_"+upper+"_E2E") != "1" {
				t.Skip("live web tools are opt-in")
			}
			key := os.Getenv(upper + "_API_KEY")
			if key == "" {
				t.Fatal("provider API key is required")
			}
			s := New(func() (Config, error) { return Config{id, id, map[string]string{id: key}}, nil }, "direct", nil)
			defer s.Close()
			server := httptest.NewServer(s)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 140*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "LocalRelay live verification", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/web"}, nil)
			if err != nil {
				t.Fatal("MCP initialization failed")
			}
			defer session.Close()
			for _, call := range []struct {
				name string
				args map[string]any
			}{
				{"search", map[string]any{"query": "Go 1.24 release", "max_results": 3,
					"ori_question": "What changed in Go 1.24?", "need_content": true, "add_localization": true,
					"after": "2025-01-01", "before": "2026-01-01", "search_depth": "advanced"}},
				{"fetch", map[string]any{"url": "https://go.dev/doc/", "max_chars": 2000}},
			} {
				out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: call.name, Arguments: call.args})
				if err != nil {
					t.Fatalf("%s MCP request failed", call.name)
				}
				if out.IsError {
					// LocalRelay exposes only sanitized error codes/messages here.
					t.Fatalf("%s failed: %v", call.name, out.Content)
				}
				data, err := json.Marshal(out.StructuredContent)
				if err != nil {
					t.Fatal("invalid tool JSON")
				}
				if call.name == "search" {
					var got Response
					if json.Unmarshal(data, &got) != nil || got.Provider != id || len(got.Results) == 0 || got.Results[0].URL == "" {
						t.Fatal("missing live search results")
					}
					fullText := false
					for _, item := range got.Results {
						fullText = fullText || strings.TrimSpace(item.RawContent) != ""
					}
					if !fullText {
						t.Fatal("live search did not return requested full text")
					}
					warned := map[string]bool{}
					for _, warning := range got.Warnings {
						warned[warning.Parameter] = true
						if warning.Code == "localization_unavailable" {
							t.Log("IP localization unavailable; search continued successfully")
						}
					}
					if (id == "deepseek" && !warned["search_depth"]) || (id == "tavily" && (!warned["ori_question"] || !warned["add_localization"])) {
						t.Fatal("missing unsupported parameter warnings")
					}
				} else {
					var got FetchResponse
					if json.Unmarshal(data, &got) != nil || got.Provider != id || strings.TrimSpace(got.Content) == "" {
						t.Fatal("missing live page content")
					}
				}
				t.Log(call.name + " succeeded")
			}
		})
	}
}
