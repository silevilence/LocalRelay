package githubproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Real CLI + official registry, with isolated npm configs/caches and no install
// scripts. LOCALRELAY_NPM_E2E=1 go test ./internal/githubproxy -run TestNPMIntegration -v.
func TestNPMIntegration(t *testing.T) {
	if os.Getenv("LOCALRELAY_NPM_E2E") != "1" {
		t.Skip("opt-in npm network acceptance")
	}
	httpProxy, socks, tunnels := integrationProxies(t)
	for _, mode := range []string{"direct", "http", "socks5", "auto-fallback"} {
		t.Run(mode, func(t *testing.T) {
			config := DefaultConfig()
			config.Mode = "direct"
			if mode != "direct" {
				config.Mode = "proxy"
				config.ProxyAddress = strings.TrimPrefix(httpProxy.URL, "http://")
			}
			if mode == "socks5" {
				config.ProxyType = "socks5"
				config.ProxyAddress = socks.Addr().String()
			}
			if mode == "auto-fallback" {
				config.Mode = "auto"
			}
			s, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var directCalls atomic.Int32
			if mode == "auto-fallback" {
				s.npm.clients[0].Transport = roundTrip(func(*http.Request) (*http.Response, error) {
					directCalls.Add(1)
					return nil, errors.New("simulated direct outage")
				})
			}
			var mu sync.Mutex
			paths := map[string]int{}
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths[r.URL.Path]++
				mu.Unlock()
				s.ServeHTTP(w, r)
			}))
			defer gateway.Close()
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, "package.json"), []byte(`{"name":"relay-e2e","version":"1.0.0","private":true}`))
			for _, name := range []string{"user.npmrc", "global.npmrc"} {
				writeTestFile(t, filepath.Join(dir, name), nil)
			}
			registry := gateway.URL + "/npm/"
			runNPM(t, dir, registry, "cache-install", "install", "is-number@7.0.0", "@types/estree@1.0.6", "--no-audit")
			for _, pkg := range []string{"is-number", "@types/estree"} {
				if _, err := os.Stat(filepath.Join(dir, "node_modules", pkg, "package.json")); err != nil {
					t.Fatal(err)
				}
			}
			runNPM(t, dir, registry, "cache-install", "ping")
			runNPM(t, dir, registry, "cache-install", "audit", "--json")
			// Simulate a lockfile written before using LocalRelay. Keep npm's SRI
			// intact: successful ci verifies the downloaded tarballs' integrity.
			lockPath := filepath.Join(dir, "package-lock.json")
			data, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), registry) {
				t.Fatal("metadata rewrite did not reach lockfile")
			}
			data = []byte(strings.ReplaceAll(string(data), registry, "https://registry.npmjs.org/"))
			writeTestFile(t, lockPath, data)
			mu.Lock()
			beforeCI := make(map[string]int, len(paths))
			for path, count := range paths {
				beforeCI[path] = count
			}
			mu.Unlock()
			runNPM(t, dir, registry, "cache-ci", "ci", "--no-audit")
			mu.Lock()
			for _, path := range []string{"/npm/is-number", "/npm/@types/estree", "/npm/is-number/-/is-number-7.0.0.tgz", "/npm/@types/estree/-/estree-1.0.6.tgz", "/npm/-/ping", "/npm/-/npm/v1/security/advisories/bulk"} {
				if paths[path] == 0 {
					t.Errorf("request bypassed local proxy: %s; paths=%v", path, paths)
				}
			}
			// npm versions differ on retaining the registry prefix during ci.
			// Either route must cause a new local download from the empty cache.
			for _, path := range []string{"/is-number/-/is-number-7.0.0.tgz", "/@types/estree/-/estree-1.0.6.tgz"} {
				if paths[path]+paths["/npm"+path] <= beforeCI[path]+beforeCI["/npm"+path] {
					t.Errorf("cold-cache ci bypassed local proxy: %s", path)
				}
			}
			mu.Unlock()
			if mode == "auto-fallback" && directCalls.Load() != 1 {
				t.Fatal("failure cache not used", directCalls.Load())
			}
			t.Log("ordinary/scoped install, audit, ping and cold-cache official-lockfile ci passed")
		})
	}
	if tunnels.Load() == 0 {
		t.Fatal("proxy never used")
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func runNPM(t *testing.T, dir, registry, cache string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	args = append(args, "--registry="+registry, "--cache="+filepath.Join(dir, cache), "--userconfig="+filepath.Join(dir, "user.npmrc"), "--globalconfig="+filepath.Join(dir, "global.npmrc"), "--ignore-scripts", "--no-fund", "--fetch-retries=0", "--fetch-timeout=20000")
	command := "npm"
	if runtime.GOOS == "windows" {
		command = "cmd"
		args = append([]string{"/d", "/c", "npm.cmd"}, args...)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	for _, value := range os.Environ() {
		key := strings.ToLower(strings.SplitN(value, "=", 2)[0])
		if strings.HasPrefix(key, "npm_config_") || key == "http_proxy" || key == "https_proxy" || key == "all_proxy" || key == "node_options" {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm %v: %v\n%s", args, err, output)
	}
	// An audit command must return an actual report, not just a successful exit.
	if strings.Contains(strings.Join(args, " "), "audit --json") {
		var report struct {
			AuditReportVersion int `json:"auditReportVersion"`
		}
		if json.Unmarshal(output, &report) != nil || report.AuditReportVersion == 0 {
			t.Fatalf("invalid audit report: %s", output)
		}
	}
}
