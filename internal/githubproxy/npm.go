package githubproxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const npmHost = "registry.npmjs.org"
const maxNPMMetadata = 64 << 20

var npmPart = regexp.MustCompile(`^[A-Za-z0-9_~][A-Za-z0-9._~-]*$`)
var npmVersionPart = regexp.MustCompile(`^[A-Za-z0-9_~][A-Za-z0-9._~+-]*$`)

// npm registry paths: https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md
// Scoped packuments use @scope%2fname; tarballs use @scope/name/-/name-version.tgz.
func npmPackagePath(path string) (name, tail string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\%\x00\r\n") {
			return "", "", false
		}
	}
	i := 1
	if strings.HasPrefix(parts[0], "@") {
		if len(parts) < 2 || !npmPart.MatchString(strings.TrimPrefix(parts[0], "@")) || !npmPart.MatchString(parts[1]) {
			return "", "", false
		}
		i = 2
	} else if !npmPart.MatchString(parts[0]) {
		return "", "", false
	}
	name = strings.Join(parts[:i], "/")
	tail = strings.Join(parts[i:], "/")
	if tail == "" || (len(parts) == i+1 && npmVersionPart.MatchString(tail)) {
		return name, tail, true
	}
	if len(parts) == i+2 && parts[i] == "-" && npmVersionPart.MatchString(parts[i+1]) && strings.HasSuffix(parts[i+1], ".tgz") {
		return name, tail, true
	}
	return "", "", false
}

func npmMetadataPath(path string) bool {
	_, tail, ok := npmPackagePath(path)
	return ok && !strings.HasPrefix(tail, "-/")
}

// npm/pacote remote.js replaces an official tarball's origin with the configured
// registry using new URL(absolutePath, registry), dropping /npm/. Older lockfiles
// therefore need this narrow GET/HEAD tarball alias, not a general root proxy.
func npmTarballAlias(r *http.Request) bool {
	_, tail, ok := npmPackagePath(r.URL.Path)
	return ok && strings.HasPrefix(tail, "-/") && (r.Method == http.MethodGet || r.Method == http.MethodHead)
}

func npmTarget(r *http.Request) (*url.URL, string, error) {
	path := strings.TrimPrefix(r.URL.Path, "/npm")
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/npm")
	if npmTarballAlias(r) {
		path, escaped = r.URL.Path, r.URL.EscapedPath()
	} else if r.URL.Path != "/npm" && !strings.HasPrefix(r.URL.Path, "/npm/") {
		return nil, "", fmt.Errorf("请使用 /npm/ 访问公开 npm 包")
	}
	if path == "" {
		path, escaped = "/", "/"
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, "", fmt.Errorf("无效 npm 查询参数")
	}
	allowedQuery := map[string]bool{}
	key := "registry"
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch path {
	case "/", "/-/ping", "/-/npm/v1/keys":
		if !read {
			return nil, "", fmt.Errorf("仅支持只读 npm 请求")
		}
		if path == "/-/ping" {
			allowedQuery["write"] = true
		}
	case "/-/v1/search":
		if !read {
			return nil, "", fmt.Errorf("仅支持只读 npm 请求")
		}
		for _, k := range []string{"text", "size", "from", "quality", "popularity", "maintenance"} {
			allowedQuery[k] = true
		}
	case "/-/npm/v1/security/advisories/bulk", "/-/npm/v1/security/audits/quick":
		// These POSTs only query vulnerabilities; publishing/login remain blocked.
		// https://docs.npmjs.com/cli/v11/commands/npm-audit#description
		if r.Method != http.MethodPost {
			return nil, "", fmt.Errorf("npm 审计接口仅支持 POST")
		}
	default:
		name, _, ok := npmPackagePath(path)
		if !read || !ok {
			return nil, "", fmt.Errorf("仅支持公开包读取、下载与审计")
		}
		key = name
	}
	for k := range query {
		if !allowedQuery[k] {
			return nil, "", fmt.Errorf("不支持的 npm 查询参数")
		}
	}
	u := &url.URL{Scheme: "https", Host: npmHost, Path: path, RawPath: escaped, RawQuery: query.Encode()}
	return u, key, nil
}

func npmAllowedURL(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && strings.EqualFold(u.Hostname(), npmHost) && (u.Port() == "" || u.Port() == "443")
}

