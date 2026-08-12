package capabilities

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	ProtocolOpenAIChat     = "openai_chat"
	ProtocolAnthropic      = "anthropic_messages"
	ProtocolGemini         = "gemini"
	ProtocolOpenAIResponse = "openai_responses"
)
const ThinkingFieldReasoningContent = "reasoning_content"

type Provider struct {
	// Protocol selects the upstream request/response shape used by the relay.
	Protocol string `json:"protocol"`
	// Thinking describes provider-specific thinking controls and content fields.
	Thinking        Thinking        `json:"thinking,omitempty"`
	Sampling        Sampling        `json:"sampling,omitempty"`
	ReasoningEffort ReasoningEffort `json:"reasoningEffort,omitempty"`
	ToolCalls       ToolCalls       `json:"toolCalls,omitempty"`
	// Streaming describes provider-specific options for streamed responses.
	Streaming Streaming `json:"streaming,omitempty"`
	// AllowThinkingDowngrade is a model-level policy populated by ApplyModel.
	// It is deliberately excluded from provider JSON so downgrading can never be
	// enabled implicitly for every model on a provider.
	AllowThinkingDowngrade bool `json:"-"`
}

type Thinking struct {
	// RequestFields names top-level request fields accepted by the provider,
	// such as "thinking", "enable_thinking", or "thinking_budget".
	RequestFields []string `json:"requestFields,omitempty"`
	// RequestMessageField maps IR thinking blocks in assistant history messages.
	// OpenAI Chat structs currently support only "reasoning_content".
	RequestMessageField string `json:"requestMessageField,omitempty"`
	// ResponseContentField maps upstream assistant thinking text into IR.
	// OpenAI Chat structs currently support only "reasoning_content".
	ResponseContentField string `json:"responseContentField,omitempty"`
	// DefaultEnabled marks providers such as DeepSeek whose thinking mode is on
	// when the request does not carry an explicit thinking switch.
	DefaultEnabled bool `json:"defaultEnabled,omitempty"`
}

type Sampling struct {
	// TopK enables the non-standard top_k extension used by compatible
	// OpenAI Chat providers such as the DeepSeek preset.
	TopK bool `json:"topK,omitempty"`
}

type ToolCalls struct {
	RequireAssistantContent bool `json:"requireAssistantContent,omitempty"`
	RequireReasoningContent bool `json:"requireReasoningContent,omitempty"`
}

type Streaming struct {
	// IncludeUsage asks OpenAI-compatible providers to emit a final SSE usage
	// chunk through stream_options.include_usage.
	IncludeUsage bool `json:"includeUsage,omitempty"`
}

type ReasoningEffort struct {
	Field    string            `json:"field,omitempty"`
	Values   []string          `json:"values,omitempty"`
	ValueMap map[string]string `json:"valueMap,omitempty"`
}

func Parse(raw string) (Provider, error) {
	if strings.TrimSpace(raw) == "" {
		return Provider{Protocol: ProtocolOpenAIChat}, nil
	}
	var cfg Provider
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Provider{}, err
	}
	if strings.TrimSpace(cfg.Protocol) == "" {
		return Provider{}, errors.New("capabilityConfig.protocol is required")
	}
	if !supportedProtocol(cfg.Protocol) {
		return Provider{}, fmt.Errorf("unsupported protocol %q", cfg.Protocol)
	}
	if !supportedThinkingMessageField(cfg.Thinking.RequestMessageField) {
		return Provider{}, fmt.Errorf("unsupported thinking.requestMessageField %q", cfg.Thinking.RequestMessageField)
	}
	if !supportedThinkingMessageField(cfg.Thinking.ResponseContentField) {
		return Provider{}, fmt.Errorf("unsupported thinking.responseContentField %q", cfg.Thinking.ResponseContentField)
	}
	return cfg, nil
}

func Validate(raw string) error {
	_, err := Parse(raw)
	return err
}

