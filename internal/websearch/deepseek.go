package websearch

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type deepseek struct{}

// Search syntax and field mapping:
// https://api-docs.deepseek.com/zh-cn/guides/search
func deepseekQuery(input Request, now time.Time) (string, error) {
	parts := []string{input.Query}
	for _, domains := range [][]string{input.IncludeDomains, input.ExcludeDomains} {
		for _, domain := range domains {
			// Prevent filter data from injecting DeepSeek search operators.
			for _, r := range domain {
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' {
					return "", fmt.Errorf("DeepSeek 域名过滤仅支持字母、数字、点和连字符")
				}
			}
		}
	}
	if len(input.IncludeDomains) > 0 {
		sites := make([]string, len(input.IncludeDomains))
		for i, domain := range input.IncludeDomains {
			sites[i] = "site:" + domain
		}
		parts = append(parts, strings.Join(sites, " OR "))
	}
	for _, domain := range input.ExcludeDomains {
		parts = append(parts, "-site:"+domain)
	}
	if input.TimeRange != "" {
		// DeepSeek filters publication dates at day precision; it cannot express
		// Tavily's rolling-hour boundary. Use UTC calendar dates and document this.
		start := now.UTC()
		switch input.TimeRange {
		case "day":
			start = start.AddDate(0, 0, -1)
		case "week":
			start = start.AddDate(0, 0, -7)
		case "month":
			start = start.AddDate(0, -1, 0)
		case "year":
			start = start.AddDate(-1, 0, 0)
		}
		parts = append(parts, "after:"+start.Format("2006-01-02"))
	}
	if input.After != "" {
		parts = append(parts, "after:"+input.After)
	}
	if input.Before != "" {
		parts = append(parts, "before:"+input.Before)
	}
	query := strings.Join(parts, " ")
	if utf8.RuneCountInString(query) > 128 {
		return "", fmt.Errorf("DeepSeek 查询含过滤条件最多 128 个 Unicode 字符，请缩短查询或过滤条件")
	}
	return query, nil
}

func (deepseek) search(ctx context.Context, clients []*http.Client, key string, input Request) (Response, error) {
	if utf8.RuneCountInString(input.OriginalQuestion) > 128 || (input.OriginalQuestion != "" && strings.TrimSpace(input.OriginalQuestion) == "") || (input.specified["ori_question"] != nil && input.OriginalQuestion == "") {
		return Response{}, &apiError{"invalid_request", "ori_question 必须为 1–128 个字符的非空文本", 400}
	}
	query, err := deepseekQuery(input, time.Now())
	if err != nil {
		return Response{}, &apiError{"invalid_request", err.Error(), 400}
	}
	resp, err := postJSON(ctx, clients, key, "https://api.deepseek.com/v1/tools/search", struct {
		Query            string `json:"query"`
		Count            int    `json:"count"`
		NeedContent      bool   `json:"need_content"`
		OriginalQuestion string `json:"ori_question,omitempty"`
		Localization     string `json:"localization,omitempty"`
	}{query, input.MaxResults, input.NeedContent, input.OriginalQuestion, input.localization})
	if err != nil {
		return Response{}, err
	}
	var decoded struct {
		Results *[]struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Summary string  `json:"summary"`
			Snippet string  `json:"snippet"`
			Score   float64 `json:"rerank_score"`
			Content string  `json:"content"`
		} `json:"results"`
	}
	if err := readJSON(resp, "search", searchResponseLimit(input.NeedContent), &decoded); err != nil {
		return Response{}, err
	}
	if decoded.Results == nil {
		return Response{}, fmt.Errorf("search response missing results")
	}
	out := Response{Results: []Result{}}
	for _, item := range *decoded.Results {
		content := item.Summary
		if content == "" {
			content = item.Snippet
		}
		// Preserve native 0–3 scores: cross-provider scores are not comparable.
		itemResult := Result{Title: item.Title, URL: item.URL, Content: content, Score: item.Score}
		if input.NeedContent {
			itemResult.RawContent = item.Content
		}
		out.Results = append(out.Results, itemResult)
	}
	return out, nil
}

func (deepseek) fetch(ctx context.Context, clients []*http.Client, key string, input FetchRequest) (FetchResponse, error) {
	resp, err := postJSON(ctx, clients, key, "https://api.deepseek.com/v1/tools/fetch", struct {
		URL string `json:"url"`
	}{input.URL})
	if err != nil {
		return FetchResponse{}, err
	}
	var decoded struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := readJSON(resp, "fetch", 8<<20, &decoded); err != nil {
		return FetchResponse{}, err
	}
	if strings.TrimSpace(decoded.Content) == "" {
		return FetchResponse{}, fmt.Errorf("fetch response missing content")
	}
	return FetchResponse{Title: decoded.Title, Content: decoded.Content}, nil
}
