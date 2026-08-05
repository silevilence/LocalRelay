package relay

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
	"localrelay/internal/store"
)

func TestAggregationRuntimeStrategiesAndCooldown(t *testing.T) {
	runtime := newAggregationRuntime()
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.Local)
	runtime.now = func() time.Time { return now }
	valid := map[string]store.RoutedModel{"p/a": {Provider: store.Provider{ID: "p"}, Model: store.Model{ID: "a"}}, "p/b": {Provider: store.Provider{ID: "p"}, Model: store.Model{ID: "b"}}}
	members := []store.AggregationMember{{ProviderID: "p", ModelID: "a"}, {ProviderID: "p", ModelID: "b"}}
	primary := store.AggregationConfig{Members: members, Strategy: store.AggregationStrategy{Type: store.AggregationPrimaryBackup, CooldownSeconds: 60}}
	if got := runtime.candidates("agg", primary, valid); len(got) != 2 || got[0].Model.ID != "a" {
		t.Fatalf("primary candidates = %#v", got)
	}
	runtime.failure("agg", "p/a", 60)
	if got := runtime.candidates("agg", primary, valid); len(got) != 1 || got[0].Model.ID != "b" {
		t.Fatalf("cooldown candidates = %#v", got)
	}
	now = now.Add(time.Minute)
	if got := runtime.candidates("agg", primary, valid); len(got) != 2 {
		t.Fatalf("expired cooldown = %#v", got)
	}
	roundRobin := store.AggregationConfig{Members: members, Strategy: store.AggregationStrategy{Type: store.AggregationRoundRobin}}
	if got := runtime.candidates("round", roundRobin, valid); got[0].Model.ID != "a" {
		t.Fatalf("round first = %s", got[0].Model.ID)
	}
	if got := runtime.candidates("round", roundRobin, valid); got[0].Model.ID != "b" {
		t.Fatalf("round second = %s", got[0].Model.ID)
	}
	if got := runtime.candidates("round-skip", roundRobin, map[string]store.RoutedModel{"p/b": valid["p/b"]}); got[0].Model.ID != "b" {
		t.Fatalf("round skipped invalid member = %s", got[0].Model.ID)
	}
	runtime.success("balance", "p/a", 9)
	balance := store.AggregationConfig{Members: members, Strategy: store.AggregationStrategy{Type: store.AggregationTokenBalance}}
	if got := runtime.candidates("balance", balance, valid); got[0].Model.ID != "b" {
		t.Fatalf("balance = %s", got[0].Model.ID)
	}
	now = now.Add(time.Hour + time.Second)
	if got := runtime.candidates("balance", balance, valid); got[0].Model.ID != "a" {
		t.Fatalf("expired balance sample = %s", got[0].Model.ID)
	}
	schedule := store.AggregationConfig{Members: members, Strategy: store.AggregationStrategy{Type: store.AggregationTimeSchedule, Schedule: []store.AggregationScheduleEntry{{Hour: 10, Member: members[1]}}}}
	now = time.Date(2026, 8, 5, 10, 0, 0, 0, time.Local)
	if got := runtime.candidates("schedule", schedule, valid); got[0].Model.ID != "b" {
		t.Fatalf("schedule = %s", got[0].Model.ID)
	}
	now = now.Add(time.Hour)
	if got := runtime.candidates("schedule", schedule, valid); got[0].Model.ID != "a" {
		t.Fatalf("schedule fallback = %s", got[0].Model.ID)
	}
}

func TestAggregationPrimaryBackupRetriesHTTPFailure(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","model":"second","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	}))
	defer good.Close()
	s := openRelayStore(t)
	defer s.Close()
	for _, provider := range []store.ProviderInput{{ID: "p1", Name: "P1", Type: "openai", BaseURL: bad.URL + "/v1"}, {ID: "p2", Name: "P2", Type: "openai", BaseURL: good.URL + "/v1"}, {ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType}} {
		if _, err := s.CreateProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []store.ModelInput{{ID: "first", ProviderID: "p1", Name: "First"}, {ID: "second", ProviderID: "p2", Name: "Second"}} {
		if _, err := s.CreateModel(model); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &store.AggregationConfig{Members: []store.AggregationMember{{ProviderID: "p1", ModelID: "first"}, {ProviderID: "p2", ModelID: "second"}}, Strategy: store.AggregationStrategy{Type: store.AggregationPrimaryBackup}}})
	if err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"agg/route","messages":[{"role":"user","content":"ping"}]}`))
	response := httptest.NewRecorder()
	relay.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"model":"p2/second"`)) {
		t.Fatalf("response = %s", response.Body.String())
	}
}

