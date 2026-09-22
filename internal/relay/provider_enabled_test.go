package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"localrelay/internal/store"
)

func TestDisabledProviderAcrossInboundProtocols(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer upstream.Close()
	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: upstream.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m", ProviderID: "p", Name: "M"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProviderEnabled("p", false); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	server := httptest.NewServer(relay)
	defer server.Close()
	for _, stream := range []bool{false, true} {
		geminiAction := "generateContent"
		if stream {
			geminiAction = "streamGenerateContent"
		}
		for _, tc := range []struct{ name, path, body string }{
			{"chat", "/v1/chat/completions", fmt.Sprintf(`{"model":"p/m","stream":%t,"messages":[{"role":"user","content":"ping"}]}`, stream)},
			{"responses", "/v1/responses", fmt.Sprintf(`{"model":"p/m","stream":%t,"input":"ping"}`, stream)},
			{"anthropic", "/v1/messages", fmt.Sprintf(`{"model":"p/m","stream":%t,"max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`, stream)},
			{"gemini", "/v1beta/models/p/m:" + geminiAction, `{"contents":[{"role":"user","parts":[{"text":"ping"}]}]}`},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				status, body := providerRequest(t, server.URL+tc.path, tc.body)
				if status != http.StatusBadRequest || !strings.Contains(body, `"code":"provider_disabled"`) || !strings.Contains(body, "provider is disabled") {
					t.Fatalf("status=%d body=%s", status, body)
				}
			})
		}
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("disabled provider received %d requests", upstreamCalls.Load())
	}
}

