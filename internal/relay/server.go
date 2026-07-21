package relay

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"localrelay/internal/capabilities"
	"localrelay/internal/protocol/openaichat"
	"localrelay/internal/store"
)

type Server struct {
	store   *store.Store
	client  *http.Client
	logs    chan store.CallLog
	logDone chan struct{}
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
		close(s.logs)
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
	log := store.CallLog{Protocol: "openai_chat", StartedAt: start.Format(time.RFC3339)}
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
	if clientReq.Stream {
		status = http.StatusBadRequest
		log.Error = "stream is not supported yet"
		writeError(w, status, "unsupported_stream", log.Error)
		return
	}

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

	irReq, err := clientReq.ToIRWithCapabilities(providerCapabilities)
	if err != nil {
		status = http.StatusBadRequest
		log.Error = err.Error()
		writeError(w, status, "bad_request", err.Error())
		return
	}
	irReq.Model = routed.Model.ID

	providerReq, err := openaichat.ToProviderRequest(irReq, providerCapabilities)
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

	upstreamResp, err := s.postProvider(r.Context(), routed.Provider, upstreamBody)
	if err != nil {
		status = http.StatusBadGateway
		log.Error = err.Error()
		writeError(w, status, "upstream_error", err.Error())
		return
	}
	defer upstreamResp.Body.Close()
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

	irResp, err := openaichat.ParseResponseWithCapabilities(respBody, providerCapabilities)
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

func (s *Server) queueLog(log store.CallLog) {
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

func (s *Server) postProvider(ctx context.Context, provider store.Provider, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, chatURL(provider.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}
	return s.client.Do(req)
}

func chatURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
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
