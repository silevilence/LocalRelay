package websearch

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

type tavily struct{}

func (tavily) search(ctx context.Context, clients []*http.Client, key string, input Request) (Response, error) {
	// https://docs.tavily.com/documentation/api-reference/endpoint/search
	// Explicit wire fields keep LocalRelay-only options out of the upstream API.
	resp, err := postJSON(ctx, clients, key, "https://api.tavily.com/search", struct {
		Query             string   `json:"query"`
		MaxResults        int      `json:"max_results"`
		SearchDepth       string   `json:"search_depth"`
		TimeRange         string   `json:"time_range,omitempty"`
		IncludeDomains    []string `json:"include_domains,omitempty"`
		ExcludeDomains    []string `json:"exclude_domains,omitempty"`
		StartDate         string   `json:"start_date,omitempty"`
		EndDate           string   `json:"end_date,omitempty"`
		AutoParameters    bool     `json:"auto_parameters"`
		IncludeAnswer     bool     `json:"include_answer"`
		IncludeRawContent bool     `json:"include_raw_content"`
	}{Query: input.Query, MaxResults: input.MaxResults, SearchDepth: input.SearchDepth,
		TimeRange: input.TimeRange, IncludeDomains: input.IncludeDomains, ExcludeDomains: input.ExcludeDomains,
		StartDate: input.After, EndDate: input.Before, IncludeRawContent: input.NeedContent})
	if err != nil {
		return Response{}, err
	}
	var decoded struct {
		Results *[]Result `json:"results"`
	}
	if err := readJSON(resp, "search", searchResponseLimit(input.NeedContent), &decoded); err != nil {
		return Response{}, err
	}
	if decoded.Results == nil {
		return Response{}, fmt.Errorf("search response missing results")
	}
	if !input.NeedContent {
		for i := range *decoded.Results {
			(*decoded.Results)[i].RawContent = ""
		}
	}
	return Response{Results: *decoded.Results}, nil
}

func (tavily) fetch(ctx context.Context, clients []*http.Client, key string, input FetchRequest) (FetchResponse, error) {
	// https://docs.tavily.com/documentation/api-reference/endpoint/extract
	// Omit query: query-based extraction returns ranked snippets, not page text.
	resp, err := postJSON(ctx, clients, key, "https://api.tavily.com/extract", struct {
		URLs   []string `json:"urls"`
		Format string   `json:"format"`
		Depth  string   `json:"extract_depth"`
	}{[]string{input.URL}, "markdown", "basic"})
	if err != nil {
		return FetchResponse{}, err
	}
	var decoded struct {
		Results []struct {
			URL     string `json:"url"`
			Content string `json:"raw_content"`
		} `json:"results"`
		Failed []struct {
			URL string `json:"url"`
		} `json:"failed_results"`
	}
	if err := readJSON(resp, "fetch", 8<<20, &decoded); err != nil {
		return FetchResponse{}, err
	}
	for _, failed := range decoded.Failed {
		if failed.URL == input.URL {
			return FetchResponse{}, &apiError{"fetch_not_found", "无法提取该网页正文", 404}
		}
	}
	if len(decoded.Results) != 1 || strings.TrimSpace(decoded.Results[0].Content) == "" {
		return FetchResponse{}, &apiError{"fetch_not_found", "网页服务未返回可用正文", 404}
	}
	// Tavily does not promise a title; do not invent one from the URL or text.
	return FetchResponse{Content: decoded.Results[0].Content}, nil
}