func TestAggregationPrimaryBackupRetriesBeforeFirstSSEEvent(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"s\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer good.Close()
	s := openRelayStore(t)
	defer s.Close()
	for _, provider := range []store.ProviderInput{{ID: "p1", Name: "P1", Type: "openai", BaseURL: bad.URL + "/v1"}, {ID: "p2", Name: "P2", Type: "openai", BaseURL: good.URL + "/v1"}, {ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType}} {
		if _, err := s.CreateProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []store.ModelInput{{ID: "first", ProviderID: "p1", Name: "First"}, {ID: "second", ProviderID: "p2", Name: "Second"}} {
		if _, err := s.CreateModel(model); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &store.AggregationConfig{Members: []store.AggregationMember{{ProviderID: "p1", ModelID: "first"}, {ProviderID: "p2", ModelID: "second"}}}}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"agg/route","stream":true,"messages":[{"role":"user","content":"ping"}]}`))
	response := httptest.NewRecorder()
	relay.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"model":"p2/second"`)) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

func TestAggregationDoesNotRetryAfterFirstSSEEvent(t *testing.T) {
	secondCalls := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"s\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"}}]}\n\ndata: {bad}\n\n"))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer second.Close()
	s := openRelayStore(t)
	defer s.Close()
	for _, provider := range []store.ProviderInput{{ID: "p1", Name: "P1", Type: "openai", BaseURL: first.URL + "/v1"}, {ID: "p2", Name: "P2", Type: "openai", BaseURL: second.URL + "/v1"}, {ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType}} {
		if _, err := s.CreateProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []store.ModelInput{{ID: "first", ProviderID: "p1", Name: "First"}, {ID: "second", ProviderID: "p2", Name: "Second"}} {
		if _, err := s.CreateModel(model); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &store.AggregationConfig{Members: []store.AggregationMember{{ProviderID: "p1", ModelID: "first"}, {ProviderID: "p2", ModelID: "second"}}}}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	response := httptest.NewRecorder()
	relay.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"agg/route","stream":true,"messages":[{"role":"user","content":"ping"}]}`)))
	if response.Code != http.StatusOK || secondCalls != 0 || !strings.Contains(response.Body.String(), "first") {
		t.Fatalf("status/calls/body = %d/%d/%s", response.Code, secondCalls, response.Body.String())
	}
}

func TestAggregationNonFailoverStrategiesDoNotRetryHTTPFailures(t *testing.T) {
	for _, strategy := range []store.AggregationStrategy{{Type: store.AggregationRoundRobin}, {Type: store.AggregationTokenBalance}, {Type: store.AggregationTimeSchedule}} {
		t.Run(strategy.Type, func(t *testing.T) {
			firstCalls, secondCalls := 0, 0
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				firstCalls++
				http.Error(w, "no", http.StatusServiceUnavailable)
			}))
			defer bad.Close()
			good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { secondCalls++; w.WriteHeader(http.StatusOK) }))
			defer good.Close()
			s := openRelayStore(t)
			defer s.Close()
			for _, provider := range []store.ProviderInput{{ID: "p1", Name: "P1", Type: "openai", BaseURL: bad.URL + "/v1"}, {ID: "p2", Name: "P2", Type: "openai", BaseURL: good.URL + "/v1"}, {ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType}} {
				if _, err := s.CreateProvider(provider); err != nil {
					t.Fatal(err)
				}
			}
			for _, model := range []store.ModelInput{{ID: "first", ProviderID: "p1", Name: "First"}, {ID: "second", ProviderID: "p2", Name: "Second"}} {
				if _, err := s.CreateModel(model); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &store.AggregationConfig{Members: []store.AggregationMember{{ProviderID: "p1", ModelID: "first"}, {ProviderID: "p2", ModelID: "second"}}, Strategy: strategy}}); err != nil {
				t.Fatal(err)
			}
			relay := New(s)
			defer relay.Close()
			if strategy.Type == store.AggregationTimeSchedule {
				relay.aggregation.now = func() time.Time { return time.Date(2026, 8, 5, 10, 0, 0, 0, time.Local) }
			}
			response := httptest.NewRecorder()
			relay.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"agg/route","messages":[{"role":"user","content":"ping"}]}`)))
			if response.Code != http.StatusBadGateway || firstCalls != 1 || secondCalls != 0 {
				t.Fatalf("status/calls = %d/%d/%d", response.Code, firstCalls, secondCalls)
			}
		})
	}
}

