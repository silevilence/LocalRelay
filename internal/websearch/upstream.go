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

type provider interface {
	search(context.Context, []*http.Client, string, Request) (Response, error)
	fetch(context.Context, []*http.Client, string, FetchRequest) (FetchResponse, error)
}

// Only adapters supply endpoints. Never forward client credentials or cookies.
// Retry routes only before sending a billable search/extraction request.
func postJSON(ctx context.Context, clients []*http.Client, key, endpoint string, input any) (*http.Response, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	for i, client := range clients {
		var wrote atomic.Bool
		trace := &httptrace.ClientTrace{WroteHeaders: func() { wrote.Store(true) }, WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) }}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil && !wrote.Load() && ctx.Err() == nil && i+1 < len(clients) {
			continue
		}
		return resp, err
	}
	return nil, fmt.Errorf("no web transport")
}

func readJSON(resp *http.Response, operation string, limit int64, target any) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Never return upstream bodies: they can echo credentials or request data.
		code, message, status := "upstream_error", "网页服务返回错误", 502
		switch resp.StatusCode {
		case 401, 403:
			code, message = "auth_failed", "网页服务认证失败，请检查 API Key"
			if resp.StatusCode == 403 && operation == "fetch" {
				code, message = "forbidden", "网页抓取被上游访问策略拒绝"
			}
		case 402, 432, 433:
			code, message, status = "quota_exceeded", "网页服务额度不足，请检查账户额度", 429
		case 429:
			code, message, status = "rate_limited", "网页服务请求过于频繁，请稍后重试", 429
		case 404:
			if operation == "fetch" {
				code, message, status = "not_found", "无法提取该网页正文", 404
			}
		case 408, 504:
			code, message, status = "timeout", "网页服务响应超时", 504
		}
		return &apiError{operation + "_" + code, message, status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("web response too large")
	}
	return json.Unmarshal(body, target)
}
