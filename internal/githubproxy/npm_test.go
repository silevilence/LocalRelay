package githubproxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNPMRoutes(t *testing.T) {
	for _, tc := range []struct{ method, path, upstream string }{
		{"GET", "/npm", "/"}, {"GET", "/npm/", "/"},
		{"GET", "/npm/lodash", "/lodash"}, {"HEAD", "/npm/lodash/latest", "/lodash/latest"},
		{"GET", "/npm/pkg/1.0.0%2Bbuild", "/pkg/1.0.0%2Bbuild"},
		{"GET", "/npm/@types%2fnode", "/@types%2fnode"}, {"GET", "/npm/@types/node/22.0.0", "/@types/node/22.0.0"},
		{"GET", "/npm/lodash/-/lodash-4.17.21.tgz", "/lodash/-/lodash-4.17.21.tgz"},
		{"HEAD", "/npm/@types/node/-/node-22.0.0.tgz", "/@types/node/-/node-22.0.0.tgz"},
		{"GET", "/lodash/-/lodash-4.17.21.tgz", "/lodash/-/lodash-4.17.21.tgz"},
		{"GET", "/@types/node/-/node-22.0.0.tgz", "/@types/node/-/node-22.0.0.tgz"},
		{"GET", "/npm/-/npm-10.0.0.tgz", "/npm/-/npm-10.0.0.tgz"},
		{"GET", "/npm/-/ping?write=true", "/-/ping?write=true"},
		{"GET", "/npm/-/v1/search?text=hello%20world&size=5", "/-/v1/search?size=5&text=hello+world"},
		{"GET", "/npm/-/npm/v1/keys", "/-/npm/v1/keys"},
		{"POST", "/npm/-/npm/v1/security/advisories/bulk", "/-/npm/v1/security/advisories/bulk"},
		{"POST", "/npm/-/npm/v1/security/audits/quick", "/-/npm/v1/security/audits/quick"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			u, _, err := npmTarget(httptest.NewRequest(tc.method, tc.path, nil))
			if err != nil || u.String() != "https://registry.npmjs.org"+tc.upstream {
				t.Fatalf("%v %v", u, err)
			}
		})
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/lodash"}, {"GET", "/npmx/a"}, {"GET", "/npm//a"}, {"GET", "/npm/@scope"},
		{"GET", "/npm/@../a"}, {"GET", "/npm/@s/$bad"}, {"GET", "/npm/../x"},
		{"GET", "/npm/a/%2e%2e/x"}, {"GET", "/npm/a/%252e%252e"}, {"GET", "/npm/a%5cb"},
		{"GET", "/npm/a/-/x.zip"}, {"GET", "/npm/a/b/c"}, {"GET", "/npm/-/user/org.couchdb.user:test"},
		{"GET", "/npm/a?token=secret"}, {"GET", "/npm/a?x=%zz"}, {"GET", "/npm/-/ping?key=secret"},
		{"PUT", "/npm/a"}, {"DELETE", "/npm/a"}, {"POST", "/npm/a"}, {"POST", "/a/-/a.tgz"},
		{"POST", "/npm/-/ping"}, {"POST", "/npm/-/v1/search"}, {"GET", "/npm/-/npm/v1/security/advisories/bulk"},
	} {
		if _, _, err := npmTarget(httptest.NewRequest(tc.method, tc.path, nil)); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestNPMRedirects(t *testing.T) {
	for _, u := range []string{"https://registry.npmjs.org/a", "https://REGISTRY.NPMJS.ORG:443/@s/p/-/p-1.tgz"} {
		if err := npmRedirect(httptest.NewRequest("GET", u, nil), nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{"http://registry.npmjs.org/a", "https://registry.npmjs.org:444/a", "https://registry.npmjs.org.evil/a", "https://u:p@registry.npmjs.org/a", "https://127.0.0.1/a", "https://github.com/a", "https://registry.npmjs.org/-/user/x"} {
		if npmRedirect(httptest.NewRequest("GET", u, nil), nil) == nil {
			t.Fatal(u)
		}
	}
	if npmRedirect(httptest.NewRequest("GET", "https://registry.npmjs.org/a", nil), make([]*http.Request, 10)) == nil {
		t.Fatal("redirect limit")
	}
	if npmRedirect(httptest.NewRequest("POST", "https://registry.npmjs.org/a", nil), nil) == nil {
		t.Fatal("POST redirect escaped audit routes")
	}
	var calls int
	s := testServer(t, "direct")
	s.npm.clients[0].Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := response(302, "")
		resp.Header.Set("Location", "https://evil.test/a")
		return resp, nil
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/npm/a", nil))
	if w.Code != 502 || calls != 1 {
		t.Fatal(w.Code, calls)
	}
}

const packument = `{"name":"@s/pkg","unknown":9007199254740993,"versions":{"1.0.0":{"name":"@s/pkg","dist":{"tarball":"https://registry.npmjs.org/@s/pkg/-/pkg-1.0.0.tgz","integrity":"sha512-kept","signatures":[{"sig":"kept"}]}},"2.0.0":{"dist":{"tarball":"https://other.test/pkg.tgz"}}}}`

func TestNPMMetadataAndHeaders(t *testing.T) {
	for _, encoding := range []string{"", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			s := testServer(t, "direct")
			s.npm.clients[0].Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.EscapedPath() != "/@s%2fpkg" || r.Header.Get("Accept") != "application/vnd.npm.install-v1+json" || r.Header.Get("Accept-Encoding") != "identity" {
					t.Error(r.URL, r.Header)
				}
				for _, k := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Secret", "Proxy-Authorization", "If-None-Match", "If-Modified-Since", "Range", "If-Range", "User-Agent"} {
					if r.Header.Get(k) != "" {
						t.Error("leaked", k)
					}
				}
				resp := response(200, packument)
				if encoding == "gzip" {
					var b bytes.Buffer
					z := gzip.NewWriter(&b)
					z.Write([]byte(packument))
					z.Close()
					resp.Body = io.NopCloser(&b)
					resp.Header.Set("Content-Encoding", "gzip")
				}
				for _, k := range []string{"ETag", "Last-Modified", "Expires", "Accept-Ranges", "Content-Range", "Set-Cookie", "Content-Length"} {
					resp.Header.Set(k, "stale")
				}
				return resp, nil
			})
			r := httptest.NewRequest("GET", "http://192.168.1.50:8719/npm/@s%2fpkg", nil)
			for _, k := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Secret", "Proxy-Authorization", "If-None-Match", "If-Modified-Since", "Range", "If-Range", "User-Agent"} {
				r.Header.Set(k, "secret")
			}
			r.Header.Set("Connection", "User-Agent")
			r.Header.Set("Accept", "application/vnd.npm.install-v1+json")
			r.Header.Set("Accept-Encoding", "gzip, br")
			r.Header.Set("X-Forwarded-Host", "evil.test")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, k := range []string{"ETag", "Last-Modified", "Expires", "Accept-Ranges", "Content-Range", "Content-Encoding", "Set-Cookie"} {
				if w.Header().Get(k) != "" {
					t.Fatal(k)
				}
			}
			for _, want := range []string{"http://192.168.1.50:8719/npm/@s/pkg/-/pkg-1.0.0.tgz", "https://other.test/pkg.tgz", "9007199254740993", "sha512-kept", `"sig":"kept"`} {
				if !strings.Contains(w.Body.String(), want) {
					t.Fatal(want, w.Body.String())
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" || !json.Valid(w.Body.Bytes()) {
				t.Fatal(w.Header())
			}
		})
	}
}

