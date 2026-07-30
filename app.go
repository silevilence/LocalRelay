package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
	"localrelay/internal/protocol/anthropic"
	"localrelay/internal/protocol/gemini"
	"localrelay/internal/protocol/openaichat"
	"localrelay/internal/protocol/openairesponses"
	"localrelay/internal/relay"
	"localrelay/internal/store"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx             context.Context
	store           *store.Store
	relay           *relay.Server
	relayServer     *http.Server
	relayMu         sync.Mutex
	trayMu          sync.Mutex
	trayStop        func()
	trayOnce        sync.Once
	trayGatewayItem trayMenuItem
	watchCancel     context.CancelFunc
	startMinimized  bool
	quitting        atomic.Bool
}

type trayMenuItem interface {
	SetTitle(string)
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	db, err := store.Open(defaultDBPath())
	if err != nil {
		panic(err)
	}
	a.store = db
	a.relay = relay.New(db)
	settings, err := a.store.DesktopSettings()
	if err != nil {
		panic(err)
	}
	if settings.GatewayEnabled {
		port, err := a.store.RelayPort()
		if err != nil {
			panic(err)
		}
		if err := a.startRelay(port); err != nil {
			panic(fmt.Errorf("start relay gateway: %w", err))
		}
	}
	a.startSystemTray()
	a.startWindowStateWatcher()
	a.startMinimized = shouldStartMinimized(settings)
	if a.startMinimized {
		runtime.WindowHide(a.ctx)
	}
}

// domReady repeats the startup hide after the WebView has attached. This is
// needed in wails dev, where StartHidden cannot inspect the SQLite setting
// before the lifecycle callback opens the store.
func (a *App) domReady(ctx context.Context) {
	if a.startMinimized {
		runtime.WindowHide(ctx)
	}
}

func (a *App) startRelay(port int) error {
	listener, err := listenRelay(port)
	if err != nil {
		return err
	}
	a.serveRelay(listener)
	return nil
}

func listenRelay(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
}

func (a *App) serveRelay(listener net.Listener) {
	server := &http.Server{Handler: a.relay}
	a.relayServer = server
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("relay gateway stopped unexpectedly: %v", err)
		}
	}()
}

