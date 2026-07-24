package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
	"localrelay/internal/protocol/anthropic"
	"localrelay/internal/protocol/gemini"
	"localrelay/internal/protocol/openaichat"
	"localrelay/internal/protocol/openairesponses"
	"localrelay/internal/relay"
	"localrelay/internal/store"
)

// App struct
type App struct {
	ctx         context.Context
	store       *store.Store
	relay       *relay.Server
	relayServer *http.Server
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
	a.relayServer = &http.Server{Addr: "127.0.0.1:8718", Handler: a.relay}
	go func() {
		if err := a.relayServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
	if a.relayServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = a.relayServer.Shutdown(shutdownCtx)
	}
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
	return "http://127.0.0.1:8718"
}

func providerChatURL(base string) string {
	return providerURL(base, capabilities.ProtocolOpenAIChat, "", false)
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
