package websearch

import (
	"context"
	"errors"
	"net"
)

func (s *Server) configuration(operation string) (provider, string, string, error) {
	cfg, err := s.load()
	if err != nil || cfg.Validate() != nil {
		return nil, "", "", &apiError{operation + "_unavailable", "网页服务配置不可用", 503}
	}
	id := cfg.SearchProvider
	if operation == "fetch" {
		id = cfg.FetchProvider
	}
	key := cfg.APIKeys[id]
	if key == "" {
		return nil, "", "", &apiError{operation + "_not_configured", "请在设置中配置所选网页服务的 API Key", 503}
	}
	return findProvider(id), id, key, nil
}

func serviceError(ctx context.Context, operation string, err error) error {
	var upstream *apiError
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded || (errors.As(err, &timeout) && timeout.Timeout()) {
		return &apiError{operation + "_timeout", "网页服务响应超时", 504}
	}
	if errors.As(err, &upstream) {
		return upstream
	}
	return &apiError{operation + "_upstream_error", "网页服务请求失败", 502}
}

func (s *Server) Search(ctx context.Context, input Request) (Response, error) {
	if err := input.validate(); err != nil {
		return Response{}, &apiError{"invalid_request", err.Error(), 400}
	}
	adapter, id, key, err := s.configuration("search")
	if err != nil {
		return Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	input, warnings := prepareSearch(input, id)
	if input.AddLocalization {
		input.localization, err = s.localization.resolve(ctx, s.clients)
		if err != nil {
			warnings = append(warnings, Warning{"localization_unavailable", "add_localization", "无法获取公网出口 IP，已省略本地化信息并继续搜索"})
		}
	}
	response, err := adapter.search(ctx, s.clients, key, input)
	if err != nil {
		return Response{}, serviceError(ctx, "search", err)
	}
	response.Version, response.Provider, response.Query = "1", id, input.Query
	response.Warnings = append(warnings, response.Warnings...)
	if response.Results == nil {
		response.Results = []Result{}
	}
	return response, nil
}

func (s *Server) Fetch(ctx context.Context, input FetchRequest) (FetchResponse, error) {
	if err := input.validate(); err != nil {
		return FetchResponse{}, &apiError{"invalid_request", err.Error(), 400}
	}
	adapter, id, key, err := s.configuration("fetch")
	if err != nil {
		return FetchResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	response, err := adapter.fetch(ctx, s.clients, key, input)
	if err != nil {
		return FetchResponse{}, serviceError(ctx, "fetch", err)
	}
	response.Provider, response.URL = id, input.URL
	chars := []rune(response.Content)
	if len(chars) > input.MaxChars {
		response.Content = string(chars[:input.MaxChars])
		response.Truncated = true
	}
	return response, nil
}
