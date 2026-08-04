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
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		s.handleAnthropic(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
		s.handleResponses(w, r)
	case r.Method == http.MethodPost:
		if model, stream, ok := geminiInboundModel(r); ok {
			s.handleGemini(w, r, model, stream)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
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

type inboundRequest struct {
	model  string
	stream bool
	toIR   func(capabilities.Provider) (ir.Request, error)
}

type inboundRequestParser func([]byte) (inboundRequest, error)
type clientResponseWriter func(ir.Response, capabilities.Provider) (any, error)
type clientStreamWriter func(io.Writer, capabilities.Provider) func(ir.StreamEvent) error
type nativeResponseWriter func(ir.Response) (any, error)
type nativeStreamWriter func(io.Writer) func(ir.StreamEvent) error

func (s *Server) handleAnthropic(w http.ResponseWriter, r *http.Request) {
	s.handleNativeRequest(w, r, "anthropic_messages", s.store.AppNameForAPIKey(r.Header.Get("X-Api-Key")), anthropic.ParseRequest, func(resp ir.Response) (any, error) {
		return anthropic.FromIRResponse(resp)
	}, func(w io.Writer) func(ir.StreamEvent) error {
		return func(event ir.StreamEvent) error { return anthropic.WriteStreamEvent(w, event) }
	})
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.handleNativeRequest(w, r, "openai_responses", s.store.AppNameForAuthorization(r.Header.Get("Authorization")), openairesponses.ParseRequest, func(resp ir.Response) (any, error) {
		return openairesponses.FromIRResponse(resp)
	}, func(w io.Writer) func(ir.StreamEvent) error {
		writer := openairesponses.NewStreamWriter(w)
		return writer.Write
	})
}

func (s *Server) handleGemini(w http.ResponseWriter, r *http.Request, model string, stream bool) {
	appName := s.store.AppNameForAPIKey(r.Header.Get("X-Goog-Api-Key"))
	if appName == store.NoAppName {
		appName = s.store.AppNameForAPIKey(r.URL.Query().Get("key"))
	}
	s.handleNativeRequest(w, r, "gemini", appName, func(data []byte) (ir.Request, error) {
		return gemini.ParseRequest(data, model, stream)
	}, func(resp ir.Response) (any, error) {
		return gemini.FromIRResponse(resp)
	}, func(w io.Writer) func(ir.StreamEvent) error {
		return func(event ir.StreamEvent) error { return gemini.WriteStreamEvent(w, event) }
	})
}

func (s *Server) handleNativeRequest(w http.ResponseWriter, r *http.Request, protocol string, appName string, parse func([]byte) (ir.Request, error), responseWriter nativeResponseWriter, streamWriter nativeStreamWriter) {
	s.handleClientRequest(w, r, protocol, appName, func(data []byte) (inboundRequest, error) {
		request, err := parse(data)
		if err != nil {
			return inboundRequest{}, err
		}
		return inboundRequest{
			model:  request.Model,
			stream: request.Stream,
			toIR: func(capabilities.Provider) (ir.Request, error) {
				return request, nil
			},
		}, nil
	}, func(response ir.Response, _ capabilities.Provider) (any, error) {
		return responseWriter(response)
	}, func(w io.Writer, _ capabilities.Provider) func(ir.StreamEvent) error {
		return streamWriter(w)
	})
}

func (s *Server) handleClientRequest(w http.ResponseWriter, r *http.Request, protocol string, appName string, parse inboundRequestParser, responseWriter clientResponseWriter, streamWriter clientStreamWriter) {
	start := time.Now().UTC()
	log := store.CallLog{Protocol: protocol, AppName: appName, StartedAt: start.Format(time.RFC3339)}
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
	incoming, err := parse(body)
	if err != nil {
		status = http.StatusBadRequest
		log.Error = err.Error()
		writeError(w, status, "bad_request", err.Error())
		return
	}
	log.Stream = incoming.stream
	routed, err := s.store.GetRoutedModel(incoming.model)
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
	// Call logs record the protocol used for the upstream provider, matching
	// the pre-existing OpenAI Chat entrypoint semantics.
	log.Protocol = providerCapabilities.Protocol
	irReq, err := incoming.toIR(providerCapabilities)
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
	upstreamResp, err := s.postProvider(r.Context(), routed.Provider, providerCapabilities, routed.Model.ID, irReq.Stream, upstreamBody)
	if err != nil {
		status = http.StatusBadGateway
		log.Error = err.Error()
		writeError(w, status, "upstream_error", err.Error())
		return
	}
	defer upstreamResp.Body.Close()
	if irReq.Stream && upstreamResp.StatusCode >= 200 && upstreamResp.StatusCode < 300 {
		if _, ok := w.(http.Flusher); !ok {
			status = http.StatusInternalServerError
			log.Error = "streaming is not supported by response writer"
			writeError(w, status, "streaming_unavailable", log.Error)
			return
		}
		if err := s.streamNativeProviderResponse(r.Context(), w, upstreamResp.Body, providerCapabilities, routed.Provider.ID+"/"+routed.Model.ID, irReq, &log, streamWriter); err != nil {
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
	if irResp.Usage == (ir.Usage{}) {
		irResp.Usage = estimateResponseUsage(irReq, irResp)
		log.TokenEstimated = true
	}
	log.InputTokens = irResp.Usage.InputTokens
	log.OutputTokens = irResp.Usage.OutputTokens
	log.CacheCreationInputTokens = irResp.Usage.CacheCreationInputTokens
	log.CacheReadInputTokens = irResp.Usage.CacheReadInputTokens
	clientResp, err := responseWriter(irResp, providerCapabilities)
	if err != nil {
		status = http.StatusBadGateway
		log.Error = err.Error()
		writeError(w, status, "response_conversion_error", err.Error())
		return
	}
	writeJSON(w, status, clientResp)
}

func (s *Server) streamNativeProviderResponse(ctx context.Context, w http.ResponseWriter, body io.Reader, cfg capabilities.Provider, model string, request ir.Request, log *store.CallLog, newWriter clientStreamWriter) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming is not supported by response writer")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	writer := newWriter(w, cfg)
	var output strings.Builder
	hasUsage := false
	hasStreamError := false
	err := forEachProviderStreamEvent(body, cfg, func(event ir.StreamEvent) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		event.Model = model
		if event.Usage != (ir.Usage{}) {
			hasUsage = true
			log.InputTokens = event.Usage.InputTokens
			log.OutputTokens = event.Usage.OutputTokens
			log.CacheCreationInputTokens = event.Usage.CacheCreationInputTokens
			log.CacheReadInputTokens = event.Usage.CacheReadInputTokens
		}
		if event.Type == ir.StreamContentBlockDelta {
			output.WriteString(event.Delta)
			output.WriteString(event.ArgumentsDelta)
		}
		if event.Type == ir.StreamError {
			hasStreamError = true
		}
		if err := writer(event); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err == nil && !hasUsage && !hasStreamError {
		usage := estimateStreamUsage(request, output.String())
		log.InputTokens = usage.InputTokens
		log.OutputTokens = usage.OutputTokens
		log.TokenEstimated = true
	}
	return err
}

func geminiInboundModel(r *http.Request) (string, bool, bool) {
	for _, prefix := range []string{"/v1beta/models/", "/v1/models/"} {
		path := r.URL.EscapedPath()
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		path = strings.TrimPrefix(path, prefix)
		stream := strings.HasSuffix(path, ":streamGenerateContent")
		if stream {
			path = strings.TrimSuffix(path, ":streamGenerateContent")
		} else if strings.HasSuffix(path, ":generateContent") {
			path = strings.TrimSuffix(path, ":generateContent")
		} else {
			continue
		}
		model, err := url.PathUnescape(path)
		if err != nil || strings.TrimSpace(model) == "" {
			return "", false, false
		}
		return model, stream, true
	}
	return "", false, false
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	s.handleClientRequest(w, r, "openai_chat", s.store.AppNameForAuthorization(r.Header.Get("Authorization")), parseOpenAIChatRequest, func(response ir.Response, cfg capabilities.Provider) (any, error) {
		return openaichat.FromIRResponseWithCapabilities(response, cfg)
	}, func(w io.Writer, cfg capabilities.Provider) func(ir.StreamEvent) error {
		return func(event ir.StreamEvent) error { return openaichat.WriteStreamEvent(w, event, cfg) }
	})
}

func parseOpenAIChatRequest(data []byte) (inboundRequest, error) {
	var request openaichat.Request
	if err := json.Unmarshal(data, &request); err != nil {
		return inboundRequest{}, err
	}
	return inboundRequest{model: request.Model, stream: request.Stream, toIR: request.ToIRWithCapabilities}, nil
}

func (s *Server) streamProviderResponse(ctx context.Context, w http.ResponseWriter, body io.Reader, cfg capabilities.Provider, model string, request ir.Request, log *store.CallLog) error {
	return s.streamNativeProviderResponse(ctx, w, body, cfg, model, request, log, func(w io.Writer, cfg capabilities.Provider) func(ir.StreamEvent) error {
		return func(event ir.StreamEvent) error { return openaichat.WriteStreamEvent(w, event, cfg) }
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
