package ir

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockImage      BlockType = "image"
	BlockToolCall   BlockType = "tool_call"
	BlockToolResult BlockType = "tool_result"
	BlockThinking   BlockType = "thinking"
)

type Request struct {
	Model    string    `json:"model"`
	Stream   bool      `json:"stream,omitempty"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Params   Params    `json:"params,omitempty"`
}

type Response struct {
	ID      string   `json:"id,omitempty"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage,omitempty"`
}

type Choice struct {
	Index      int     `json:"index"`
	Message    Message `json:"message"`
	StopReason string  `json:"stopReason,omitempty"`
}

type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content,omitempty"`
}

type ContentBlock struct {
	Type BlockType `json:"type"`

	Text string `json:"text,omitempty"`

	// ImageURL keeps remote or data URLs as-is. Detail mirrors OpenAI Chat's
	// image_url.detail hint when present.
	ImageURL string `json:"imageUrl,omitempty"`
	Detail   string `json:"detail,omitempty"`

	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Result     string          `json:"result,omitempty"`

	// Signature is optional provider metadata for thinking/reasoning blocks.
	Signature string `json:"signature,omitempty"`
}

type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type Params struct {
	MaxTokens   *int     `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
	// TopK is an integer in the Anthropic and Gemini APIs.
	TopK             *int            `json:"topK,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	ReasoningEffort  *string         `json:"reasoningEffort,omitempty"`
	Thinking         json.RawMessage `json:"thinking,omitempty"`
	EnableThinking   *bool           `json:"enableThinking,omitempty"`
	ThinkingBudget   *int            `json:"thinkingBudget,omitempty"`
	PresencePenalty  *float64        `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequencyPenalty,omitempty"`
	ResponseFormat   json.RawMessage `json:"responseFormat,omitempty"`
}

type Usage struct {
	InputTokens              int `json:"inputTokens,omitempty"`
	OutputTokens             int `json:"outputTokens,omitempty"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens,omitempty"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens,omitempty"`
}

func Text(text string) ContentBlock {
	return ContentBlock{Type: BlockText, Text: text}
}

func Image(url, detail string) ContentBlock {
	return ContentBlock{Type: BlockImage, ImageURL: url, Detail: detail}
}

func ToolCall(id, name string, arguments json.RawMessage) ContentBlock {
	return ContentBlock{Type: BlockToolCall, ToolCallID: id, ToolName: name, Arguments: arguments}
}

func ToolResult(id, result string) ContentBlock {
	return ContentBlock{Type: BlockToolResult, ToolCallID: id, Result: result}
}

func Thinking(text, signature string) ContentBlock {
	return ContentBlock{Type: BlockThinking, Text: text, Signature: signature}
}
