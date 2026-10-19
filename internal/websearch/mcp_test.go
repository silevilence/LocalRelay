package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func rpc(t *testing.T, s http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "http://127.0.0.1/web", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2025-11-25")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestMCPProtocolAndToolErrors(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected upstream"); return nil, nil })
	s.load = func() (Config, error) { return DefaultConfig(), nil }
	init := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if init.Code != 200 || !strings.Contains(init.Body.String(), "LocalRelay Web") {
		t.Fatal(init.Code, init.Body.String())
	}
	list := rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"name":"search"`) || !strings.Contains(list.Body.String(), `"name":"fetch"`) {
		t.Fatal(list.Body.String())
	}
	for _, tc := range []struct{ name, args, code string }{
		{"search", `{"query":"Go"}`, "search_not_configured"},
		{"fetch", `{"url":"https://go.dev"}`, "fetch_not_configured"},
		{"search", `{"query":"Go","max_results":null}`, "invalid_request"},
		{"fetch", `{"url":"file:///secret"}`, "invalid_request"},
		{"search", `{"query":"Go","apiKey":"secret"}`, "invalid_request"},
	} {
		w := rpc(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"`+tc.name+`","arguments":`+tc.args+`}}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"isError":true`) || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	unknown := rpc(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"unknown","arguments":{}}}`)
	if !strings.Contains(unknown.Body.String(), `"error":`) {
		t.Fatal(unknown.Body.String())
	}
	if w := rpc(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "DELETE"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/web", nil)
		r.Header.Set("Accept", "text/event-stream")
		s.ServeHTTP(w, r)
		if w.Code != 405 {
			t.Fatal(method, w.Code)
		}
	}
	for _, origin := range []string{"https://evil.example", "http://127.0.0.1", "null", ""} {
		r := httptest.NewRequest("POST", "/web", strings.NewReader("{}"))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(origin, w.Code)
		}
	}
	if w := rpc(t, s, strings.Repeat(" ", (32<<10)+1)); w.Code != 413 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("POST", "/web", brokenReader{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := rpc(t, s, "{"); w.Code == 200 {
		t.Fatal("malformed JSON accepted")
	}
	if w := call(s, "GET", "/web/extra", ""); w.Code != 404 {
		t.Fatal("route broadened")
	}
	if _, err := mcpResult(make(chan int), nil); err == nil {
		t.Fatal("encoding failure ignored")
	}
	out, err := mcpResult(nil, io.ErrUnexpectedEOF)
	if err != nil || !out.IsError {
		t.Fatal(out, err)
	}
}

// A real MCP SDK client -> HTTP listener -> fixed upstream URL over TLS sockets.
// No public network is contacted by this test.
func TestMCPHTTPEndToEnd(t *testing.T) {
	for _, id := range []string{"tavily", "deepseek"} {
		t.Run(id, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+id+"-secret" || r.Header.Get("Cookie") != "" {
					t.Error("credential isolation failed")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/search":
					io.WriteString(w, `{"results":[{"title":"Go","url":"https://go.dev","content":"docs","score":0.9}]}`)
				case "/extract":
					io.WriteString(w, `{"results":[{"url":"https://go.dev","raw_content":"# Go docs"}]}`)
				case "/v1/tools/search":
					io.WriteString(w, `{"results":[{"title":"Go","url":"https://go.dev","summary":"docs","rerank_score":2.4}]}`)
				case "/v1/tools/fetch":
					io.WriteString(w, `{"title":"Go","content":"# Go docs"}`)
				default:
					t.Error("unexpected path", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			s := New(func() (Config, error) { return webConfig(id, id), nil }, "direct", nil)
			defer s.Close()
			tr := upstream.Client().Transport.(*http.Transport).Clone()
			tr.TLSClientConfig.ServerName = "example.com"
			tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
			}
			s.clients[0].Transport = tr
			downstream := httptest.NewServer(s)
			defer downstream.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: downstream.URL + "/web"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			list, err := session.ListTools(ctx, nil)
			if err != nil || len(list.Tools) != 2 {
				t.Fatal(list, err)
			}
			for _, tc := range []struct {
				name string
				args map[string]any
			}{
				{"search", map[string]any{"query": "Go", "max_results": 1}},
				{"fetch", map[string]any{"url": "https://go.dev", "max_chars": 4}},
			} {
				out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
				if err != nil || out.IsError {
					t.Fatal(out, err)
				}
				data, err := json.Marshal(out.StructuredContent)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), `"provider":"`+id+`"`) {
					t.Fatal(string(data))
				}
				if tc.name == "fetch" && !strings.Contains(string(data), `"truncated":true`) {
					t.Fatal(string(data))
				}
				if len(out.Content) != 1 {
					t.Fatal("missing text fallback")
				}
			}
		})
	}
}

func TestMCPCancellationReachesUpstream(t *testing.T) {
	started, finished := make(chan struct{}), make(chan struct{})
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(finished)
		return nil, r.Context().Err()
	})
	server := httptest.NewServer(s)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/web", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fetch","arguments":{"url":"https://go.dev"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not cancelled")
	}
	<-done
}
