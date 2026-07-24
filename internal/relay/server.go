package relay

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
	"localrelay/internal/protocol/anthropic"
	"localrelay/internal/protocol/gemini"
	"localrelay/internal/protocol/openaichat"
	"localrelay/internal/protocol/openairesponses"
	"localrelay/internal/store"
)

const statusClientClosedRequest = 499

type Server struct {
	store   *store.Store
	client  *http.Client
	logs    chan store.CallLog
	logDone chan struct{}
	logMu   sync.RWMutex
	closed  bool
	once    sync.Once
}

func New(s *store.Store) *Server {
	server := &Server{
		store:   s,
		client:  &http.Client{Timeout: 2 * time.Minute},
		logs:    make(chan store.CallLog, 1024),
		logDone: make(chan struct{}),
	}
	go server.writeLogs()
	return server
}

func (s *Server) Close() {
	s.once.Do(func() {
		s.logMu.Lock()
		s.closed = true
		close(s.logs)
		s.logMu.Unlock()
		<-s.logDone
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		s.handleModels(w)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		s.handleChat(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func (s *Server) handleModels(w http.ResponseWriter) {
	models, err := s.store.ListEnabledModels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, m := range models {
		data = append(data, map[string]any{
			"id":       m.ProviderID + "/" + m.ID,
			"object":   "model",
			"owned_by": m.ProviderID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	log := store.CallLog{Protocol: "openai_chat", AppName: s.store.AppNameForAuthorization(r.Header.Get("Authorization")), StartedAt: start.Format(time.RFC3339)}
	status := http.StatusOK
	defer func() {
		log.EndedAt = time.Now().UTC().Format(time.RFC3339)
		log.StatusCode = status
		s.queueLog(log)
	}()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 20<<20))
	if err != nil {
		status = http.StatusRequestEntityTooLarge
		log.Error = err.Error()
		writeError(w, status, "request_too_large", err.Error())
		return
	}
	var clientReq openaichat.Request
	if err := json.Unmarshal(body, &clientReq); err != nil {
		status = http.StatusBadRequest
		log.Error = err.Error()
		writeError(w, status, "bad_request", err.Error())
		return
	}
	log.Stream = clientReq.Stream
	routed, err := s.store.GetRoutedModel(clientReq.Model)
	if err != nil {
		status = routeStatus(err)
		log.Error = err.Error()
		writeError(w, status, routeCode(err), err.Error())
		return
	}
	log.ProviderID, log.ModelID = routed.Provider.ID, routed.Model.ID
	providerCapabilities, err := capabilities.Parse(routed.Provider.CapabilityConfig)
	if err != nil {
		status = http.StatusBadRequest
		log.Error = err.Error()
		writeError(w, status, "bad_provider_capabilities", err.Error())
		return
	}
	log.Protocol = providerCapabilities.Protocol

	irReq, err := clientReq.ToIRWithCapabilities(providerCapabilities)
	if err != nil {
		status = http.StatusBadRequest
		log.Error = err.Error()
		writeError(w, status, "bad_request", err.Error())
		return
	}
	irReq.Model = routed.Model.ID

	providerReq, err := toProviderRequest(irReq, providerCapabilities)
	if err != nil {
		status = http.StatusBadRequest
		log.Error = err.Error()
		writeError(w, status, "unsupported_provider", err.Error())
		return
	}
	upstreamBody, err := json.Marshal(providerReq)
	if err != nil {
		status = http.StatusInternalServerError
		log.Error = err.Error()
		writeError(w, status, "marshal_error", err.Error())
		return
	}

	upstreamResp, err := s.postProvider(r.Context(), routed.Provider, providerCapabilities, routed.Model.ID, clientReq.Stream, upstreamBody)
	if err != nil {
		status = http.StatusBadGateway
		log.Error = err.Error()
		writeError(w, status, "upstream_error", err.Error())
		return
	}
	defer upstreamResp.Body.Close()
	if clientReq.Stream && upstreamResp.StatusCode >= 200 && upstreamResp.StatusCode < 300 {
		if _, ok := w.(http.Flusher); !ok {
			status = http.StatusInternalServerError
			log.Error = "streaming is not supported by response writer"
			writeError(w, status, "streaming_unavailable", log.Error)
			return
		}
		if err := s.streamProviderResponse(r.Context(), w, upstreamResp.Body, providerCapabilities, routed.Provider.ID+"/"+routed.Model.ID, &log); err != nil {
			log.Error = err.Error()
			if errors.Is(err, context.Canceled) {
				status = statusClientClosedRequest
			} else {
				status = http.StatusBadGateway
			}
		}
		return
	}
	respBody, err := io.ReadAll(upstreamResp.Body)
	if err != nil {
		status = http.StatusBadGateway
		log.Error = err.Error()
		writeError(w, status, "upstream_read_error", err.Error())
		return
	}
	if upstreamResp.StatusCode < 200 || upstreamResp.StatusCode >= 300 {
		status = upstreamResp.StatusCode
		log.Error = truncateError(respBody)
		w.Header().Set("Content-Type", contentType(upstreamResp.Header.Get("Content-Type")))
		w.WriteHeader(status)
		_, _ = w.Write(respBody)
		return
	}

	irResp, err := parseProviderResponse(respBody, providerCapabilities)
	if err != nil {
		status = http.StatusBadGateway
		log.Error = err.Error()
		writeError(w, status, "bad_upstream_response", err.Error())
		return
	}
	irResp.Model = routed.Provider.ID + "/" + routed.Model.ID
	log.InputTokens = irResp.Usage.InputTokens
	log.OutputTokens = irResp.Usage.OutputTokens
	log.CacheCreationInputTokens = irResp.Usage.CacheCreationInputTokens
	log.CacheReadInputTokens = irResp.Usage.CacheReadInputTokens

	clientResp, err := openaichat.FromIRResponseWithCapabilities(irResp, providerCapabilities)
	if err != nil {
		status = http.StatusBadGateway
		log.Error = err.Error()
		writeError(w, status, "response_conversion_error", err.Error())
		return
	}
	writeJSON(w, status, clientResp)
}

func (s *Server) streamProviderResponse(ctx context.Context, w http.ResponseWriter, body io.Reader, cfg capabilities.Provider, model string, log *store.CallLog) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming is not supported by response writer")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return forEachProviderStreamEvent(body, cfg, func(event ir.StreamEvent) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		event.Model = model
		if event.Usage != (ir.Usage{}) {
			log.InputTokens = event.Usage.InputTokens
			log.OutputTokens = event.Usage.OutputTokens
			log.CacheCreationInputTokens = event.Usage.CacheCreationInputTokens
			log.CacheReadInputTokens = event.Usage.CacheReadInputTokens
		}
		if err := openaichat.WriteStreamEvent(w, event, cfg); err != nil {
			return err
		}
		flusher.Flush()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		return nil
	})
}

func (s *Server) queueLog(log store.CallLog) {
	s.logMu.RLock()
	defer s.logMu.RUnlock()
	if s.closed {
		_ = s.store.CreateCallLog(log)
		return
	}
	select {
	case s.logs <- log:
	default:
		_ = s.store.CreateCallLog(log)
	}
}

func (s *Server) writeLogs() {
	defer close(s.logDone)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var batch []store.CallLog
	flush := func() {
		if len(batch) > 0 {
			_ = s.store.CreateCallLogs(batch)
		}
		batch = batch[:0]
	}
	for {
		select {
		case log, ok := <-s.logs:
			if !ok {
				flush()
				return
			}
			batch = append(batch, log)
			if len(batch) >= 16 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *Server) postProvider(ctx context.Context, provider store.Provider, cfg capabilities.Provider, model string, stream bool, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, providerURL(provider.BaseURL, cfg.Protocol, model, stream), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
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
	return s.client.Do(req)
}

func toProviderRequest(req ir.Request, cfg capabilities.Provider) (any, error) {
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

func parseProviderResponse(data []byte, cfg capabilities.Provider) (ir.Response, error) {
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

func forEachProviderStreamEvent(r io.Reader, cfg capabilities.Provider, yield func(ir.StreamEvent) error) error {
	switch cfg.Protocol {
	case capabilities.ProtocolOpenAIChat:
		return openaichat.ForEachStreamEvent(r, cfg, yield)
	case capabilities.ProtocolAnthropic:
		return anthropic.ForEachStreamEvent(r, yield)
	case capabilities.ProtocolGemini:
		return gemini.ForEachStreamEvent(r, yield)
	case capabilities.ProtocolOpenAIResponse:
		return openairesponses.ForEachStreamEvent(r, yield)
	default:
		return fmt.Errorf("unsupported provider protocol %q", cfg.Protocol)
	}
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

func chatURL(base string) string {
	return providerURL(base, capabilities.ProtocolOpenAIChat, "", false)
}

func routeStatus(err error) int {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case errors.Is(err, store.ErrInvalidModelID), errors.Is(err, store.ErrModelDisabled):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func routeCode(err error) string {
	switch {
	case errors.Is(err, store.ErrInvalidModelID):
		return "invalid_model_id"
	case errors.Is(err, sql.ErrNoRows):
		return "model_not_found"
	case errors.Is(err, store.ErrModelDisabled):
		return "model_disabled"
	default:
		return "store_error"
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"code":    code,
		},
	})
}

func contentType(value string) string {
	if value == "" {
		return "application/json"
	}
	return value
}

func truncateError(body []byte) string {
	const limit = 4096
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit])
}

var _ http.Handler = (*Server)(nil)
