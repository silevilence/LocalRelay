package githubproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func testServer(t *testing.T, mode string, trips ...roundTrip) *Server {
	t.Helper()
	c := DefaultConfig()
	c.Mode = mode
	c.ProxyAddress = "127.0.0.1:7890"
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	for i, f := range trips {
		s.clients[i].Transport = f
	}
	return s
}

func TestConfig(t *testing.T) {
	for _, mode := range []string{"auto", "direct", "proxy"} {
		for _, kind := range []string{"http", "socks5"} {
			for _, address := range []string{"localhost:7890", "[::1]:1080"} {
				c := Config{8719, mode, kind, address}
				s, err := New(c)
				if err != nil {
					t.Fatal(c, err)
				}
				s.Close()
			}
		}
	}
	c := DefaultConfig()
	s, err := New(c)
	if err != nil || len(s.clients) != 1 {
		t.Fatal(err)
	}
	s.Close()
	for _, change := range []func(*Config){
		func(c *Config) { c.Port = 0 }, func(c *Config) { c.Port = 65536 }, func(c *Config) { c.Mode = "bad" }, func(c *Config) { c.ProxyType = "ftp" }, func(c *Config) { c.Mode = "proxy" },
	} {
		c := DefaultConfig()
		change(&c)
		if _, err := New(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	for _, addr := range []string{"127.0.0.1", "host:0", "host:65536", "host:no", "http://host:80", "user:pass@host:80", "host:80/path", "host:80?key=x", "host:80#x", "%:80", ":80"} {
		c := DefaultConfig()
		c.ProxyAddress = addr
		if c.Validate() == nil {
			t.Fatalf("accepted %s", addr)
		}
	}
}

func TestRoutes(t *testing.T) {
	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/github/owner/repo.git", "https://github.com/owner/repo.git/info/refs?service=git-upload-pack"},
		{"GET", "/github/owner/repo.git/info/refs?service=git-upload-pack", "https://github.com/owner/repo.git/info/refs?service=git-upload-pack"},
		{"POST", "/github/owner/repo.git/git-upload-pack", "https://github.com/owner/repo.git/git-upload-pack"},
		{"HEAD", "/github/owner/repo/archive/refs/heads/main.zip?token=secret", "https://github.com/owner/repo/archive/refs/heads/main.zip"},
		{"GET", "/github/owner/repo/releases/download/v1/a.zip", "https://github.com/owner/repo/releases/download/v1/a.zip"},
		{"GET", "/github/owner/repo/releases/latest/download/a.zip", "https://github.com/owner/repo/releases/latest/download/a.zip"},
		{"GET", "/github/owner/repo/raw/main/a%20b.txt?key=secret", "https://raw.githubusercontent.com/owner/repo/main/a%20b.txt"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			u, repo, err := target(httptest.NewRequest(tc.method, tc.path, nil))
			if err != nil || u.String() != tc.want || repo != "owner/repo" {
				t.Fatalf("%v %q %v", u, repo, err)
			}
		})
	}
	for _, path := range []string{"/", "/github/x", "/github/x/..", "/github//repo", "/github/x/r/../raw/a/b", "/github/x/r/raw/a/%252e%252e", "/github/x/r/raw/a/%5cb", "/github/x/r/git-receive-pack", "/github/x/r/info/refs?service=git-receive-pack", "/github/x/r/info/refs?service=git-upload-pack&token=s", "/github/x/r?token=s", "/github/x/r/issues", "/github/x/r/releases/download/v1"} {
		if _, _, err := target(httptest.NewRequest("GET", path, nil)); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	for _, method := range []string{"PUT", "DELETE", "CONNECT", "POST"} {
		if _, _, err := target(httptest.NewRequest(method, "/github/o/r/raw/main/x", nil)); err == nil {
			t.Fatal(method)
		}
	}
}

func TestRedirectSafety(t *testing.T) {
	for _, host := range []string{"github.com", "codeload.github.com", "raw.githubusercontent.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "GITHUB.COM:443"} {
		u, _ := url.Parse("https://" + host + "/a")
		if !allowedURL(u) {
			t.Fatal(host)
		}
	}
	for _, value := range []string{"http://github.com/a", "https://github.com:8443/a", "https://github.com.evil.test/a", "https://127.0.0.1/a", "https://u:p@github.com/a", "https://github.com/a/git-receive-pack"} {
		r := httptest.NewRequest("GET", value, nil)
		if checkRedirect(r, nil) == nil {
			t.Fatal(value)
		}
	}
	if checkRedirect(httptest.NewRequest("GET", "https://github.com/a", nil), make([]*http.Request, 10)) == nil {
		t.Fatal("redirect limit")
	}
	var calls int
	s := testServer(t, "direct", func(r *http.Request) (*http.Response, error) {
		calls++
		resp := response(302, "")
		resp.Header.Set("Location", "https://evil.test/private")
		return resp, nil
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/github/o/r/archive/main.zip", nil))
	if w.Code != 502 || calls != 1 {
		t.Fatal(w.Code, calls)
	}
}

func TestForwardingAndFallback(t *testing.T) {
	var direct, proxy int
	payload := string([]byte{0, 1, 2, 255, 0, 42})
	s := testServer(t, "auto", func(r *http.Request) (*http.Response, error) { direct++; return nil, errors.New("unreachable") }, func(r *http.Request) (*http.Response, error) {
		proxy++
		for _, key := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Goog-Api-Key", "Proxy-Authorization", "X-Secret", "If-Range"} {
			if r.Header.Get(key) != "" {
				t.Errorf("leaked %s", key)
			}
		}
		if r.Header.Get("Git-Protocol") != "version=2" || r.Header.Get("Range") != "bytes=0-5" {
			t.Error(r.Header)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "request" {
			t.Error(string(b))
		}
		resp := response(206, payload)
		resp.Header.Set("Content-Range", "bytes 0-5/6")
		resp.Header.Set("Set-Cookie", "secret")
		return resp, nil
	})
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/github/o/r.git/git-upload-pack", strings.NewReader("request"))
		for _, key := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Goog-Api-Key", "Proxy-Authorization", "X-Secret", "If-Range"} {
			r.Header.Set(key, "secret")
		}
		r.Header.Set("Connection", "If-Range")
		r.Header.Set("Git-Protocol", "version=2")
		r.Header.Set("Range", "bytes=0-5")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 206 || w.Body.String() != payload || w.Header().Get("Content-Range") == "" || w.Header().Get("Set-Cookie") != "" {
			t.Fatal(w)
		}
	}
	if direct != 1 || proxy != 2 {
		t.Fatal(direct, proxy)
	}
	if s.order("o/r")[0] != 1 || s.order("different/repo")[0] != 1 {
		t.Fatal("cache not used")
	}
}

func TestModesAndFailureRecovery(t *testing.T) {
	for _, mode := range []string{"direct", "proxy", "auto"} {
		var calls [2]int
		s := testServer(t, mode, func(r *http.Request) (*http.Response, error) { calls[0]++; return response(503, "down"), nil }, func(r *http.Request) (*http.Response, error) { calls[1]++; return response(404, "missing"), nil })
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/github/o/r", nil))
		if mode == "direct" {
			if w.Code != 502 || calls[1] != 0 {
				t.Fatal(mode, w.Code, calls)
			}
		} else if w.Code != 404 {
			t.Fatal(mode, w.Code)
		}
		if mode == "proxy" && calls[0] != 0 {
			t.Fatal("direct in proxy mode")
		}
	}
	s := testServer(t, "auto", func(r *http.Request) (*http.Response, error) { return response(200, "ok"), nil }, func(r *http.Request) (*http.Response, error) { return nil, errors.New("down") })
	s.remember("o/r", 1, true)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/github/o/r", nil))
	if w.Code != 200 || s.order("o/r")[0] != 0 {
		t.Fatal("fallback to direct")
	}
	s.sticky["expired"] = routeChoice{1, time.Now().Add(-time.Second)}
	s.failed = [2]time.Time{}
	if s.order("expired")[0] != 0 {
		t.Fatal("expired sticky")
	}
	for i := 0; i < 1024; i++ {
		s.sticky[string(rune(i))] = routeChoice{0, time.Now().Add(-time.Second)}
	}
	s.remember("new", 1, true)
	if len(s.sticky) > 10 {
		t.Fatal("expired cache retained")
	}
	for i := 0; i < 1024; i++ {
		s.sticky[string(rune(i))] = routeChoice{0, time.Now().Add(time.Minute)}
	}
	s.remember("bounded", 1, true)
	if len(s.sticky) > 1024 {
		t.Fatal("unbounded cache")
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("broken") }
func (brokenReader) Close() error             { return nil }

func TestBadRequestsAndPartialTransfer(t *testing.T) {
	s := testServer(t, "direct", func(r *http.Request) (*http.Response, error) {
		resp := response(200, "")
		resp.Body = brokenReader{}
		return resp, nil
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("DELETE", "/github/o/r", nil))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/github/o/r/git-upload-pack", bytes.NewReader(make([]byte, maxUploadBody+1))))
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
	gateway := httptest.NewServer(s)
	defer gateway.Close()
	resp, err := http.Get(gateway.URL + "/github/o/r")
	if err == nil {
		defer resp.Body.Close()
		if _, err := io.ReadAll(resp.Body); err == nil {
			t.Fatal("truncated response was not aborted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.clients[0].Transport = roundTrip(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/github/o/r", nil).WithContext(ctx))
	if w.Body.Len() != 0 {
		t.Fatal("wrote after cancellation")
	}
}

func TestLocalHTTPStreaming(t *testing.T) {
	data := bytes.Repeat([]byte{0, 255, 42, 13, 10}, 100000)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		for i := 0; i < len(data); i += 5000 {
			w.Write(data[i : i+5000])
			w.(http.Flusher).Flush()
		}
	}))
	defer upstream.Close()
	s := testServer(t, "direct")
	base, _ := url.Parse(upstream.URL)
	s.clients[0].Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = base.Scheme
		r.URL.Host = base.Host
		return upstream.Client().Transport.RoundTrip(r)
	})
	gateway := httptest.NewServer(s)
	defer gateway.Close()
	s.npm.clients[0].Transport = s.clients[0].Transport
	for _, path := range []string{"/github/o/r/archive/main.zip", "/npm/@scope/pkg/-/pkg-1.0.0.tgz"} {
		resp, err := http.Get(gateway.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || !bytes.Equal(body, data) {
			t.Fatal(path, "stream corrupted", err, len(body))
		}
	}
}