func (a *App) shutdown(_ context.Context) {
	if a.watchCancel != nil {
		a.watchCancel()
	}
	a.relayMu.Lock()
	a.stopRelayLocked()
	a.relayMu.Unlock()
	a.stopSystemTray()
	if a.relay != nil {
		a.relay.Close()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
}

// Greet returns a greeting for the given name
func (a *App) Greet(name string) string {
	return fmt.Sprintf("你好，%s。Go 后端通信正常。", name)
}

func (a *App) ListProviders() ([]store.Provider, error) {
	return a.store.ListProviders()
}

func (a *App) ListProviderPresets() []store.ProviderPreset {
	return store.BuiltinProviderPresets()
}

func (a *App) CreateProvider(input store.ProviderInput) (store.Provider, error) {
	return a.store.CreateProvider(input)
}

func (a *App) UpdateProvider(input store.ProviderInput) (store.Provider, error) {
	return a.store.UpdateProvider(input)
}

func (a *App) DeleteProvider(id string) error {
	return a.store.DeleteProvider(id)
}

func (a *App) ListModels(providerID string) ([]store.Model, error) {
	return a.store.ListModels(providerID)
}

func (a *App) CreateModel(input store.ModelInput) (store.Model, error) {
	return a.store.CreateModel(input)
}

func (a *App) UpdateModel(input store.ModelInput) (store.Model, error) {
	return a.store.UpdateModel(input)
}

func (a *App) DeleteModel(providerID string, id string) error {
	return a.store.DeleteModel(providerID, id)
}

// ProviderModel is a model advertised by an upstream provider. It is not
// persisted until the user explicitly selects it in the UI.
type ProviderModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (a *App) FetchProviderModels(providerID string) ([]ProviderModel, error) {
	providers, err := a.store.ListProviders()
	if err != nil {
		return nil, err
	}
	var provider *store.Provider
	for i := range providers {
		if providers[i].ID == strings.TrimSpace(providerID) {
			provider = &providers[i]
			break
		}
	}
	if provider == nil {
		return nil, fmt.Errorf("provider %q was not found", providerID)
	}
	cfg, err := capabilities.Parse(provider.CapabilityConfig)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, providerModelsURL(provider.BaseURL, cfg.Protocol), nil)
	if err != nil {
		return nil, err
	}
	if provider.APIKey != "" {
		switch cfg.Protocol {
		case capabilities.ProtocolGemini:
			req.Header.Set("X-Goog-Api-Key", provider.APIKey)
		case capabilities.ProtocolAnthropic:
			req.Header.Set("X-Api-Key", provider.APIKey)
			req.Header.Set("Anthropic-Version", "2023-06-01")
		default:
			req.Header.Set("Authorization", "Bearer "+provider.APIKey)
		}
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseProviderModels(body)
}

func (a *App) ListAPIKeys() ([]store.APIKey, error) {
	return a.store.ListAPIKeys()
}

func (a *App) CreateAPIKey(input store.APIKeyInput) (store.APIKey, error) {
	return a.store.CreateAPIKey(input)
}

func (a *App) UpdateAPIKey(input store.APIKeyInput) (store.APIKey, error) {
	return a.store.UpdateAPIKey(input)
}

func (a *App) DeleteAPIKey(id int64) error {
	return a.store.DeleteAPIKey(id)
}

func (a *App) TokenStats(filter store.TokenStatsFilter) (store.TokenStats, error) {
	return a.store.TokenStats(filter)
}

func (a *App) TokenStatModels(filter store.TokenStatsFilter) ([]string, error) {
	return a.store.TokenStatModels(filter)
}

func (a *App) TokenStatApps(filter store.TokenStatsFilter) ([]string, error) {
	return a.store.TokenStatApps(filter)
}

func (a *App) TokenStatRows(filter store.TokenStatsFilter, groupBy string) ([]store.TokenStatRow, error) {
	return a.store.TokenStatRows(filter, groupBy)
}

func (a *App) TokenTrend(filter store.TokenStatsFilter, grain string, groupBy string) ([]store.TokenTrendPoint, error) {
	return a.store.TokenTrend(filter, grain, groupBy)
}

func (a *App) CallLogs(filter store.TokenStatsFilter, page int, pageSize int) (store.CallLogPage, error) {
	return a.store.CallLogs(filter, page, pageSize)
}

type ProviderTestResult struct {
	Model     string `json:"model"`
	Content   string `json:"content"`
	LatencyMs int64  `json:"latencyMs"`
}

func (a *App) TestProviderModel(providerID string, modelID string) (ProviderTestResult, error) {
	routed, err := a.store.GetRoutedModel(strings.TrimSpace(providerID) + "/" + strings.TrimSpace(modelID))
	if err != nil {
		return ProviderTestResult{}, err
	}
	cfg, err := capabilities.Parse(routed.Provider.CapabilityConfig)
	if err != nil {
		return ProviderTestResult{}, err
	}
	maxTokens := 16
	irReq := ir.Request{
		Model:    routed.Model.ID,
		Params:   ir.Params{MaxTokens: &maxTokens},
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Text("Reply with OK.")}}},
	}
	providerReq, err := testProviderRequest(irReq, cfg)
	if err != nil {
		return ProviderTestResult{}, err
	}
	body, err := json.Marshal(providerReq)
	if err != nil {
		return ProviderTestResult{}, err
	}
	req, err := http.NewRequest(http.MethodPost, providerURL(routed.Provider.BaseURL, cfg.Protocol, routed.Model.ID, false), bytes.NewReader(body))
	if err != nil {
		return ProviderTestResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if routed.Provider.APIKey != "" {
		switch cfg.Protocol {
		case capabilities.ProtocolGemini:
			req.Header.Set("X-Goog-Api-Key", routed.Provider.APIKey)
		case capabilities.ProtocolAnthropic:
			req.Header.Set("X-Api-Key", routed.Provider.APIKey)
			req.Header.Set("Anthropic-Version", "2023-06-01")
		default:
			req.Header.Set("Authorization", "Bearer "+routed.Provider.APIKey)
		}
	}

	start := time.Now()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return ProviderTestResult{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ProviderTestResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProviderTestResult{}, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	irResp, err := testProviderResponse(respBody, cfg)
	if err != nil {
		return ProviderTestResult{}, err
	}
	return ProviderTestResult{
		Model:     routed.Provider.ID + "/" + routed.Model.ID,
		Content:   firstText(irResp),
		LatencyMs: time.Since(start).Milliseconds(),
	}, nil
}

func (a *App) RelayBaseURL() string {
	port := store.DefaultRelayPort
	if a.store != nil {
		if saved, err := a.store.RelayPort(); err == nil {
			port = saved
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// LocalAddress is an address clients can use to connect to this machine's
// gateway. Source names the loopback, network interface, or hostname origin.
type LocalAddress struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

// LocalAccessAddresses lists loopback, all non-loopback IPv4 interfaces, and
// the local hostname. Virtual interfaces are intentionally retained because
// they can be valid paths for Docker, VPN, and VM clients.
func (a *App) LocalAccessAddresses() ([]LocalAddress, error) {
	port, err := a.store.RelayPort()
	if err != nil {
		return nil, err
	}
	addresses := []LocalAddress{{
		URL:    fmt.Sprintf("http://127.0.0.1:%d", port),
		Source: "本地回环",
	}}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var networkAddresses []LocalAddress
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		interfaceAddresses, err := iface.Addrs()
		if err != nil {
			// A transient or virtual-adapter lookup failure must not hide the
			// loopback address and all other usable adapters from Settings.
			log.Printf("read addresses for interface %s: %v", iface.Name, err)
			continue
		}
		for _, address := range interfaceAddresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			ipv4 := ip.To4()
			if ipv4 == nil || ipv4.IsUnspecified() || ipv4.IsLoopback() {
				continue
			}
			networkAddresses = append(networkAddresses, LocalAddress{
				URL:    fmt.Sprintf("http://%s:%d", ipv4.String(), port),
				Source: iface.Name,
			})
		}
	}
	sort.Slice(networkAddresses, func(i, j int) bool {
		if networkAddresses[i].URL == networkAddresses[j].URL {
			return networkAddresses[i].Source < networkAddresses[j].Source
		}
		return networkAddresses[i].URL < networkAddresses[j].URL
	})
	addresses = append(addresses, networkAddresses...)
	if hostname, err := os.Hostname(); err == nil && strings.TrimSpace(hostname) != "" {
		addresses = append(addresses, LocalAddress{
			URL:    fmt.Sprintf("http://%s:%d", hostname, port),
			Source: "主机名",
		})
	}
	return addresses, nil
}

// RelayPort returns the active gateway port selected in Settings.
func (a *App) RelayPort() (int, error) {
	return a.store.RelayPort()
}

// SetRelayPort applies a new gateway port. It reserves the new wildcard
// listener before stopping the old server, so an unavailable port leaves the
// current gateway untouched.
func (a *App) SetRelayPort(port int) (int, error) {
	if err := store.ValidateRelayPort(port); err != nil {
		return 0, err
	}

	a.relayMu.Lock()
	defer a.relayMu.Unlock()

	current, err := a.store.RelayPort()
	if err != nil {
		return 0, err
	}
	if current == port {
		return port, nil
	}
	settings, err := a.store.DesktopSettings()
	if err != nil {
		return 0, err
	}
	if !settings.GatewayEnabled {
		if err := a.store.SetRelayPort(port); err != nil {
			return 0, err
		}
		return port, nil
	}
	listener, err := listenRelay(port)
	if err != nil {
		return 0, fmt.Errorf("port %d is unavailable: %w", port, err)
	}
	if err := a.store.SetRelayPort(port); err != nil {
		_ = listener.Close()
		return 0, err
	}

	if a.relayServer != nil {
		shutdownWarning := a.stopRelayLocked()
		if shutdownWarning != nil && a.relayServer != nil {
			_ = a.store.SetRelayPort(current)
			_ = listener.Close()
			return 0, fmt.Errorf("stop existing relay gateway: %w", shutdownWarning)
		}
		if shutdownWarning != nil {
			a.emitGatewayError(shutdownWarning)
		}
	}
	a.serveRelay(listener)
	return port, nil
}

// RelayServiceEnabled returns whether the gateway is meant to be running.
func (a *App) RelayServiceEnabled() (bool, error) {
	settings, err := a.store.DesktopSettings()
	return settings.GatewayEnabled, err
}

// SetRelayServiceEnabled starts or stops the HTTP gateway while retaining the
// relay instance and its stores, so a later enable needs no reinitialisation.
func (a *App) SetRelayServiceEnabled(enabled bool) (bool, error) {
	a.relayMu.Lock()
	defer a.relayMu.Unlock()
	var shutdownWarning error
	settings, err := a.store.DesktopSettings()
	if err != nil {
		return false, err
	}
	if settings.GatewayEnabled == enabled {
		return enabled, nil
	}
	if enabled {
		port, err := a.store.RelayPort()
		if err != nil {
			return false, err
		}
		listener, err := listenRelay(port)
		if err != nil {
			return false, fmt.Errorf("port %d is unavailable: %w", port, err)
		}
		settings.GatewayEnabled = true
		if err := a.store.SetDesktopSettings(settings); err != nil {
			_ = listener.Close()
			return false, err
		}
		a.serveRelay(listener)
	} else {
		shutdownWarning = a.stopRelayLocked()
		if shutdownWarning != nil && a.relayServer != nil {
			return true, fmt.Errorf("stop relay gateway: %w", shutdownWarning)
		}
		settings.GatewayEnabled = false
		if err := a.store.SetDesktopSettings(settings); err != nil {
			return true, err
		}
	}
	a.updateTrayGatewayMenu(enabled)
	a.emitGatewayState(enabled)
	if shutdownWarning != nil {
		return enabled, shutdownWarning
	}
	return enabled, nil
}

func (a *App) stopRelayLocked() error {
	if a.relayServer == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := a.relayServer.Shutdown(shutdownCtx)
	cancel()
	if err == nil {
		a.relayServer = nil
		return nil
	}
	// Shutdown has already stopped accepting new connections. If its grace
	// period expires, force-close the remaining requests so the persisted
	// disabled state always agrees with the actual listener state.
	if closeErr := a.relayServer.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		return fmt.Errorf("graceful shutdown failed: %w; force close failed: %v", err, closeErr)
	}
	a.relayServer = nil
	return fmt.Errorf("graceful shutdown exceeded 3 seconds; remaining requests were force-closed: %w", err)
}

func (a *App) emitGatewayState(enabled bool) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "gateway-service-changed", enabled)
	}
}

