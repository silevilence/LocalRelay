package websearch

import (
	"net"
	"net/http"
	"net/url"
	"time"
)

func newClients(mode string, proxyURL *url.URL) []*http.Client {
	routes := []*url.URL{nil}
	if mode == "proxy" {
		routes = []*url.URL{proxyURL}
	} else if mode == "auto" && proxyURL != nil {
		routes = append(routes, proxyURL)
	}
	clients := make([]*http.Client, 0, len(routes))
	for _, route := range routes {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil // Never inherit system HTTP(S)_PROXY credentials.
		if route != nil {
			transport.Proxy = http.ProxyURL(route)
		}
		transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		transport.TLSHandshakeTimeout = 5 * time.Second
		transport.ResponseHeaderTimeout = 45 * time.Second
		clients = append(clients, &http.Client{
			Transport: transport, Timeout: searchTimeout,
			// The search API has a fixed HTTPS endpoint. Do not follow redirects
			// with a credential-bearing request, including same-host redirects.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		})
	}
	return clients
}
