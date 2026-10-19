package websearch

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

type FetchRequest struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars"`
}

type FetchResponse struct {
	Provider  string `json:"provider"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

func decodeFetchRequest(body []byte) (FetchRequest, error) {
	input := FetchRequest{MaxChars: 20000}
	if err := decodeObject(body, &input); err != nil {
		return input, err
	}
	return input, input.validate()
}

func (r *FetchRequest) validate() error {
	r.URL = strings.TrimSpace(r.URL)
	u, err := url.Parse(r.URL)
	if err != nil || len(r.URL) > 8192 || u == nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return fmt.Errorf("url 必须为不含用户名密码的 HTTP/HTTPS 网页地址，最多 8192 字节")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("fetch 仅支持公开网页")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return fmt.Errorf("fetch 仅支持公开网页")
		}
	}
	// The URL is data sent to a fixed provider endpoint, never dialed locally.
	if r.MaxChars < 1 || r.MaxChars > 100000 {
		return fmt.Errorf("max_chars 必须为 1–100000")
	}
	return nil
}