func (a *App) emitGatewayError(err error) {
	if err == nil {
		return
	}
	log.Printf("gateway service operation: %v", err)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "gateway-service-error", err.Error())
	}
}

func providerChatURL(base string) string {
	return providerURL(base, capabilities.ProtocolOpenAIChat, "", false)
}

func providerModelsURL(base string, protocol string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages"} {
		base = strings.TrimSuffix(base, suffix)
	}
	return base + "/models"
}

func parseProviderModels(data []byte) ([]ProviderModel, error) {
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	models := make([]ProviderModel, 0, len(response.Data)+len(response.Models))
	add := func(id, name string) {
		id = strings.TrimPrefix(strings.TrimSpace(id), "models/")
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(name) == "" {
			name = id
		}
		models = append(models, ProviderModel{ID: id, Name: name})
	}
	for _, model := range response.Data {
		add(model.ID, model.ID)
	}
	for _, model := range response.Models {
		add(model.Name, model.DisplayName)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("upstream response does not contain a supported model list")
	}
	return models, nil
}

func providerURL(base string, protocol string, model string, stream bool) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	switch protocol {
	case capabilities.ProtocolAnthropic:
		if strings.HasSuffix(base, "/messages") {
			return base
		}
		return base + "/messages"
	case capabilities.ProtocolGemini:
		action := "generateContent"
		if stream {
			action = "streamGenerateContent?alt=sse"
		}
		if strings.Contains(base, ":generateContent") || strings.Contains(base, ":streamGenerateContent") {
			return base
		}
		return base + "/models/" + url.PathEscape(model) + ":" + action
	case capabilities.ProtocolOpenAIResponse:
		if strings.HasSuffix(base, "/responses") {
			return base
		}
		return base + "/responses"
	default:
		if strings.HasSuffix(base, "/chat/completions") {
			return base
		}
		return base + "/chat/completions"
	}
}

