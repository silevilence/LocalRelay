package websearch

import "fmt"

type Warning struct {
	Code      string `json:"code"`
	Parameter string `json:"parameter"`
	Message   string `json:"message"`
}

type searchCapabilities struct {
	OriginalQuestion bool
	Localization     bool
}

func searchResponseLimit(needContent bool) int64 {
	if needContent {
		return 16 << 20
	}
	return 4 << 20
}

// Capability differences are declared in the provider registry. Unsupported
// known options are removed before any localization or billable request occurs.
func prepareSearch(input Request, id string) (Request, []Warning) {
	var caps searchCapabilities
	var advanced bool
	for _, p := range providers {
		if p.ID == id {
			caps = p.capabilities
			advanced = p.AdvancedSearch
			break
		}
	}
	var warnings []Warning
	ignore := func(parameter string) {
		warnings = append(warnings, Warning{"unsupported_parameter", parameter, fmt.Sprintf("%s 不支持 %s，已忽略该参数并继续搜索", id, parameter)})
	}
	if !advanced && input.SearchDepth == "advanced" {
		ignore("search_depth")
		input.SearchDepth = "basic"
	}
	if !caps.OriginalQuestion && (input.specified["ori_question"] != nil || input.OriginalQuestion != "") {
		ignore("ori_question")
		input.OriginalQuestion = ""
	}
	if !caps.Localization && (input.specified["add_localization"] != nil || input.AddLocalization) {
		ignore("add_localization")
		input.AddLocalization = false
	}
	if input.TimeRange != "" && (input.After != "" || input.Before != "") {
		input.TimeRange = ""
		warnings = append(warnings, Warning{"parameter_overridden", "time_range", "已使用显式 after / before 日期，忽略 time_range"})
	}
	return input, warnings
}
