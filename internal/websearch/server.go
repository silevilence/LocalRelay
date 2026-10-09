package websearch

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"
)

//go:embed search.md
var documentation string

const searchTimeout = 60 * time.Second

type Server struct {
	load    func() (Config, error)
	clients []*http.Client
}

func New(load func() (Config, error), mode string, proxyURL *url.URL) *Server {
	return &Server{load: load, clients: newClients(mode, proxyURL)}
}

func (s *Server) Close() {
	for _, client := range s.clients {
		client.CloseIdleConnections()
	}
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *apiError) Error() string { return e.Message }

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error apiError `json:"error"`
	}{apiError{code, message, status}})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/docs/search" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, 405, "method_not_allowed", "请使用 GET /docs/search")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, documentation)
		}
		return
	}
	if r.URL.Path != "/search" {
		writeError(w, 404, "not_found", "接口不存在")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, 405, "method_not_allowed", "请使用 POST /search")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, 415, "unsupported_media_type", "Content-Type 必须为 application/json")
		return
	}
	// Read the bounded body first so an oversized valid JSON prefix cannot hide
	// trailing data. Clients cannot supply provider credentials or target URLs.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, 413, "request_too_large", "请求体不能超过 32 KiB")
		} else {
			writeError(w, 400, "invalid_request", "无法读取请求体")
		}
		return
	}
	input, err := decodeRequest(body)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	cfg, err := s.load()
	if err != nil || cfg.Validate() != nil {
		writeError(w, 503, "search_unavailable", "搜索配置不可用")
		return
	}
	if cfg.APIKey == "" {
		writeError(w, 503, "search_not_configured", "请在设置中配置搜索 API Key")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	response, err := findProvider(cfg.Provider).search(ctx, s.clients, cfg.APIKey, input)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		var upstream *apiError
		var timeout net.Error
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || (errors.As(err, &timeout) && timeout.Timeout()) {
			writeError(w, 504, "search_timeout", "搜索服务响应超时")
		} else if errors.As(err, &upstream) {
			writeError(w, upstream.Status, upstream.Code, upstream.Message)
		} else {
			writeError(w, 502, "search_upstream_error", "搜索服务请求失败")
		}
		return
	}
	response.Version, response.Provider, response.Query = "1", cfg.Provider, input.Query
	if response.Results == nil {
		response.Results = []Result{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(response)
}
