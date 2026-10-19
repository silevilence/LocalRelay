// Package websearch exposes a provider-independent search API for local agents.
package websearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Config struct {
	SearchProvider string
	FetchProvider  string
	APIKeys        map[string]string
}

func DefaultConfig() Config {
	return Config{SearchProvider: "tavily", FetchProvider: "tavily", APIKeys: map[string]string{}}
}

type ProviderInfo struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	AdvancedSearch     bool   `json:"advancedSearch"`
	MaxQueryCharacters int    `json:"maxQueryCharacters"`
}

// Each provider owns its request/response mapping; HTTP and storage do not need
// provider-specific switches when a new adapter is registered here.
var providers = []struct {
	ProviderInfo
	adapter      provider
	capabilities searchCapabilities
}{
	{ProviderInfo{"tavily", "Tavily", true, 0}, tavily{}, searchCapabilities{}},
	{ProviderInfo{"deepseek", "DeepSeek", false, 128}, deepseek{}, searchCapabilities{OriginalQuestion: true, Localization: true}},
}

func Providers() []ProviderInfo {
	items := make([]ProviderInfo, 0, len(providers))
	for _, p := range providers {
		items = append(items, p.ProviderInfo)
	}
	return items
}

func (c Config) Validate() error {
	if findProvider(c.SearchProvider) == nil || findProvider(c.FetchProvider) == nil {
		return fmt.Errorf("不支持的搜索或抓取服务")
	}
	for id, key := range c.APIKeys {
		if findProvider(id) == nil {
			return fmt.Errorf("不支持的凭据服务商")
		}
		if len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
			return fmt.Errorf("网页服务 API Key 格式无效")
		}
	}
	return nil
}

func findProvider(id string) provider {
	for _, p := range providers {
		if p.ID == id {
			return p.adapter
		}
	}
	return nil
}

type Request struct {
	Query            string   `json:"query"`
	MaxResults       int      `json:"max_results"`
	SearchDepth      string   `json:"search_depth"`
	TimeRange        string   `json:"time_range,omitempty"`
	IncludeDomains   []string `json:"include_domains,omitempty"`
	ExcludeDomains   []string `json:"exclude_domains,omitempty"`
	OriginalQuestion string   `json:"ori_question,omitempty"`
	NeedContent      bool     `json:"need_content,omitempty"`
	AddLocalization  bool     `json:"add_localization,omitempty"`
	After            string   `json:"after,omitempty"`
	Before           string   `json:"before,omitempty"`
	specified        map[string]json.RawMessage
	localization     string
}

func (r *Request) validate() error {
	r.Query = strings.TrimSpace(r.Query)
	if r.Query == "" || len(r.Query) > 4096 {
		return fmt.Errorf("query 必须为 1–4096 字节的非空文本")
	}
	if r.MaxResults < 1 || r.MaxResults > 20 {
		return fmt.Errorf("max_results 必须为 1–20")
	}
	if r.SearchDepth != "basic" && r.SearchDepth != "advanced" {
		return fmt.Errorf("search_depth 必须为 basic 或 advanced")
	}
	for _, date := range []*string{&r.After, &r.Before} {
		if len(*date) == 4 {
			*date += "-01-01"
		}
		if *date != "" {
			if _, err := time.Parse("2006-01-02", *date); err != nil {
				return fmt.Errorf("after / before 必须为 YYYY 或有效的 YYYY-MM-DD 日期")
			}
		}
	}
	if r.After != "" && r.Before != "" && r.After >= r.Before {
		return fmt.Errorf("after 必须早于 before")
	}
	switch r.TimeRange {
	case "", "day", "week", "month", "year":
	default:
		return fmt.Errorf("time_range 必须为 day、week、month 或 year")
	}
	for _, list := range [][]string{r.IncludeDomains, r.ExcludeDomains} {
		if len(list) > 100 {
			return fmt.Errorf("域名过滤列表最多 100 项")
		}
		for _, domain := range list {
			if len(domain) == 0 || len(domain) > 253 || strings.ContainsAny(domain, "/:@?#\\ \t\r\n\x00") {
				return fmt.Errorf("域名过滤项必须为不含协议、端口或路径的域名")
			}
		}
	}
	return nil
}

type Result struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	RawContent string  `json:"raw_content,omitempty"`
}

type Response struct {
	Version  string    `json:"version"`
	Provider string    `json:"provider"`
	Query    string    `json:"query"`
	Results  []Result  `json:"results"`
	Warnings []Warning `json:"warnings,omitempty"`
}

func decodeRequest(body []byte) (Request, error) {
	input := Request{MaxResults: 5, SearchDepth: "basic"}
	if err := decodeObject(body, &input); err != nil {
		return input, err
	}
	// Keep presence separately: even an explicitly supplied false value for an
	// unsupported parameter deserves a warning, unlike an omitted default.
	if err := json.Unmarshal(body, &input.specified); err != nil {
		return input, err
	}
	return input, input.validate()
}

func decodeObject(body []byte, target any) error {
	// JSON null is not a search request; null optional fields also must not
	// silently inherit defaults and hide client schema mistakes.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return fmt.Errorf("请求体必须为 JSON 对象")
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("请求字段不能为 null")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求字段未知或类型错误，请参考 /docs/search")
	}
	return nil
}
