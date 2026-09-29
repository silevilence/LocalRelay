package githubproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxUploadBody = 16 << 20

type routeChoice struct {
	index   int
	expires time.Time
}

type Server struct {
	config   Config
	clients  []*http.Client
	mu       sync.Mutex
	sticky   map[string]routeChoice
	failed   [2]time.Time
	resource resource
	npm      *Server
}

// Resource policies share the transport/retry/streaming implementation, while
// keeping destination rules and reachability caches separate for each upstream.
type resource struct {
	name     string
	target   func(*http.Request) (*url.URL, string, error)
	redirect func(*http.Request, []*http.Request) error
}

func New(config Config) (*Server, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	s := newServer(config, resource{"GitHub", target, checkRedirect})
	s.npm = newServer(config, resource{"npm", npmTarget, npmRedirect})
	return s, nil
}

func newServer(config Config, policy resource) *Server {
	s := &Server{config: config, resource: policy, sticky: make(map[string]routeChoice)}
	for i := 0; i < 2; i++ {
		if i == 1 && config.ProxyAddress == "" {
			break
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil // Direct means direct, regardless of environment proxy variables.
		transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		transport.TLSHandshakeTimeout = 5 * time.Second
		transport.ResponseHeaderTimeout = 8 * time.Second
		transport.DisableCompression = true // Preserve range offsets and archive/asset bytes.
		if i == 1 {
			proxyURL, _ := url.Parse(config.ProxyType + "://" + config.ProxyAddress)
			// net/http supports both HTTP CONNECT and SOCKS5 (remote DNS).
			transport.Proxy = http.ProxyURL(proxyURL)
		}
		s.clients = append(s.clients, &http.Client{Transport: transport, CheckRedirect: policy.redirect})
	}
	return s
}

func (s *Server) Close() {
	if s.npm != nil {
		s.npm.Close()
	}
	for _, c := range s.clients {
		c.CloseIdleConnections()
	}
}

// Exact hosts only: no arbitrary *.githubusercontent.com destinations.
func allowedURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "codeload.github.com", "raw.githubusercontent.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return !strings.Contains(strings.ToLower(u.Path), "git-receive-pack")
	}
	return false
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 || !allowedURL(req.URL) {
		return fmt.Errorf("GitHub 重定向目标不允许")
	}
	return nil
}

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Smart HTTP uses GET info/refs and POST git-upload-pack; see
// https://git-scm.com/docs/http-protocol. Only these read-only RPCs are exposed.
func target(r *http.Request) (*url.URL, string, error) {
	if !strings.HasPrefix(r.URL.Path, "/github/") {
		return nil, "", fmt.Errorf("请使用 /github/{owner}/{repo}")
	}
	path := strings.TrimPrefix(r.URL.Path, "/github/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || !repoPart.MatchString(parts[0]) || !repoPart.MatchString(parts[1]) {
		return nil, "", fmt.Errorf("无效仓库路径")
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, "\\%\x00\r\n") || strings.EqualFold(p, "git-receive-pack") {
			return nil, "", fmt.Errorf("不允许的路径")
		}
	}
	u := &url.URL{Scheme: "https", Host: "github.com", Path: "/" + path}
	tail := strings.Join(parts[2:], "/")
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		return nil, "", fmt.Errorf("仅支持只读请求")
	}
	if r.Method == http.MethodPost && tail != "git-upload-pack" {
		return nil, "", fmt.Errorf("仅允许 git-upload-pack POST")
	}
	switch {
	case tail == "" || tail == "git-upload-pack":
		if r.URL.RawQuery != "" {
			return nil, "", fmt.Errorf("不支持的查询参数")
		}
		if tail == "" {
			u.Path += "/info/refs"
			u.RawQuery = "service=git-upload-pack"
		}
	case tail == "info/refs":
		if r.URL.RawQuery != "service=git-upload-pack" {
			return nil, "", fmt.Errorf("仅支持 git-upload-pack")
		}
		u.RawQuery = r.URL.RawQuery
	case strings.HasPrefix(tail, "archive/") && len(parts) >= 4:
	case strings.HasPrefix(tail, "releases/download/") && len(parts) >= 6:
	case strings.HasPrefix(tail, "releases/latest/download/") && len(parts) >= 6:
	case strings.HasPrefix(tail, "raw/") && len(parts) >= 5:
		u.Host = "raw.githubusercontent.com"
		u.Path = "/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git") + "/" + strings.Join(parts[3:], "/")
	default:
		return nil, "", fmt.Errorf("仅支持 clone、archive、release 资产与 raw 文件")
	}
	// Download query strings are intentionally not forwarded: browser tokens and
	// client API keys must never become upstream credentials. Signed redirect URLs
	// originate at GitHub and are retained by the HTTP client.
	return u, strings.ToLower(parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")), nil
}

