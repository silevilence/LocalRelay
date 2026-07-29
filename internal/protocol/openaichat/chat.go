package openaichat

import (
	"encoding/json"
	"errors"
	"fmt"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
)

type Request struct {
	Model            string          `json:"model"`
	Messages         []Message       `json:"messages"`
	Tools            []Tool          `json:"tools,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	Stop             any             `json:"stop,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	ResponseFormat   json.RawMessage `json:"response_format,omitempty"`
	ReasoningEffort  *string         `json:"reasoning_effort,omitempty"`
	Thinking         json.RawMessage `json:"thinking,omitempty"`
	EnableThinking   *bool           `json:"enable_thinking,omitempty"`
	ThinkingBudget   *int            `json:"thinking_budget,omitempty"`
	StreamOptions    *StreamOptions  `json:"stream_options,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type Message struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function FunctionTool `json:"function"`
}

type FunctionTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type Response struct {
	ID      string           `json:"id,omitempty"`
	Object  string           `json:"object,omitempty"`
	Created int64            `json:"created,omitempty"`
	Model   string           `json:"model"`
	Choices []ResponseChoice `json:"choices"`
	Usage   *Usage           `json:"usage,omitempty"`
}

type ResponseChoice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

type Usage struct {
	PromptTokens        int                 `json:"prompt_tokens,omitempty"`
	CompletionTokens    int                 `json:"completion_tokens,omitempty"`
	TotalTokens         int                 `json:"total_tokens,omitempty"`
	PromptCacheHit      int                 `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMiss     int                 `json:"prompt_cache_miss_tokens,omitempty"`
	PromptTokensDetails *PromptTokenDetails `json:"prompt_tokens_details,omitempty"`
}

type PromptTokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

func Parse(data []byte) (ir.Request, error) {
	var in Request
	if err := json.Unmarshal(data, &in); err != nil {
		return ir.Request{}, err
	}
	return in.ToIR()
}

func ParseResponse(data []byte) (ir.Response, error) {
	return ParseResponseWithCapabilities(data, capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat})
}

func ParseResponseWithCapabilities(data []byte, cfg capabilities.Provider) (ir.Response, error) {
	var in Response
	if err := json.Unmarshal(data, &in); err != nil {
		return ir.Response{}, err
	}
	return in.ToIRWithCapabilities(cfg)
}

func (r Request) ToIR() (ir.Request, error) {
	return r.ToIRWithCapabilities(capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat})
}

func (r Request) ToIRWithCapabilities(cfg capabilities.Provider) (ir.Request, error) {
	out := ir.Request{
		Model:  r.Model,
		Stream: r.Stream,
		Params: ir.Params{
			MaxTokens:        r.MaxTokens,
			Temperature:      r.Temperature,
			TopP:             r.TopP,
			PresencePenalty:  r.PresencePenalty,
			FrequencyPenalty: r.FrequencyPenalty,
			ResponseFormat:   r.ResponseFormat,
			ReasoningEffort:  r.ReasoningEffort,
			Thinking:         r.Thinking,
			EnableThinking:   r.EnableThinking,
			ThinkingBudget:   r.ThinkingBudget,
		},
	}
	out.Params.Stop = stopStrings(r.Stop)

	for _, tool := range r.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return ir.Request{}, fmt.Errorf("unsupported tool type %q", tool.Type)
		}
		out.Tools = append(out.Tools, ir.Tool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}

	for _, msg := range r.Messages {
		converted, err := msg.toIRWithCapabilities(cfg)
		if err != nil {
			return ir.Request{}, err
		}
		out.Messages = append(out.Messages, converted)
	}
	return out, nil
}

func (r Response) ToIR() (ir.Response, error) {
	return r.ToIRWithCapabilities(capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat})
}

func (r Response) ToIRWithCapabilities(cfg capabilities.Provider) (ir.Response, error) {
	out := ir.Response{ID: r.ID, Model: r.Model}
	if r.Usage != nil {
		inputTokens := r.Usage.PromptTokens
		if inputTokens == 0 {
			inputTokens = r.Usage.PromptCacheHit + r.Usage.PromptCacheMiss
		}
		out.Usage = ir.Usage{
			InputTokens:  inputTokens,
			OutputTokens: r.Usage.CompletionTokens,
		}
		if r.Usage.PromptCacheHit > 0 {
			out.Usage.CacheReadInputTokens = r.Usage.PromptCacheHit
		}
		if r.Usage.PromptTokensDetails != nil && r.Usage.PromptTokensDetails.CachedTokens > 0 {
			out.Usage.CacheReadInputTokens = r.Usage.PromptTokensDetails.CachedTokens
		}
	}
	for _, choice := range r.Choices {
		msg, err := choice.Message.toIRWithCapabilities(cfg)
		if err != nil {
			return ir.Response{}, err
		}
		out.Choices = append(out.Choices, ir.Choice{
			Index:      choice.Index,
			Message:    msg,
			StopReason: choice.FinishReason,
		})
	}
	return out, nil
}

