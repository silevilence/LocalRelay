package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"localrelay/internal/store"
)

func TestDeepSeekEndToEndWithAPIKey(t *testing.T) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("set DEEPSEEK_API_KEY to run DeepSeek end-to-end verification")
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.CreateProvider(store.ProviderInput{
		ID:      "deepseek",
		Name:    "DeepSeek",
		Type:    "deepseek",
		BaseURL: "https://api.deepseek.com",
		APIKey:  key,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{
		ID:         "deepseek-v4-flash",
		ProviderID: "deepseek",
		Name:       "DeepSeek V4 Flash",
	}); err != nil {
		t.Fatal(err)
	}

	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{
		"model":"deepseek/deepseek-v4-flash",
		"stream":false,
		"max_tokens":32,
		"messages":[{"role":"user","content":"Reply with one short English sentence."}]
	}`))
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
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, response = %#v", resp.StatusCode, out)
	}
	if out.Model != "deepseek/deepseek-v4-flash" || len(out.Choices) != 1 {
		t.Fatalf("response = %#v", out)
	}
	if out.Usage.PromptTokens == 0 || out.Usage.CompletionTokens == 0 {
		t.Fatalf("usage = %#v", out.Usage)
	}
	relay.Close()

	var stats store.TokenStats
	for range 20 {
		stats, err = s.TokenStats(store.TokenStatsFilter{ProviderID: "deepseek", ModelID: "deepseek-v4-flash"})
		if err != nil {
			t.Fatal(err)
		}
		if stats.Calls == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stats.Calls != 1 || stats.InputTokens == 0 || stats.OutputTokens == 0 {
		t.Fatalf("stats = %#v", stats)
	}
}
