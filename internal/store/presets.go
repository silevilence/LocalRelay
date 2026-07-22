package store

import "localrelay/internal/capabilities"

type ProviderPreset struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	Type             string       `json:"type"`
	BaseURL          string       `json:"baseUrl"`
	CapabilityConfig string       `json:"capabilityConfig"`
	Models           []ModelInput `json:"models"`
}

func BuiltinProviderPresets() []ProviderPreset {
	return []ProviderPreset{
		{
			ID:               "deepseek",
			Name:             "DeepSeek",
			Type:             "deepseek",
			BaseURL:          "https://api.deepseek.com",
			CapabilityConfig: capabilities.DefaultJSON("deepseek"),
			Models: []ModelInput{
				{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Capabilities: `{"tools":true,"thinking":true}`, ContextLength: 1000000, MaxTokens: 384000},
				{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Capabilities: `{"tools":true,"thinking":true}`, ContextLength: 1000000, MaxTokens: 384000},
			},
		},
		{
			ID:               "volcengine-coding",
			Name:             "火山引擎 Coding Plan",
			Type:             "openai-compatible",
			BaseURL:          "https://ark.cn-beijing.volces.com/api/coding/v3",
			CapabilityConfig: capabilities.DefaultJSON("openai-compatible"),
			Models: []ModelInput{
				{ID: "ark-code-latest", Name: "Ark Code Latest", Capabilities: `{"tools":true,"stream":true}`, ContextLength: 262144, MaxTokens: 32768},
			},
		},
		{
			ID:               "opencode-go",
			Name:             "Opencode GO",
			Type:             "openai-compatible",
			BaseURL:          "https://opencode.ai/zen/go/v1",
			CapabilityConfig: capabilities.DefaultJSON("openai-compatible"),
			Models: []ModelInput{
				{ID: "kimi-k3", Name: "Kimi K3", Capabilities: `{"tools":true,"thinking":true}`, ContextLength: 1048576, MaxTokens: 1048576},
				{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Capabilities: `{"tools":true,"thinking":true}`, ContextLength: 1000000, MaxTokens: 384000},
			},
		},
		{
			ID:               "siliconflow",
			Name:             "硅基流动",
			Type:             "siliconflow",
			BaseURL:          "https://api.siliconflow.com/v1",
			CapabilityConfig: capabilities.DefaultJSON("siliconflow"),
			Models: []ModelInput{
				{ID: "Qwen/Qwen3-Coder-480B-A35B-Instruct", Name: "Qwen3 Coder", Capabilities: `{"tools":true}`, ContextLength: 262144, MaxTokens: 262144},
				{ID: "deepseek-ai/DeepSeek-V4-Flash", Name: "DeepSeek V4 Flash", Capabilities: `{"tools":true,"thinking":true}`, ContextLength: 1048576, MaxTokens: 393216},
			},
		},
	}
}