func FromIRResponse(resp ir.Response) (Response, error) {
	return FromIRResponseWithCapabilities(resp, capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat})
}

func FromIRResponseWithCapabilities(resp ir.Response, cfg capabilities.Provider) (Response, error) {
	out := Response{
		ID:      resp.ID,
		Object:  "chat.completion",
		Model:   resp.Model,
		Choices: make([]ResponseChoice, 0, len(resp.Choices)),
	}
	if resp.Usage != (ir.Usage{}) {
		out.Usage = &Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
		if resp.Usage.CacheReadInputTokens > 0 {
			out.Usage.PromptTokensDetails = &PromptTokenDetails{CachedTokens: resp.Usage.CacheReadInputTokens}
		}
	}
	for _, choice := range resp.Choices {
		msg, err := messageFromIRForField(choice.Message, cfg, cfg.Thinking.ResponseContentField)
		if err != nil {
			return Response{}, err
		}
		out.Choices = append(out.Choices, ResponseChoice{
			Index:        choice.Index,
			Message:      msg,
			FinishReason: choice.StopReason,
		})
	}
	return out, nil
}

func (m Message) toIR() (ir.Message, error) {
	return m.toIRWithCapabilities(capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat})
}

func (m Message) toIRWithCapabilities(cfg capabilities.Provider) (ir.Message, error) {
	if err := validateRole(m.Role); err != nil {
		return ir.Message{}, err
	}
	out := ir.Message{Role: ir.Role(m.Role)}
	if m.Role == string(ir.RoleTool) {
		text, err := contentText(m.Content)
		if err != nil {
			return ir.Message{}, err
		}
		out.Content = append(out.Content, ir.ToolResult(m.ToolCallID, text))
		return out, nil
	}

	blocks, err := contentBlocks(m.Content)
	if err != nil {
		return ir.Message{}, err
	}
	out.Content = append(out.Content, blocks...)
	if cfg.Thinking.ResponseContentField == capabilities.ThinkingFieldReasoningContent && m.ReasoningContent != "" {
		out.Content = append(out.Content, ir.Thinking(m.ReasoningContent, ""))
	}
	for _, call := range m.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return ir.Message{}, fmt.Errorf("unsupported tool call type %q", call.Type)
		}
		arguments, err := rawArguments(call.Function.Arguments)
		if err != nil {
			return ir.Message{}, err
		}
		out.Content = append(out.Content, ir.ToolCall(call.ID, call.Function.Name, arguments))
	}
	return out, nil
}

func ToProviderRequest(req ir.Request, cfg capabilities.Provider) (Request, error) {
	if cfg.Protocol != capabilities.ProtocolOpenAIChat {
		return Request{}, fmt.Errorf("unsupported provider protocol %q", cfg.Protocol)
	}

	out := Request{
		Model:            req.Model,
		Stream:           req.Stream,
		MaxTokens:        req.Params.MaxTokens,
		Temperature:      req.Params.Temperature,
		TopP:             req.Params.TopP,
		PresencePenalty:  req.Params.PresencePenalty,
		FrequencyPenalty: req.Params.FrequencyPenalty,
		ResponseFormat:   req.Params.ResponseFormat,
	}
	if err := applyConfiguredFields(&out, req, cfg); err != nil {
		return Request{}, err
	}
	stops := stopStrings(req.Params.Stop)
	if len(stops) == 1 {
		out.Stop = stops[0]
	} else if len(stops) > 1 {
		out.Stop = stops
	}

	for _, tool := range req.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return Request{}, fmt.Errorf("unsupported IR tool type %q", tool.Type)
		}
		out.Tools = append(out.Tools, Tool{
			Type: "function",
			Function: FunctionTool{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}

	for _, msg := range req.Messages {
		converted, err := messageFromIRForField(msg, cfg, cfg.Thinking.RequestMessageField)
		if err != nil {
			return Request{}, err
		}
		out.Messages = append(out.Messages, converted)
	}
	return out, nil
}

func applyConfiguredFields(out *Request, req ir.Request, cfg capabilities.Provider) error {
	if req.Stream && cfg.Streaming.IncludeUsage {
		out.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	if req.Params.ReasoningEffort != nil {
		if !cfg.SupportsReasoningEffort(*req.Params.ReasoningEffort) {
			return cfg.UnsupportedFieldError("reasoning_effort")
		}
		mapped := cfg.MapReasoningEffort(*req.Params.ReasoningEffort)
		out.ReasoningEffort = &mapped
	}
	if len(req.Params.Thinking) > 0 {
		if !cfg.SupportsRequestField("thinking") {
			return cfg.UnsupportedFieldError("thinking")
		}
		out.Thinking = req.Params.Thinking
	}
	if req.Params.EnableThinking != nil {
		if !cfg.SupportsRequestField("enable_thinking") {
			return cfg.UnsupportedFieldError("enable_thinking")
		}
		out.EnableThinking = req.Params.EnableThinking
	}
	if req.Params.ThinkingBudget != nil {
		if !cfg.SupportsRequestField("thinking_budget") {
			return cfg.UnsupportedFieldError("thinking_budget")
		}
		out.ThinkingBudget = req.Params.ThinkingBudget
	}
	return nil
}

func messageFromIR(msg ir.Message) (Message, error) {
	return messageFromIRForField(msg, capabilities.Provider{Protocol: capabilities.ProtocolOpenAIChat}, "")
}

func messageFromIRForField(msg ir.Message, cfg capabilities.Provider, thinkingField string) (Message, error) {
	if err := validateRole(string(msg.Role)); err != nil {
		return Message{}, err
	}
	out := Message{Role: string(msg.Role)}
	var parts []ContentPart
	var text string
	var toolResults int

	for _, block := range msg.Content {
		switch block.Type {
		case ir.BlockText:
			text += block.Text
			parts = append(parts, ContentPart{Type: "text", Text: block.Text})
		case ir.BlockImage:
			parts = append(parts, ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: block.ImageURL, Detail: block.Detail}})
		case ir.BlockToolCall:
			args, err := argumentString(block.Arguments)
			if err != nil {
				return Message{}, err
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:   block.ToolCallID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.ToolName,
					Arguments: args,
				},
			})
		case ir.BlockToolResult:
			if msg.Role != ir.RoleTool {
				return Message{}, errors.New("tool_result blocks require tool role")
			}
			toolResults++
			if toolResults > 1 {
				return Message{}, errors.New("OpenAI Chat tool messages support exactly one tool_result block")
			}
			out.ToolCallID = block.ToolCallID
			text += block.Result
		case ir.BlockThinking:
			if msg.Role == ir.RoleAssistant && thinkingField == capabilities.ThinkingFieldReasoningContent {
				out.ReasoningContent += block.Text
			}
		default:
			return Message{}, fmt.Errorf("unsupported IR block type %q", block.Type)
		}
	}

	if msg.Role == ir.RoleTool {
		if toolResults != 1 {
			return Message{}, errors.New("OpenAI Chat tool messages require one tool_result block")
		}
		out.Content = text
		return out, nil
	}
	if len(out.ToolCalls) > 0 {
		// OpenAI-compatible assistant messages with tool_calls only support
		// null/string content. Any image parts in the IR are dropped here.
		if text != "" {
			out.Content = text
		} else if cfg.ToolCalls.RequireAssistantContent {
			out.Content = ""
		}
		return out, nil
	}
	if len(parts) == 0 {
		out.Content = nil
		return out, nil
	}
	if len(parts) == 1 && parts[0].Type == "text" {
		out.Content = parts[0].Text
		return out, nil
	}
	out.Content = parts
	return out, nil
}