func npmRedirect(r *http.Request, via []*http.Request) error {
	if len(via) >= 10 || !npmAllowedURL(r.URL) {
		return fmt.Errorf("npm 重定向目标不允许")
	}
	copyURL := *r.URL
	copyURL.Path = "/npm" + r.URL.Path
	copyURL.RawPath = "/npm" + r.URL.EscapedPath()
	copyRequest := *r
	copyRequest.URL = &copyURL
	_, _, err := npmTarget(&copyRequest)
	return err
}

func rewriteNPMResponse(resp *http.Response, r *http.Request, body io.Reader) error {
	// Use the client's origin (including LAN addresses), never an untrusted
	// X-Forwarded-Host. This service is directly exposed as plain HTTP.
	base, err := url.Parse("http://" + r.Host)
	if err != nil || base.Host == "" || base.User != nil || base.Path != "" || base.RawQuery != "" || base.Fragment != "" {
		return fmt.Errorf("无效本地访问地址")
	}
	if r.Method != http.MethodHead {
		switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
		case "", "identity":
		case "gzip":
			reader, err := gzip.NewReader(body)
			if err != nil {
				return err
			}
			defer reader.Close()
			body = reader
		default:
			return fmt.Errorf("不支持的 npm 元数据编码")
		}
		data, err := io.ReadAll(io.LimitReader(body, maxNPMMetadata+1))
		if err != nil {
			return err
		}
		if len(data) > maxNPMMetadata {
			return fmt.Errorf("npm 元数据过大")
		}
		data, err = rewriteNPMPackument(data, base.String()+"/npm")
		if err != nil {
			return err
		}
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(data))
		resp.ContentLength = int64(len(data))
		resp.Header.Set("Content-Length", strconv.Itoa(len(data)))
	} else {
		resp.Header.Del("Content-Length")
	}
	for _, k := range []string{"Content-Encoding", "ETag", "Last-Modified", "Content-Range", "Accept-Ranges", "Expires"} {
		resp.Header.Del(k)
	}
	resp.Header.Set("Content-Type", "application/json")
	// Local origin/port is embedded in tarball URLs. Avoid reusing metadata from
	// another origin or a previous port, including npm's persistent HTTP cache.
	resp.Header.Set("Cache-Control", "no-store")
	return nil
}

// RawMessage preserves unknown fields and numeric precision. Only dist.tarball
// on the version document or its versions entries is changed; integrity and
// signatures remain untouched. External tarball URLs are intentionally retained.
func rewriteNPMPackument(data []byte, base string) ([]byte, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if document == nil {
		return nil, fmt.Errorf("无效 npm 元数据")
	}
	if err := rewriteNPMDist(document, base); err != nil {
		return nil, err
	}
	if raw, ok := document["versions"]; ok {
		var versions map[string]json.RawMessage
		if err := json.Unmarshal(raw, &versions); err != nil {
			return nil, err
		}
		for version, value := range versions {
			var manifest map[string]json.RawMessage
			if err := json.Unmarshal(value, &manifest); err != nil {
				return nil, err
			}
			if manifest == nil {
				return nil, fmt.Errorf("无效 npm 版本元数据")
			}
			if err := rewriteNPMDist(manifest, base); err != nil {
				return nil, err
			}
			versions[version], _ = json.Marshal(manifest)
		}
		document["versions"], _ = json.Marshal(versions)
	}
	return json.Marshal(document)
}

func rewriteNPMDist(document map[string]json.RawMessage, base string) error {
	if raw, ok := document["dist"]; ok {
		var dist map[string]json.RawMessage
		if err := json.Unmarshal(raw, &dist); err != nil {
			return err
		}
		var tarball string
		if json.Unmarshal(dist["tarball"], &tarball) == nil {
			u, err := url.Parse(tarball)
			if err == nil && npmAllowedURL(u) && u.RawQuery == "" && u.Fragment == "" {
				_, tail, valid := npmPackagePath(u.Path)
				if valid && strings.HasPrefix(tail, "-/") {
					dist["tarball"], _ = json.Marshal(base + u.EscapedPath())
					document["dist"], _ = json.Marshal(dist)
				}
			}
		}
	}
	return nil
}
