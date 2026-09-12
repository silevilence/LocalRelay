package relay

import (
	"net/http"
	"strings"
)

// excludedClientHeaders is read-only; request-specific Connection fields must
// never be added here, or one request could change filtering for another.
var excludedClientHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-connection":    true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"cookie":              true,
	"host":                true, // Also exclude Header entries inserted programmatically; HTTP parsing uses Request.Host.
	"content-length":      true, // Recomputed from the converted body by net/http.
	// The inbound body is fully read and re-marshalled into uncompressed JSON.
	// Its encoding and 100-continue handshake do not describe this new body.
	"content-encoding": true,
	"expect":           true,
	// Let Go Transport negotiate gzip and transparently decompress JSON/SSE.
	// Copying the client's encoding disables that automatic decompression:
	// https://pkg.go.dev/net/http#Transport (DisableCompression).
	"accept-encoding": true,
	// Remove client credentials even when the provider has no API key. These
	// protocol headers are rebuilt by postProvider for the selected upstream.
	"authorization":     true,
	"x-api-key":         true,
	"x-goog-api-key":    true,
	"anthropic-version": true,
	"content-type":      true,
}

// upstreamHeaders preserves arbitrary client headers and all their values by
// default. Each attempt owns its copy, so filtering, authentication and transport
// changes cannot mutate the inbound snapshot shared by aggregation retries.
func upstreamHeaders(incoming http.Header) http.Header {
	headers := incoming.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	connectionFields := make(map[string]bool)
	// RFC 9110 section 7.6.1 also requires removing fields named by Connection.
	// Inspect all lines before deleting anything; names are case-insensitive.
	// https://www.rfc-editor.org/rfc/rfc9110.html#section-7.6.1
	for name, values := range headers {
		if strings.ToLower(name) == "connection" {
			for _, value := range values {
				for _, token := range strings.Split(value, ",") {
					connectionFields[strings.ToLower(strings.TrimSpace(token))] = true
				}
			}
		}
	}
	for name := range headers {
		lowerName := strings.ToLower(name)
		if excludedClientHeaders[lowerName] || connectionFields[lowerName] {
			delete(headers, name)
		}
	}
	return headers
}
