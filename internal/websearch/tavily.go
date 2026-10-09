package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
)

type searchProvider interface {
	search(context.Context, []*http.Client, string, Request) (Response, error)
}

type tavily struct{}

func (tavily) search(ctx context.Context, clients []*http.Client, key string, input Request) (Response, error) {
	// Tavily mapping: https://docs.tavily.com/documentation/api-reference/endpoint/search
	// Disable automatic parameter selection and generated answers: search depth
	// and its credit cost must stay under the caller's explicit control.
	payload, err := json.Marshal(struct {
		Request
		AutoParameters    bool `json:"auto_parameters"`
		IncludeAnswer     bool `json:"include_answer"`
		IncludeRawContent bool `json:"include_raw_content"`
	}{Request: input})
	if err != nil {
		return Response{}, err
	}
	for i, client := range clients {
		var wrote atomic.Bool
		trace := &httptrace.ClientTrace{WroteHeaders: func() { wrote.Store(true) }, WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) }}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(payload))
		if err != nil {
			return Response{}, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			// Only switch routes before sending the search request. Retrying a
			// submitted search (even after 5xx) can consume credits twice.
			if !wrote.Load() && ctx.Err() == nil && i+1 < len(clients) {
				continue
			}
			return Response{}, err
		}
		return readTavilyResponse(resp)
	}
	return Response{}, fmt.Errorf("no search transport")
}

func readTavilyResponse(resp *http.Response) (Response, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Never return upstream bodies: they can echo credentials or request data.
		switch resp.StatusCode {
		case 401, 403:
			return Response{}, &apiError{"search_auth_failed", "搜索服务认证失败，请检查 API Key", 502}
		case 429:
			return Response{}, &apiError{"search_rate_limited", "搜索服务请求过于频繁，请稍后重试", 429}
		case 432, 433:
			return Response{}, &apiError{"search_quota_exceeded", "搜索服务额度不足，请检查账户额度", 429}
		default:
			return Response{}, &apiError{"search_upstream_error", "搜索服务返回错误", 502}
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return Response{}, err
	}
	if len(body) > 4<<20 {
		return Response{}, fmt.Errorf("search response too large")
	}
	var decoded struct {
		Results *[]Result `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Response{}, err
	}
	if decoded.Results == nil {
		return Response{}, fmt.Errorf("search response missing results")
	}
	return Response{Results: *decoded.Results}, nil
}