func (s *Server) order(repo string) []int {
	if s.config.Mode == "direct" || len(s.clients) == 1 {
		return []int{0}
	}
	if s.config.Mode == "proxy" {
		return []int{1}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	first := 0
	if choice, ok := s.sticky[repo]; ok && now.Before(choice.expires) {
		first = choice.index
	}
	if now.Before(s.failed[first]) && !now.Before(s.failed[1-first]) {
		first = 1 - first
	}
	return []int{first, 1 - first}
}

// Real requests serve as reachability probes. Cache successes per repository for
// 5 minutes and failures for 30 seconds; retry only before any downstream bytes.
func (s *Server) remember(repo string, index int, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if !success {
		s.failed[index] = now.Add(30 * time.Second)
		return
	}
	s.failed[index] = time.Time{}
	if len(s.sticky) >= 1024 {
		for key, choice := range s.sticky {
			if now.After(choice.expires) {
				delete(s.sticky, key)
			}
		}
		if len(s.sticky) >= 1024 {
			clear(s.sticky)
		}
	}
	s.sticky[repo] = routeChoice{index, now.Add(5 * time.Minute)}
}

var requestHeaders = []string{"Accept", "Accept-Encoding", "Content-Type", "Content-Encoding", "Git-Protocol", "User-Agent", "Range", "If-Range", "If-None-Match", "If-Modified-Since"}
var responseHeaders = []string{"Content-Type", "Content-Length", "Content-Encoding", "Content-Disposition", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control", "Expires", "Vary"}

func copyHeaders(dst, src http.Header, names []string) {
	hop := make(map[string]bool)
	for _, line := range src.Values("Connection") {
		for _, name := range strings.Split(line, ",") {
			hop[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	for _, name := range names {
		if hop[strings.ToLower(name)] {
			continue
		}
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.npm != nil && (r.URL.Path == "/npm" || strings.HasPrefix(r.URL.Path, "/npm/") || npmTarballAlias(r)) {
		s.npm.ServeHTTP(w, r)
		return
	}
	u, repo, err := s.resource.target(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadBody))
		if err != nil {
			http.Error(w, "代理请求体过大或读取失败", http.StatusRequestEntityTooLarge)
			return
		}
	}
	order := s.order(repo)
	for attempt, index := range order {
		ctx, cancel := context.WithCancel(r.Context())
		req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), bytes.NewReader(body))
		if err != nil {
			cancel()
			http.Error(w, "无效代理请求", http.StatusBadRequest)
			return
		}
		copyHeaders(req.Header, r.Header, requestHeaders)
		metadata := s.resource.name == "npm" && npmMetadataPath(u.Path)
		if metadata {
			// Metadata is rewritten for this local origin. Upstream validators and
			// byte ranges describe a different representation.
			for _, name := range []string{"If-None-Match", "If-Modified-Since", "Range", "If-Range", "Accept-Encoding"} {
				req.Header.Del(name)
			}
			req.Header.Set("Accept-Encoding", "identity")
		}
		headerTimer := time.AfterFunc(12*time.Second, cancel) // Also bounds proxy CONNECT and redirect chains.
		resp, err := s.clients[index].Do(req)
		headerTimer.Stop()
		if err != nil || resp.StatusCode >= 500 {
			if resp != nil {
				resp.Body.Close()
			}
			cancel()
			if r.Context().Err() != nil {
				return
			}
			s.remember(repo, index, false)
			if attempt+1 < len(order) {
				continue
			}
			http.Error(w, s.resource.name+" 上游不可达，请检查出站代理与链路设置", http.StatusBadGateway)
			return
		}
		timer := time.AfterFunc(2*time.Minute, cancel)
		if metadata && resp.StatusCode == http.StatusOK {
			if err := rewriteNPMResponse(resp, r, &idleReader{resp.Body, timer}); err != nil {
				timer.Stop()
				resp.Body.Close()
				cancel()
				http.Error(w, "npm 元数据处理失败", http.StatusBadGateway)
				return
			}
		}
		s.remember(repo, index, true)
		copyHeaders(w.Header(), resp.Header, responseHeaders)
		w.WriteHeader(resp.StatusCode)
		// Large packs/assets are streamed without a total request deadline. A read
		// idle timeout bounds stalled transfers without truncating healthy ones.
		_, copyErr := io.Copy(w, &idleReader{resp.Body, timer})
		timer.Stop()
		resp.Body.Close()
		cancel()
		if copyErr != nil {
			if r.Context().Err() == nil {
				s.remember(repo, index, false)
			}
			// The local service is plain HTTP/1.x. Close the connection without a
			// terminal chunk so clients detect a truncated download (even when the
			// upstream did not send Content-Length). Do not append an error body.
			conn, _, abortErr := http.NewResponseController(w).Hijack()
			if abortErr == nil {
				abortErr = conn.Close()
			}
			if abortErr != nil {
				log.Printf("abort %s transfer after %v: %v", s.resource.name, copyErr, abortErr)
			}
		}
		return
	}
}

type idleReader struct {
	io.Reader
	timer *time.Timer
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.timer.Reset(2 * time.Minute)
	}
	return n, err
}
