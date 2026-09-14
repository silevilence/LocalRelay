package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"localrelay/internal/capabilities"
	"localrelay/internal/store"
)

func TestStreamContinuesBeyondClientTimeout(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(fmt.Sprintf("aggregate=%t", aggregate), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				ticker := time.NewTicker(30 * time.Millisecond)
				defer ticker.Stop()
				for range 40 {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
					fmt.Fprint(w, "data: {\"id\":\"long\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n")
					w.(http.Flusher).Flush()
				}
				fmt.Fprint(w, "data: {\"id\":\"long\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":40}}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			s := openRelayStore(t)
			defer s.Close()
			if _, err := s.CreateProvider(store.ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: upstream.URL + "/v1"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateModel(store.ModelInput{ID: "m", ProviderID: "p", Name: "M"}); err != nil {
				t.Fatal(err)
			}
			model := "p/m"
			if aggregate {
				if _, err := s.CreateProvider(store.ProviderInput{ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &store.AggregationConfig{Members: []store.AggregationMember{{ProviderID: "p", ModelID: "m"}}, Strategy: store.AggregationStrategy{Type: store.AggregationRoundRobin}}}); err != nil {
					t.Fatal(err)
				}
				model = "agg/route"
			}
			relay := New(s)
			defer relay.Close()
			relay.client.Timeout = 500 * time.Millisecond
			gateway := httptest.NewServer(relay)
			defer gateway.Close()
			resp, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"ping"}]}`, model)))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			scanner := bufio.NewScanner(resp.Body)
			chunks, done := 0, false
			for scanner.Scan() {
				line := scanner.Text()
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := strings.TrimPrefix(line, "data: ")
				if data == "[DONE]" {
					done = true
					continue
				}
				if !json.Valid([]byte(data)) || strings.Contains(data, `"error"`) {
					t.Fatalf("invalid stream event: %s", data)
				}
				if strings.Contains(data, `"content":"x"`) {
					chunks++
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK || chunks != 40 || !done {
				t.Fatalf("status=%d chunks=%d done=%t", resp.StatusCode, chunks, done)
			}
			relay.Close()
			logs, err := s.CallLogs(store.TokenStatsFilter{}, 1, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(logs.Items) != 1 || logs.Items[0].StatusCode != http.StatusOK || logs.Items[0].Error != "" || logs.Items[0].InputTokens != 5 || logs.Items[0].OutputTokens != 40 {
				t.Fatalf("unexpected logs: %+v", logs)
			}
			t.Logf("complete stream: %d chunks and DONE; usage=5/40", chunks)
		})
	}
}

// Keep the non-streaming total deadline independently covered: receiving bytes
// must not extend the whole-response budget of a JSON request.
func TestNonStreamRetainsTotalTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				io.WriteString(w, " ")
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer upstream.Close()
	relay := &Server{client: &http.Client{Timeout: 200 * time.Millisecond}}
	resp, err := relay.postProvider(t.Context(), store.Provider{BaseURL: upstream.URL}, capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat}, "m", false, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if err == nil || !strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("expected non-streaming total timeout, got %v", err)
	}
}

func TestStreamTimeoutStagesAndCancellation(t *testing.T) {
	for _, tt := range []struct {
		name          string
		headers       bool
		initialBody   bool
		status        int
		cancelClient  bool
		parentTimeout bool
		wantMessage   string
	}{
		{name: "response headers", wantMessage: "upstream response timeout"},
		{name: "first body byte", headers: true, wantMessage: "upstream stream idle timeout"},
		{name: "stalled body", headers: true, initialBody: true, wantMessage: "upstream stream idle timeout"},
		{name: "error body", headers: true, status: 429, wantMessage: "upstream stream idle timeout"},
		{name: "cancel before headers", cancelClient: true, wantMessage: "context canceled"},
		{name: "cancel during body", headers: true, initialBody: true, cancelClient: true, wantMessage: "context canceled"},
		{name: "parent deadline", headers: true, initialBody: true, parentTimeout: true, wantMessage: "context deadline exceeded"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.parentTimeout {
				var end context.CancelFunc
				ctx, end = context.WithTimeout(ctx, 100*time.Millisecond)
				defer end()
			}
			upstreamCanceled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if tt.headers {
					w.Header().Set("Content-Type", "text/event-stream")
					status := tt.status
					if status == 0 {
						status = http.StatusOK
					}
					w.WriteHeader(status)
					if tt.initialBody {
						io.WriteString(w, ": heartbeat\n\n")
					}
					w.(http.Flusher).Flush()
				}
				if tt.cancelClient {
					cancel()
				}
				<-r.Context().Done()
				close(upstreamCanceled)
			}))
			defer upstream.Close()
			relay := &Server{client: &http.Client{Timeout: 200 * time.Millisecond}}
			resp, err := relay.postProvider(ctx, store.Provider{BaseURL: upstream.URL}, capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat}, "m", true, []byte(`{}`), nil)
			if err == nil {
				_, err = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
			wantCause := context.DeadlineExceeded
			if tt.cancelClient {
				wantCause = context.Canceled
			}
			if !errors.Is(err, wantCause) || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("error=%v, want %s (%v)", err, tt.wantMessage, wantCause)
			}
			if tt.parentTimeout && strings.Contains(err.Error(), "upstream stream idle") {
				t.Fatalf("parent deadline replaced by idle timeout: %v", err)
			}
			select {
			case <-upstreamCanceled:
			case <-time.After(time.Second):
				t.Fatal("upstream request was not canceled")
			}
		})
	}
}

func TestStreamBodyCloseCancelsUpstream(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": heartbeat\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	relay := &Server{client: &http.Client{Timeout: time.Minute}}
	resp, err := relay.postProvider(t.Context(), store.Provider{BaseURL: upstream.URL}, capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat}, "m", true, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("closing the response did not cancel the upstream")
	}
}

func TestStreamHeartbeatsAndPartialEventsRefreshTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// No complete SSE data event until the very end. The transport watchdog
		// must count raw bytes, including comments and partial JSON, as activity.
		parts := []string{": heartbeat\n\n", ": heartbeat\n\n", "data: ", `{"choices":`, "[]}\n\n"}
		for _, part := range parts {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(80 * time.Millisecond):
			}
			io.WriteString(w, part)
			w.(http.Flusher).Flush()
		}
	}))
	defer upstream.Close()
	relay := &Server{client: &http.Client{Timeout: 250 * time.Millisecond}}
	resp, err := relay.postProvider(t.Context(), store.Provider{BaseURL: upstream.URL}, capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat}, "m", true, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != ": heartbeat\n\n: heartbeat\n\ndata: {\"choices\":[]}\n\n" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if _, err := resp.Body.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("cleanup changed EOF into %v", err)
	}
}

func TestStreamIdleTimeoutLogsGatewayFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"s\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	s := openRelayStore(t)
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m", ProviderID: "p", Name: "M"}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	relay.client.Timeout = 200 * time.Millisecond
	gateway := httptest.NewServer(relay)
	defer gateway.Close()
	resp, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"p/m","stream":true,"messages":[{"role":"user","content":"ping"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(body), "partial") || !strings.Contains(string(body), "upstream stream idle timeout") {
		t.Fatalf("body=%s err=%v", body, err)
	}
	relay.Close()
	logs, err := s.CallLogs(store.TokenStatsFilter{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs.Items) != 1 || logs.Items[0].StatusCode != http.StatusBadGateway || !strings.Contains(logs.Items[0].Error, "upstream stream idle timeout") {
		t.Fatalf("idle timeout incorrectly classified: %+v", logs)
	}
}