func TestNPMAuditFallbackAndDownloads(t *testing.T) {
	s := testServer(t, "auto")
	var direct, proxy int
	s.npm.clients[0].Transport = roundTrip(func(*http.Request) (*http.Response, error) { direct++; return nil, errors.New("offline") })
	payload := string([]byte{0, 255, 1, 2})
	s.npm.clients[1].Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		proxy++
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			if string(b) != payload || r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Content-Type") != "application/json" {
				t.Error(r.Header, b)
			}
			return response(200, `{}`), nil
		}
		if r.Header.Get("Range") != "bytes=0-3" || r.Header.Get("If-Range") != "etag" {
			t.Error(r.Header)
		}
		resp := response(206, payload)
		resp.Header.Set("Content-Range", "bytes 0-3/20")
		resp.Header.Set("ETag", "etag")
		return resp, nil
	})
	for _, path := range []string{"/npm/-/npm/v1/security/advisories/bulk", "/npm/-/npm/v1/security/audits/quick"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Content-Encoding", "gzip")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	for _, path := range []string{"/npm/a/-/a-1.tgz", "/a/-/a-1.tgz", "/npm/-/npm-1.tgz"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Range", "bytes=0-3")
		r.Header.Set("If-Range", "etag")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 206 || w.Body.String() != payload || w.Header().Get("Content-Range") != "bytes 0-3/20" || w.Header().Get("ETag") != "etag" {
			t.Fatal(w)
		}
	}
	if direct != 1 || proxy != 5 {
		t.Fatal(direct, proxy)
	}
	if s.order("o/r")[0] != 0 {
		t.Fatal("npm failure polluted GitHub reachability")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/npm/-/npm/v1/security/advisories/bulk", bytes.NewReader(make([]byte, maxUploadBody+1))))
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
}

