package websearch

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// DeepSeek requires an IP, not an OS locale or timezone. Only an explicit
// add_localization request uses ipify (https://www.ipify.org/). This observes
// the configured outbound route, so a proxy may supply its own location.
type localizationResolver struct {
	mu      sync.Mutex
	ip      string
	err     error
	expires time.Time
}

func (r *localizationResolver) resolve(ctx context.Context, clients []*http.Client) (string, error) {
	r.mu.Lock()
	if time.Now().Before(r.expires) {
		ip, err := r.ip, r.err
		r.mu.Unlock()
		return ip, err
	}
	r.mu.Unlock()
	ip, err := lookupPublicIP(ctx, clients)
	// Do not cache cancellation of an individual caller for other requests.
	if ctx.Err() == nil {
		ttl := 5 * time.Minute
		if err != nil {
			ttl = 30 * time.Second
		}
		r.mu.Lock()
		r.ip, r.err, r.expires = ip, err, time.Now().Add(ttl)
		r.mu.Unlock()
	}
	return ip, err
}

func lookupPublicIP(ctx context.Context, clients []*http.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, client := range clients {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api64.ipify.org?format=json", nil)
		if err != nil {
			return "", err
		}
		// No API key, caller headers, query text or cookies go to the IP service.
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var decoded struct {
			IP string `json:"ip"`
		}
		if err := readJSON(resp, "localization", 256, &decoded); err != nil {
			continue
		}
		ip, err := netip.ParseAddr(decoded.IP)
		if err == nil && ip.Zone() == "" && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() {
			return ip.Unmap().String(), nil
		}
	}
	return "", fmt.Errorf("public IP lookup unavailable")
}