func contentBlocks(content any) ([]ir.ContentBlock, error) {
	switch v := content.(type) {
	case nil:
		return nil, nil
	case string:
		if v == "" {
			return nil, nil
		}
		return []ir.ContentBlock{ir.Text(v)}, nil
	case []any:
		var out []ir.ContentBlock
		for _, raw := range v {
			part, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("content part must be an object")
			}
			switch part["type"] {
			case "text":
				out = append(out, ir.Text(stringValue(part["text"])))
			case "image_url":
				image, ok := part["image_url"].(map[string]any)
				if !ok {
					return nil, errors.New("image_url content part must contain image_url object")
				}
				out = append(out, ir.Image(stringValue(image["url"]), stringValue(image["detail"])))
			default:
				return nil, fmt.Errorf("unsupported content part type %q", part["type"])
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported content shape %T", content)
	}
}

func contentText(content any) (string, error) {
	blocks, err := contentBlocks(content)
	if err != nil {
		return "", err
	}
	var out string
	for _, block := range blocks {
		if block.Type == ir.BlockText {
			out += block.Text
		}
	}
	return out, nil
}

func stopStrings(stop any) []string {
	switch v := stop.(type) {
	case nil:
		return nil
	case string:
		return appendStop(nil, v)
	case []any:
		var out []string
		for _, item := range v {
			out = appendStop(out, stringValue(item))
		}
		return out
	case []string:
		var out []string
		for _, item := range v {
			out = appendStop(out, item)
		}
		return out
	default:
		return nil
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func rawArguments(v any) (json.RawMessage, error) {
	switch args := v.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case string:
		if args == "" {
			return json.RawMessage(`{}`), nil
		}
		if !json.Valid([]byte(args)) {
			return nil, errors.New("function arguments string must contain JSON")
		}
		return json.RawMessage(args), nil
	default:
		data, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
}

func argumentString(raw json.RawMessage) (string, error) {
	raw = defaultRaw(raw, `{}`)
	if !json.Valid(raw) {
		return "", errors.New("IR tool_call arguments must contain JSON")
	}
	return string(raw), nil
}

func defaultRaw(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}

func validateRole(role string) error {
	switch ir.Role(role) {
	case ir.RoleSystem, ir.RoleUser, ir.RoleAssistant, ir.RoleTool:
		return nil
	default:
		return fmt.Errorf("unsupported role %q", role)
	}
}

func appendStop(out []string, stop string) []string {
	if stop == "" {
		return out
	}
	return append(out, stop)
}