func TestNPMErrorsAndHEAD(t *testing.T) {
	for _, tc := range []struct {
		status         int
		body, encoding string
		want           int
	}{
		{200, "not json", "", 502}, {200, "bad gzip", "gzip", 502}, {200, "{}", "br", 502},
		{404, `{"error":"not found"}`, "", 404}, {429, `{"error":"rate limit"}`, "", 429}, {503, "down", "", 502},
	} {
		s := testServer(t, "direct")
		s.npm.clients[0].Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			r := response(tc.status, tc.body)
			r.Header.Set("Content-Encoding", tc.encoding)
			return r, nil
		})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/npm/pkg", nil))
		if w.Code != tc.want {
			t.Fatal(tc, w.Code)
		}
	}
	s := testServer(t, "direct")
	s.npm.clients[0].Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		r := response(200, "")
		r.Header.Set("Content-Length", "20")
		r.Header.Set("ETag", "original")
		return r, nil
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("HEAD", "/npm/pkg", nil))
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "" || w.Header().Get("ETag") != "" {
		t.Fatal(w)
	}
	s.npm.clients[0].Transport = roundTrip(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/npm/pkg", nil).WithContext(ctx))
	if w.Body.Len() != 0 {
		t.Fatal("wrote after cancellation")
	}
}

func TestNPMPackumentBoundaries(t *testing.T) {
	for _, data := range []string{"null", "[]", `{"dist":5}`, `{"versions":4}`, `{"versions":{"1":null}}`, `{"versions":{"1":4}}`, `{"versions":{"1":{"dist":4}}}`} {
		if _, err := rewriteNPMPackument([]byte(data), "http://local/npm"); err == nil {
			t.Fatal(data)
		}
	}
	for _, data := range []string{`{"dist":null}`, `{"dist":{}}`, `{"dist":{"tarball":5}}`, `{"dist":{"tarball":"https://registry.npmjs.org/a/-/a.tgz?secret=x"}}`, `{"dist":{"tarball":"https://registry.npmjs.org/a"}}`, `{"dist":{"tarball":"%zz"}}`} {
		got, err := rewriteNPMPackument([]byte(data), "http://local/npm")
		if err != nil || strings.Contains(string(got), "http://local") {
			t.Fatal(string(got), err)
		}
	}
	r := httptest.NewRequest("GET", "http://local/npm/a", nil)
	r.Host = "user@local"
	if rewriteNPMResponse(response(200, "{}"), r, strings.NewReader("{}")) == nil {
		t.Fatal("unsafe origin accepted")
	}
	r.Host = "local"
	if rewriteNPMResponse(response(200, "{}"), r, brokenReader{}) == nil {
		t.Fatal("ignored read error")
	}
	if rewriteNPMResponse(response(200, "{}"), r, io.LimitReader(zeroReader{}, maxNPMMetadata+1)) == nil {
		t.Fatal("unbounded metadata")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