// ApplyModel overlays provider protocol capabilities with the optional
// providerCapabilityOverrides object stored in a model's capabilities JSON.
// The explicit allowThinkingDowngrade flag is model-scoped and defaults to
// false even when the provider supports disabling thinking.
func ApplyModel(base Provider, raw string) (Provider, error) {
	if strings.TrimSpace(raw) == "" {
		return base, nil
	}
	var model map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &model); err != nil {
		return Provider{}, fmt.Errorf("parse model capabilities: %w", err)
	}

	merged := base
	if overrideRaw, ok := model["providerCapabilityOverrides"]; ok {
		var override map[string]any
		if err := json.Unmarshal(overrideRaw, &override); err != nil {
			return Provider{}, errors.New("model providerCapabilityOverrides must be an object")
		}
		baseRaw, err := json.Marshal(base)
		if err != nil {
			return Provider{}, err
		}
		var combined map[string]any
		if err := json.Unmarshal(baseRaw, &combined); err != nil {
			return Provider{}, err
		}
		mergeObjects(combined, override)
		combinedRaw, err := json.Marshal(combined)
		if err != nil {
			return Provider{}, err
		}
		merged, err = Parse(string(combinedRaw))
		if err != nil {
			return Provider{}, fmt.Errorf("model providerCapabilityOverrides is invalid: %w", err)
		}
	}
	if downgradeRaw, ok := model["allowThinkingDowngrade"]; ok {
		if err := json.Unmarshal(downgradeRaw, &merged.AllowThinkingDowngrade); err != nil {
			return Provider{}, errors.New("model allowThinkingDowngrade must be a boolean")
		}
	}
	return merged, nil
}

func ValidateModel(raw string) error {
	_, err := ApplyModel(defaults["openai"], raw)
	return err
}

func mergeObjects(dst, src map[string]any) {
	for key, value := range src {
		srcObject, srcIsObject := value.(map[string]any)
		dstObject, dstIsObject := dst[key].(map[string]any)
		if srcIsObject && dstIsObject {
			mergeObjects(dstObject, srcObject)
			continue
		}
		dst[key] = value
	}
}

func DefaultJSON(providerType string) string {
	cfg, ok := defaults[strings.ToLower(strings.TrimSpace(providerType))]
	if !ok {
		cfg = defaults["openai"]
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return string(data)
}

func (p Provider) SupportsRequestField(field string) bool {
	return slices.Contains(p.Thinking.RequestFields, field)
}

func (p Provider) SupportsTopK() bool {
	return p.Sampling.TopK
}

func (p Provider) SupportsReasoningEffort(value string) bool {
	if p.ReasoningEffort.Field != "reasoning_effort" {
		return false
	}
	return len(p.ReasoningEffort.Values) == 0 || slices.Contains(p.ReasoningEffort.Values, value) || p.ReasoningEffort.ValueMap[value] != ""
}

func (p Provider) MapReasoningEffort(value string) string {
	if mapped := p.ReasoningEffort.ValueMap[value]; mapped != "" {
		return mapped
	}
	return value
}

func (p Provider) UnsupportedFieldError(field string) error {
	return fmt.Errorf("provider capability config does not support %s", field)
}

func supportedThinkingMessageField(field string) bool {
	return field == "" || field == ThinkingFieldReasoningContent
}

func supportedProtocol(protocol string) bool {
	switch protocol {
	case ProtocolOpenAIChat, ProtocolAnthropic, ProtocolGemini, ProtocolOpenAIResponse:
		return true
	default:
		return false
	}
}

var defaults = map[string]Provider{
	"openai": {
		Protocol: ProtocolOpenAIChat,
		ReasoningEffort: ReasoningEffort{
			Field:  "reasoning_effort",
			Values: []string{"none", "minimal", "low", "medium", "high", "xhigh"},
		},
	},
	"openai-compatible": {Protocol: ProtocolOpenAIChat},
	"volcengine-coding": {
		Protocol:  ProtocolOpenAIChat,
		Streaming: Streaming{IncludeUsage: true},
	},
	"anthropic":        {Protocol: ProtocolAnthropic},
	"gemini":           {Protocol: ProtocolGemini},
	"openai-responses": {Protocol: ProtocolOpenAIResponse},
	"deepseek": {
		Protocol: ProtocolOpenAIChat,
		Sampling: Sampling{TopK: true},
		Thinking: Thinking{
			RequestFields:        []string{"thinking"},
			RequestMessageField:  ThinkingFieldReasoningContent,
			ResponseContentField: ThinkingFieldReasoningContent,
			DefaultEnabled:       true,
		},
		ReasoningEffort: ReasoningEffort{
			Field:  "reasoning_effort",
			Values: []string{"high", "max"},
			ValueMap: map[string]string{
				"low":    "high",
				"medium": "high",
				"xhigh":  "max",
			},
		},
		ToolCalls: ToolCalls{RequireAssistantContent: true, RequireReasoningContent: true},
	},
	"siliconflow": {
		Protocol: ProtocolOpenAIChat,
		Thinking: Thinking{
			RequestFields:        []string{"enable_thinking", "thinking_budget"},
			ResponseContentField: ThinkingFieldReasoningContent,
		},
	},
}
