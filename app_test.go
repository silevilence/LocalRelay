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

func TestFetchProviderModelsUsesOpenAIListEndpoint(t *testing.T) {
	var auth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"ark-code-latest"},{"id":"doubao-seed-code"}]}`))
	}))
	defer upstream.Close()

	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateProvider(store.ProviderInput{ID: "volc", Name: "Volc", Type: "openai-compatible", BaseURL: upstream.URL + "/v1", APIKey: "sk-test"}); err != nil {
		t.Fatal(err)
	}

	models, err := (&App{store: s}).FetchProviderModels("volc")
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test" || len(models) != 2 || models[0].ID != "ark-code-latest" || models[1].ID != "doubao-seed-code" {
		t.Fatalf("models=%#v auth=%q", models, auth)
	}
}

func TestRelayGatewayCanChangePort(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	app := &App{store: s, listenRelay: listenLoopbackRelay}
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

func TestRelayListenAddressBindsAllInterfaces(t *testing.T) {
	if got := relayListenAddress(9123); got != "0.0.0.0:9123" {
		t.Fatalf("relay listen address = %q, want wildcard address", got)
	}
}

func TestRelayServiceCanPauseChangePortAndResume(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := &App{store: s, relay: relay.New(s), listenRelay: listenLoopbackRelay}
	defer app.shutdown(nil)

	initialPort := availablePort(t)
	if err := s.SetRelayPort(initialPort); err != nil {
		t.Fatal(err)
	}
	if err := app.startRelay(initialPort); err != nil {
		t.Fatal(err)
	}
	if enabled, err := app.SetRelayServiceEnabled(false); err != nil || enabled || app.relayServer != nil {
		t.Fatalf("pause result enabled=%v err=%v server=%v", enabled, err, app.relayServer)
	}
	nextPort := availablePort(t)
	if got, err := app.SetRelayPort(nextPort); err != nil || got != nextPort {
		t.Fatalf("saved stopped-gateway port = %d, %v", got, err)
	}
	if enabled, err := app.SetRelayServiceEnabled(true); err != nil || !enabled || app.relayServer == nil {
		t.Fatalf("resume result enabled=%v err=%v server=%v", enabled, err, app.relayServer)
	}
	response, err := http.Get(app.RelayBaseURL() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status after resume = %d", response.StatusCode)
	}
}

func TestLocalAccessAddressesAlwaysStartsWithLoopback(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetRelayPort(9123); err != nil {
		t.Fatal(err)
	}
	addresses, err := (&App{store: s}).LocalAccessAddresses()
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) == 0 || addresses[0] != (LocalAddress{URL: "http://127.0.0.1:9123", Source: "本地回环"}) {
		t.Fatalf("addresses = %#v", addresses)
	}
}

func TestAutostartCommand(t *testing.T) {
	if got := autostartCommand(`C:\Program Files\LocalRelay\LocalRelay.exe`); got != `"C:\Program Files\LocalRelay\LocalRelay.exe"` {
		t.Fatalf("autostart command = %q", got)
	}
}

func TestShouldStartMinimizedUsesOnlySetting(t *testing.T) {
	if !shouldStartMinimized(store.DesktopSettings{StartMinimized: true}) {
		t.Fatal("enabled setting did not request a minimized start")
	}
	if shouldStartMinimized(store.DesktopSettings{}) {
		t.Fatal("disabled setting requested a minimized start")
	}
}

func TestShouldInterceptCloseHonorsExplicitQuit(t *testing.T) {
	if !shouldInterceptClose(store.DesktopSettings{HideOnClose: true}, false) {
		t.Fatal("ordinary close should hide when the setting is enabled")
	}
	if shouldInterceptClose(store.DesktopSettings{HideOnClose: true}, true) {
		t.Fatal("explicit tray exit must not be intercepted as a hide")
	}
	if shouldInterceptClose(store.DesktopSettings{HideOnClose: false}, false) {
		t.Fatal("disabled hide-on-close should not intercept")
	}
}

func TestDesktopSettingWrappersPersist(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := &App{store: s}

	if settings, err := app.SetHideOnMinimize(false); err != nil || settings.HideOnMinimize {
		t.Fatalf("hide-on-minimize settings=%#v err=%v", settings, err)
	}
	if settings, err := app.SetHideOnClose(false); err != nil || settings.HideOnClose {
		t.Fatalf("hide-on-close settings=%#v err=%v", settings, err)
	}
	if settings, err := app.SetStartMinimized(false); err != nil || settings.StartMinimized {
		t.Fatalf("start-minimized settings=%#v err=%v", settings, err)
	}
	settings, err := app.DesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.HideOnMinimize || settings.HideOnClose || settings.StartMinimized {
		t.Fatalf("persisted desktop settings=%#v", settings)
	}
}

func TestRelayServiceEnableKeepsDisabledStateWhenPortIsUnavailable(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "localrelay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	settings, err := s.DesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.GatewayEnabled = false
	if err := s.SetDesktopSettings(settings); err != nil {
		t.Fatal(err)
	}

	listener, err := listenLoopbackRelay(0)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := s.SetRelayPort(port); err != nil {
		t.Fatal(err)
	}

	app := &App{store: s, relay: relay.New(s), listenRelay: listenLoopbackRelay}
	if enabled, err := app.SetRelayServiceEnabled(true); err == nil || enabled {
		t.Fatalf("enable on unavailable port enabled=%v err=%v", enabled, err)
	}
	if enabled, err := app.RelayServiceEnabled(); err != nil || enabled {
		t.Fatalf("persisted enabled state=%v err=%v", enabled, err)
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

func listenLoopbackRelay(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}
