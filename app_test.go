package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"localrelay/internal/relay"
	"localrelay/internal/store"
)

func TestProviderModelTestSendsChatRequest(t *testing.T) {
	var upstreamModel, auth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		upstreamModel = req.Model
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer upstream.Close()

	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "p", Name: "P", Type: "openai", BaseURL: upstream.URL + "/v1", APIKey: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(store.ModelInput{ID: "m", ProviderID: "p", Name: "M"}); err != nil {
		t.Fatal(err)
	}

	result, err := (&App{store: s}).TestProviderModel("p", "m")
	if err != nil {
		t.Fatal(err)
	}
	if upstreamModel != "m" || auth != "Bearer sk-test" || result.Model != "p/m" || result.Content != "OK" {
		t.Fatalf("test result=%#v upstream=%q auth=%q", result, upstreamModel, auth)
	}
}

func TestRelayGatewayUsesWildcardListenerAndCanChangePort(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	app := &App{store: s}
	app.relay = relay.New(s)
	defer app.shutdown(nil)

	initialPort := availablePort(t)
	if err := s.SetRelayPort(initialPort); err != nil {
		t.Fatal(err)
	}
	if err := app.startRelay(initialPort); err != nil {
		t.Fatal(err)
	}
	if app.relayServer == nil {
		t.Fatal("relay server was not started")
	}
	if got := app.RelayBaseURL(); got != "http://127.0.0.1:"+strconv.Itoa(initialPort) {
		t.Fatalf("relay base URL = %q", got)
	}
	response, err := http.Get(app.RelayBaseURL() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}

	nextPort := availablePort(t)
	if _, err := app.SetRelayPort(nextPort); err != nil {
		t.Fatal(err)
	}
	if got := app.RelayBaseURL(); got != "http://127.0.0.1:"+strconv.Itoa(nextPort) {
		t.Fatalf("relay base URL after change = %q", got)
	}
}

func TestListenRelayBindsAllInterfaces(t *testing.T) {
	listener, err := listenRelay(0)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsUnspecified() {
		t.Fatalf("relay listener address = %v, want wildcard address", listener.Addr())
	}
}

func availablePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