func TestProviderEnabledModelsList(t *testing.T) {
	s := openRelayStore(t)
	defer s.Close()
	for _, id := range []string{"active", "disabled"} {
		if _, err := s.CreateProvider(store.ProviderInput{ID: id, Name: id, Type: "openai", BaseURL: "https://example.test"}); err != nil {
			t.Fatal(err)
		}
		for _, enabled := range []bool{true, false} {
			if _, err := s.CreateModel(store.ModelInput{ID: fmt.Sprint(enabled), ProviderID: id, Name: "Model", Enabled: &enabled}); err != nil {
				t.Fatal(err)
			}
		}
	}
	relay := New(s)
	defer relay.Close()
	for _, enabled := range []bool{false, true} {
		if err := s.SetProviderEnabled("disabled", enabled); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		relay.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
		var list struct {
			Data []struct{ ID string } `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		want := 1
		if enabled {
			want = 2
		}
		if response.Code != http.StatusOK || len(list.Data) != want || list.Data[0].ID != "active/true" || (enabled && list.Data[1].ID != "disabled/true") {
			t.Fatalf("enabled=%v status=%d list=%s", enabled, response.Code, response.Body.String())
		}
	}
}

func TestAggregationSkipsDisabledProviders(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, strategy := range []string{store.AggregationPrimaryBackup, store.AggregationRoundRobin, store.AggregationTokenBalance, store.AggregationTimeSchedule} {
			t.Run(fmt.Sprintf("%s/stream=%t", strategy, stream), func(t *testing.T) {
				var firstCalls, secondCalls atomic.Int32
				upstreamHandler := func(calls *atomic.Int32) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							for _, event := range []string{
								`{"id":"s","choices":[{"index":0,"delta":{"role":"assistant","content":"hello "}}]}`,
								`{"id":"s","choices":[{"index":0,"delta":{"content":"world"}}]}`,
								`{"id":"s","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2}}`,
								`[DONE]`,
							} {
								fmt.Fprintf(w, "data: %s\n\n", event)
								w.(http.Flusher).Flush()
							}
						} else {
							w.Header().Set("Content-Type", "application/json")
							io.WriteString(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2}}`)
						}
					}
				}
				first := httptest.NewServer(upstreamHandler(&firstCalls))
				defer first.Close()
				second := httptest.NewServer(upstreamHandler(&secondCalls))
				defer second.Close()
				s := openRelayStore(t)
				defer s.Close()
				for _, provider := range []store.ProviderInput{
					{ID: "p1", Name: "First", Type: "openai", BaseURL: first.URL},
					{ID: "p2", Name: "Second", Type: "openai", BaseURL: second.URL},
					{ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType},
				} {
					if _, err := s.CreateProvider(provider); err != nil {
						t.Fatal(err)
					}
				}
				for _, id := range []string{"p1", "p2"} {
					if _, err := s.CreateModel(store.ModelInput{ID: "m", ProviderID: id, Name: "M"}); err != nil {
						t.Fatal(err)
					}
				}
				members := []store.AggregationMember{{ProviderID: "p1", ModelID: "m"}, {ProviderID: "p2", ModelID: "m"}}
				// Scheduling falls back to its first member when the scheduled
				// member is invalid, so keep the enabled fallback first.
				if strategy == store.AggregationTimeSchedule {
					members[0], members[1] = members[1], members[0]
				}
				cfg := store.AggregationConfig{Members: members, Strategy: store.AggregationStrategy{Type: strategy}}
				if strategy == store.AggregationTimeSchedule {
					for hour := 0; hour < 24; hour++ {
						cfg.Strategy.Schedule = append(cfg.Strategy.Schedule, store.AggregationScheduleEntry{Hour: hour, Member: members[1]})
					}
				}
				if _, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &cfg}); err != nil {
					t.Fatal(err)
				}
				if err := s.SetProviderEnabled("p1", false); err != nil {
					t.Fatal(err)
				}
				relay := New(s)
				defer relay.Close()
				server := httptest.NewServer(relay)
				defer server.Close()
				body := fmt.Sprintf(`{"model":"agg/route","stream":%t,"messages":[{"role":"user","content":"ping"}]}`, stream)
				status, response := providerRequest(t, server.URL+"/v1/chat/completions", body)
				if status != http.StatusOK || !strings.Contains(response, `"model":"p2/m"`) || firstCalls.Load() != 0 || secondCalls.Load() != 1 {
					t.Fatalf("status=%d response=%s calls=%d/%d", status, response, firstCalls.Load(), secondCalls.Load())
				}
				if stream {
					if !strings.Contains(response, `"content":"hello "`) || !strings.Contains(response, `"content":"world"`) || !strings.Contains(response, `"finish_reason":"stop"`) || !strings.HasSuffix(response, "data: [DONE]\n\n") {
						t.Fatalf("incomplete stream: %s", response)
					}
				} else if !strings.Contains(response, `"content":"hello world"`) {
					t.Fatalf("incomplete content: %s", response)
				}
				if err := s.SetProviderEnabled("p1", true); err != nil {
					t.Fatal(err)
				}
				status, response = providerRequest(t, server.URL+"/v1/chat/completions", body)
				if status != http.StatusOK || !strings.Contains(response, `"model":"p1/m"`) || firstCalls.Load() != 1 {
					t.Fatalf("reenabled status=%d response=%s calls=%d", status, response, firstCalls.Load())
				}
				for _, id := range []string{"p1", "p2"} {
					if err := s.SetProviderEnabled(id, false); err != nil {
						t.Fatal(err)
					}
				}
				status, response = providerRequest(t, server.URL+"/v1/chat/completions", body)
				if status != http.StatusBadGateway || !strings.Contains(response, "no enabled members") {
					t.Fatalf("no members: status=%d response=%s", status, response)
				}
				if err := s.SetProviderEnabled("agg", false); err != nil {
					t.Fatal(err)
				}
				status, response = providerRequest(t, server.URL+"/v1/chat/completions", body)
				if status != http.StatusBadRequest || !strings.Contains(response, `"code":"provider_disabled"`) || firstCalls.Load() != 1 || secondCalls.Load() != 1 {
					t.Fatalf("disabled aggregate: status=%d response=%s calls=%d/%d", status, response, firstCalls.Load(), secondCalls.Load())
				}
			})
		}
	}
}

func providerRequest(t *testing.T, url, body string) (int, string) {
	t.Helper()
	response, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data)
}
