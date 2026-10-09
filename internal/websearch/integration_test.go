package websearch

import (
	"encoding/json"
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
	s := New(func() (Config, error) { return Config{"tavily", key}, nil }, "direct", nil)
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