func TestAggregationAllMembersUnavailableAndCanceledRequest(t *testing.T) {
	s := openRelayStore(t)
	defer s.Close()
	for _, provider := range []store.ProviderInput{{ID: "p", Name: "P", Type: "openai", BaseURL: "https://example.test/v1"}, {ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType}} {
		if _, err := s.CreateProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	enabled := false
	if _, err := s.CreateModel(store.ModelInput{ID: "m", ProviderID: "p", Name: "M", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &store.AggregationConfig{Members: []store.AggregationMember{{ProviderID: "p", ModelID: "m"}}}}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	response := httptest.NewRecorder()
	relay.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"agg/route","messages":[{"role":"user","content":"ping"}]}`)))
	if response.Code != http.StatusBadGateway || !bytes.Contains(response.Body.Bytes(), []byte(`"aggregation_error"`)) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	enabled = true
	if _, err := s.UpdateModel(store.ModelInput{ID: "m", ProviderID: "p", Name: "M", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	routed, err := s.GetRoutedModel("agg/route")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := relay.forwardAggregation(ctx, httptest.NewRecorder(), routed, inboundRequest{}, nil, nil, &store.CallLog{})
	if result.status != statusClientClosedRequest || result.err != context.Canceled {
		t.Fatalf("cancel result = %#v", result)
	}
}

func TestAggregationResponseConversionFailureDoesNotRetry(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	response := `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { firstCalls++; _, _ = w.Write([]byte(response)) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { secondCalls++; _, _ = w.Write([]byte(response)) }))
	defer second.Close()
	s := openRelayStore(t)
	defer s.Close()
	for _, provider := range []store.ProviderInput{{ID: "p1", Name: "P1", Type: "openai", BaseURL: first.URL + "/v1"}, {ID: "p2", Name: "P2", Type: "openai", BaseURL: second.URL + "/v1"}, {ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType}} {
		if _, err := s.CreateProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []store.ModelInput{{ID: "first", ProviderID: "p1", Name: "First"}, {ID: "second", ProviderID: "p2", Name: "Second"}} {
		if _, err := s.CreateModel(model); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &store.AggregationConfig{Members: []store.AggregationMember{{ProviderID: "p1", ModelID: "first"}, {ProviderID: "p2", ModelID: "second"}}}}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	routed, err := s.GetRoutedModel("agg/route")
	if err != nil {
		t.Fatal(err)
	}
	incoming, err := parseOpenAIChatRequest([]byte(`{"model":"agg/route","messages":[{"role":"user","content":"ping"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	result := relay.forwardAggregation(context.Background(), httptest.NewRecorder(), routed, incoming, func(ir.Response, capabilities.Provider) (any, error) {
		return nil, errors.New("client conversion failed")
	}, nil, &store.CallLog{})
	if result.err == nil || firstCalls != 1 || secondCalls != 0 {
		t.Fatalf("result/calls = %#v/%d/%d", result, firstCalls, secondCalls)
	}
}

func TestAggregationPrimaryBackupReturnsOrderedFailureSummary(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "first", http.StatusServiceUnavailable) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "second", http.StatusBadGateway) }))
	defer second.Close()
	s := openRelayStore(t)
	defer s.Close()
	for _, provider := range []store.ProviderInput{{ID: "p1", Name: "P1", Type: "openai", BaseURL: first.URL + "/v1"}, {ID: "p2", Name: "P2", Type: "openai", BaseURL: second.URL + "/v1"}, {ID: "agg", Name: "Aggregate", Type: store.AggregationProviderType}} {
		if _, err := s.CreateProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []store.ModelInput{{ID: "first", ProviderID: "p1", Name: "First"}, {ID: "second", ProviderID: "p2", Name: "Second"}} {
		if _, err := s.CreateModel(model); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "route", ProviderID: "agg", Name: "Route", Aggregation: &store.AggregationConfig{Members: []store.AggregationMember{{ProviderID: "p1", ModelID: "first"}, {ProviderID: "p2", ModelID: "second"}}}}); err != nil {
		t.Fatal(err)
	}
	relay := New(s)
	defer relay.Close()
	response := httptest.NewRecorder()
	relay.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"agg/route","messages":[{"role":"user","content":"ping"}]}`)))
	body := response.Body.String()
	if response.Code != http.StatusBadGateway || !strings.Contains(body, "aggregation members failed") || strings.Index(body, "p1/first") > strings.Index(body, "p2/second") {
		t.Fatalf("status/body = %d/%s", response.Code, body)
	}
}
