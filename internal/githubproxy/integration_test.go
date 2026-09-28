package githubproxy

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in real-network acceptance: LOCALRELAY_GITHUB_E2E=1 go test
// ./internal/githubproxy -run TestGitHubIntegration -v -timeout 10m.
// Tests use temporary clones and local CONNECT/SOCKS5 proxies, never user Git config.
func TestGitHubIntegration(t *testing.T) {
	if os.Getenv("LOCALRELAY_GITHUB_E2E") != "1" {
		t.Skip("opt-in GitHub network acceptance")
	}
	var tunnels atomic.Int32
	httpProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 405)
			return
		}
		up, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			return
		}
		tunnels.Add(1)
		fmt.Fprint(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		bridge(conn, up)
	}))
	defer httpProxy.Close()
	socks, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socks.Close()
	go func() {
		for {
			conn, err := socks.Accept()
			if err != nil {
				return
			}
			go serveSOCKS(conn, &tunnels)
		}
	}()
	var expectedCommit string
	var expectedHash [32]byte
	for _, mode := range []string{"direct", "http", "socks5", "auto-fallback"} {
		t.Run(mode, func(t *testing.T) {
			c := DefaultConfig()
			c.Mode = "direct"
			if mode != "direct" {
				c.Mode = "proxy"
				c.ProxyAddress = strings.TrimPrefix(httpProxy.URL, "http://")
			}
			if mode == "socks5" {
				c.ProxyType = "socks5"
				c.ProxyAddress = socks.Addr().String()
			}
			if mode == "auto-fallback" {
				c.Mode = "auto"
			}
			s, err := New(c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var directCalls atomic.Int32
			if mode == "auto-fallback" {
				s.clients[0].Transport = roundTrip(func(*http.Request) (*http.Response, error) {
					directCalls.Add(1)
					return nil, errors.New("simulated direct outage")
				})
			}
			gateway := httptest.NewServer(s)
			defer gateway.Close()
			dir := filepath.Join(t.TempDir(), "git")
			start := time.Now()
			// Git's own source tree provides a substantial pack and many files.
			runGit(t, "-c", "http.proxy=", "clone", "--depth=1", gateway.URL+"/github/git/git.git", dir)
			runGit(t, "-C", dir, "fsck", "--full")
			if mode == "auto-fallback" && directCalls.Load() != 1 {
				t.Fatalf("repository stickiness not reused: %d", directCalls.Load())
			}
			commit := strings.TrimSpace(runGit(t, "-C", dir, "rev-parse", "HEAD"))
			if expectedCommit == "" {
				expectedCommit = commit
			}
			if commit != expectedCommit {
				t.Fatalf("clone revision mismatch %s != %s", commit, expectedCommit)
			}
			if _, err := os.Stat(filepath.Join(dir, "builtin", "clone.c")); err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Timeout: 2 * time.Minute}
			asset := gateway.URL + "/github/mvdan/sh/releases/download/v3.12.0/shfmt_v3.12.0_windows_amd64.exe"
			resp, err := client.Get(asset)
			if err != nil {
				t.Fatal(err)
			}
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil || resp.StatusCode != 200 || len(data) < 1000000 {
				t.Fatalf("asset %d bytes status %d: %v", len(data), resp.StatusCode, readErr)
			}
			hash := sha256.Sum256(data)
			if expectedHash == [32]byte{} {
				expectedHash = hash
			}
			if hash != expectedHash {
				t.Fatal("release bytes changed across routes")
			}
			for _, path := range []string{"/github/octocat/Hello-World/raw/master/README", "/github/octocat/Hello-World/archive/refs/heads/master.zip"} {
				resp, err := client.Get(gateway.URL + path)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || resp.StatusCode != 200 || len(body) == 0 {
					t.Fatalf("download %s: %d %v", path, resp.StatusCode, err)
				}
			}
			t.Logf("commit=%s asset sha256=%x bytes=%d elapsed=%s", commit, hash, len(data), time.Since(start).Round(time.Millisecond))
		})
	}
	if tunnels.Load() == 0 {
		t.Fatal("proxy never used")
	}
}

func runGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func bridge(client, up net.Conn) {
	defer client.Close()
	defer up.Close()
	go func() { io.Copy(up, client); up.Close() }()
	io.Copy(client, up)
}

func serveSOCKS(conn net.Conn, count *atomic.Int32) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	version, err := r.ReadByte()
	if err != nil || version != 5 {
		return
	}
	n, err := r.ReadByte()
	if err != nil {
		return
	}
	if _, err = io.CopyN(io.Discard, r, int64(n)); err != nil {
		return
	}
	conn.Write([]byte{5, 0})
	header := make([]byte, 4)
	if _, err = io.ReadFull(r, header); err != nil || header[1] != 1 {
		return
	}
	var host string
	switch header[3] {
	case 1:
		b := make([]byte, 4)
		if _, err = io.ReadFull(r, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		n, err := r.ReadByte()
		if err != nil {
			return
		}
		b := make([]byte, n)
		if _, err = io.ReadFull(r, b); err != nil {
			return
		}
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err = io.ReadFull(r, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		return
	}
	port := make([]byte, 2)
	if _, err = io.ReadFull(r, port); err != nil {
		return
	}
	up, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(port))), 5*time.Second)
	if err != nil {
		return
	}
	count.Add(1)
	conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	conn.SetDeadline(time.Time{})
	bridge(conn, up)
}