func testProviderRequest(req ir.Request, cfg capabilities.Provider) (any, error) {
	switch cfg.Protocol {
	case capabilities.ProtocolOpenAIChat:
		return openaichat.ToProviderRequest(req, cfg)
	case capabilities.ProtocolAnthropic:
		return anthropic.ToProviderRequest(req)
	case capabilities.ProtocolGemini:
		return gemini.ToProviderRequest(req)
	case capabilities.ProtocolOpenAIResponse:
		return openairesponses.ToProviderRequest(req)
	default:
		return nil, fmt.Errorf("unsupported provider protocol %q", cfg.Protocol)
	}
}

func testProviderResponse(data []byte, cfg capabilities.Provider) (ir.Response, error) {
	switch cfg.Protocol {
	case capabilities.ProtocolOpenAIChat:
		return openaichat.ParseResponseWithCapabilities(data, cfg)
	case capabilities.ProtocolAnthropic:
		return anthropic.ParseResponse(data)
	case capabilities.ProtocolGemini:
		return gemini.ParseResponse(data)
	case capabilities.ProtocolOpenAIResponse:
		return openairesponses.ParseResponse(data)
	default:
		return ir.Response{}, fmt.Errorf("unsupported provider protocol %q", cfg.Protocol)
	}
}

func firstText(resp ir.Response) string {
	for _, choice := range resp.Choices {
		for _, block := range choice.Message.Content {
			if block.Type == ir.BlockText {
				return block.Text
			}
		}
	}
	return ""
}

func defaultDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "LocalRelay", "localrelay.db")
}
