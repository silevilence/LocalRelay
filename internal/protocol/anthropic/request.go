package anthropic

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"localrelay/internal/ir"
)

type Request struct {
	Model         string          `json:"model"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	Messages      []Message       `json:"messages"`
	System        System          `json:"system,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Thinking      json.RawMessage `json:"thinking,omitempty"`
}

type Message struct {
	Role    string        `json:"role"`
	Content ContentBlocks `json:"content"`
}

// System and ContentBlocks accept the shorthand string forms accepted by the
// Anthropic API, while keeping the canonical structured form for output.
type System string

func (s *System) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = System(text)
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	var value strings.Builder
	for _, block := range blocks {
		if block.Type != "text" {
			return fmt.Errorf("unsupported Anthropic system block %q", block.Type)
		}
		value.WriteString(block.Text)
	}
	*s = System(value.String())
	return nil
}

type ContentBlocks []ContentBlock

func (blocks *ContentBlocks) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*blocks = ContentBlocks{{Type: "text", Text: text}}
		return nil
	}
	var value []ContentBlock
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*blocks = value
	return nil
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// ParseRequest converts an Anthropic Messages request into the gateway's
// protocol-neutral representation. Anthropic puts tool results in user
// messages; each result becomes an IR tool message so OpenAI-compatible
// upstreams can represent it without losing its tool_use_id.
func ParseRequest(data []byte) (ir.Request, error) {
	var in Request
	if err := json.Unmarshal(data, &in); err != nil {
		return ir.Request{}, err
	}
	return in.ToIR()
}

func (r Request) ToIR() (ir.Request, error) {
	out := ir.Request{
		Model:  r.Model,
		Stream: r.Stream,
		Params: ir.Params{
			MaxTokens:   r.MaxTokens,
			Temperature: r.Temperature,
			TopP:        r.TopP,
			Stop:        r.StopSequences,
			Thinking:    r.Thinking,
		},
	}
	if r.System != "" {
		out.Messages = append(out.Messages, ir.Message{Role: ir.RoleSystem, Content: []ir.ContentBlock{ir.Text(string(r.System))}})
	}
	for _, tool := range r.Tools {
		out.Tools = append(out.Tools, ir.Tool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: defaultRaw(tool.InputSchema, `{}`)})
	}
	for _, message := range r.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return ir.Request{}, fmt.Errorf("unsupported Anthropic role %q", message.Role)
		}
		var ordinary []ir.ContentBlock
		flushOrdinary := func() {
			if len(ordinary) > 0 {
				out.Messages = append(out.Messages, ir.Message{Role: ir.Role(message.Role), Content: ordinary})
				ordinary = nil
			}
		}
		for _, block := range message.Content {
			switch block.Type {
			case "text":
				ordinary = append(ordinary, ir.Text(block.Text))
			case "image":
				imageURL, err := imageURLFromSource(block.Source)
				if err != nil {
					return ir.Request{}, err
				}
				ordinary = append(ordinary, ir.Image(imageURL, ""))
			case "thinking":
				ordinary = append(ordinary, ir.Thinking(block.Thinking, block.Signature))
			case "tool_use":
				ordinary = append(ordinary, ir.ToolCall(block.ID, block.Name, defaultRaw(block.Input, `{}`)))
			case "tool_result":
				result, err := toolResultText(block.Content)
				if err != nil {
					return ir.Request{}, err
				}
				flushOrdinary()
				out.Messages = append(out.Messages, ir.Message{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult(block.ToolUseID, result)}})
			default:
				return ir.Request{}, fmt.Errorf("unsupported Anthropic content block %q", block.Type)
			}
		}
		flushOrdinary()
	}
	return out, nil
}

func imageURLFromSource(source *ImageSource) (string, error) {
	if source == nil {
		return "", fmt.Errorf("Anthropic image block requires a source")
	}
	switch source.Type {
	case "url":
		if strings.TrimSpace(source.URL) == "" {
			return "", fmt.Errorf("Anthropic URL image source requires a URL")
		}
		return source.URL, nil
	case "base64":
		if source.MediaType == "" || source.Data == "" {
			return "", fmt.Errorf("Anthropic base64 image source requires media_type and data")
		}
		if _, err := base64.StdEncoding.DecodeString(source.Data); err != nil {
			return "", err
		}
		return "data:" + source.MediaType + ";base64," + source.Data, nil
	default:
		return "", fmt.Errorf("unsupported Anthropic image source type %q", source.Type)
	}
}

func toolResultText(content any) (string, error) {
	switch value := content.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case []any:
		// IR tool results carry only a string. Anthropic also permits richer
		// result blocks (including is_error metadata), which cannot be
		// represented by the current IR; preserve their textual content.
		var text strings.Builder
		for _, raw := range value {
			block, ok := raw.(map[string]any)
			if !ok || block["type"] != "text" {
				return "", fmt.Errorf("unsupported Anthropic tool_result content")
			}
			part, ok := block["text"].(string)
			if !ok {
				return "", fmt.Errorf("Anthropic tool_result text content requires text")
			}
			text.WriteString(part)
		}
		return text.String(), nil
	default:
		return "", fmt.Errorf("unsupported Anthropic tool_result content %T", content)
	}
}

func ToProviderRequest(req ir.Request) (Request, error) {
	out := Request{
		Model:         req.Model,
		MaxTokens:     req.Params.MaxTokens,
		Stream:        req.Stream,
		Temperature:   req.Params.Temperature,
		TopP:          req.Params.TopP,
		StopSequences: req.Params.Stop,
		Thinking:      req.Params.Thinking,
	}
	for _, tool := range req.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return Request{}, fmt.Errorf("unsupported IR tool type %q", tool.Type)
		}
		out.Tools = append(out.Tools, Tool{Name: tool.Name, Description: tool.Description, InputSchema: defaultRaw(tool.Parameters, `{}`)})
	}
	for _, msg := range req.Messages {
		if msg.Role == ir.RoleSystem {
			out.System += System(textOnly(msg.Content))
			continue
		}
		role, err := anthropicRole(msg.Role)
		if err != nil {
			return Request{}, err
		}
		blocks, err := requestContentFromIR(msg.Content)
		if err != nil {
			return Request{}, err
		}
		out.Messages = append(out.Messages, Message{Role: role, Content: blocks})
	}
	return out, nil
}

func ParseResponse(data []byte) (ir.Response, error) {
	var in Response
	if err := json.Unmarshal(data, &in); err != nil {
		return ir.Response{}, err
	}
	return in.ToIR()
}

func (r Response) ToIR() (ir.Response, error) {
	msg := ir.Message{Role: ir.RoleAssistant}
	for _, block := range r.Content {
		switch block.Type {
		case "text":
			msg.Content = append(msg.Content, ir.Text(block.Text))
		case "thinking":
			msg.Content = append(msg.Content, ir.Thinking(block.Thinking, block.Signature))
		case "tool_use":
			msg.Content = append(msg.Content, ir.ToolCall(block.ID, block.Name, defaultRaw(block.Input, `{}`)))
		default:
			return ir.Response{}, fmt.Errorf("unsupported Anthropic content block %q", block.Type)
		}
	}
	return ir.Response{
		ID:    r.ID,
		Model: r.Model,
		Choices: []ir.Choice{{
			Index:      0,
			Message:    msg,
			StopReason: openAIStop(r.StopReason),
		}},
		Usage: usageToIR(r.Usage),
	}, nil
}

func ParseStream(r io.Reader) ([]ir.StreamEvent, error) {
	var events []ir.StreamEvent
	err := ForEachStreamEvent(r, func(event ir.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

type StreamDecoder struct {
	blocks  map[int]ir.BlockType
	ignored map[int]bool
}

func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{
		blocks:  map[int]ir.BlockType{},
		ignored: map[int]bool{},
	}
}

func ForEachStreamEvent(r io.Reader, yield func(ir.StreamEvent) error) error {
	decoder := NewStreamDecoder()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 20<<20)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		events, err := decoder.DecodePayload([]byte(payload))
		if err != nil {
			if yieldErr := yield(ir.StreamEvent{Type: ir.StreamError, Error: err.Error()}); yieldErr != nil {
				return yieldErr
			}
			return err
		}
		for _, event := range events {
			if err := yield(event); err != nil {
				return err
			}
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		if yieldErr := yield(ir.StreamEvent{Type: ir.StreamError, Error: err.Error()}); yieldErr != nil {
			return yieldErr
		}
		return err
	}
	return flush()
}

func decodeStreamPayload(data []byte) ([]ir.StreamEvent, error) {
	return NewStreamDecoder().DecodePayload(data)
}

func (d *StreamDecoder) DecodePayload(data []byte) ([]ir.StreamEvent, error) {
	var raw struct {
		Type         string          `json:"type"`
		Index        int             `json:"index"`
		Message      Response        `json:"message"`
		ContentBlock ContentBlock    `json:"content_block"`
		Delta        json.RawMessage `json:"delta"`
		Usage        *Usage          `json:"usage"`
		Error        struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	switch raw.Type {
	case "message_start":
		return []ir.StreamEvent{{Type: ir.StreamMessageStart, ID: raw.Message.ID, Model: raw.Message.Model}, {Type: ir.StreamChoiceStart, Role: ir.RoleAssistant}}, nil
	case "content_block_start":
		event := ir.StreamEvent{Type: ir.StreamContentBlockStart, BlockIndex: raw.Index}
		switch raw.ContentBlock.Type {
		case "text":
			event.BlockType = ir.BlockText
		case "thinking":
			event.BlockType = ir.BlockThinking
		case "tool_use":
			event.BlockType = ir.BlockToolCall
			event.ToolCallID = raw.ContentBlock.ID
			event.ToolName = raw.ContentBlock.Name
		default:
			d.ignored[raw.Index] = true
			return nil, nil
		}
		d.blocks[raw.Index] = event.BlockType
		delete(d.ignored, raw.Index)
		return []ir.StreamEvent{event}, nil
	case "content_block_delta":
		if d.ignored[raw.Index] {
			return nil, nil
		}
		var delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		}
		if err := json.Unmarshal(raw.Delta, &delta); err != nil {
			return nil, err
		}
		event := ir.StreamEvent{Type: ir.StreamContentBlockDelta, BlockIndex: raw.Index}
		switch delta.Type {
		case "text_delta":
			event.BlockType = ir.BlockText
			event.Delta = delta.Text
		case "thinking_delta":
			event.BlockType = ir.BlockThinking
			event.Delta = delta.Thinking
		case "input_json_delta":
			if d.blocks[raw.Index] != ir.BlockToolCall {
				return nil, nil
			}
			event.BlockType = ir.BlockToolCall
			event.ArgumentsDelta = delta.PartialJSON
		case "signature_delta", "citations_delta":
			return nil, nil
		default:
			return nil, nil
		}
		return []ir.StreamEvent{event}, nil
	case "content_block_stop":
		if d.ignored[raw.Index] {
			delete(d.ignored, raw.Index)
			return nil, nil
		}
		delete(d.blocks, raw.Index)
		return []ir.StreamEvent{{Type: ir.StreamContentBlockStop, BlockIndex: raw.Index}}, nil
	case "message_delta":
		var delta struct {
			StopReason string `json:"stop_reason"`
		}
		_ = json.Unmarshal(raw.Delta, &delta)
		return []ir.StreamEvent{{Type: ir.StreamMessageDelta, StopReason: openAIStop(delta.StopReason), Usage: usageToIR(raw.Usage)}}, nil
	case "message_stop":
		return []ir.StreamEvent{{Type: ir.StreamMessageStop}}, nil
	case "ping":
		return nil, nil
	case "error":
		return []ir.StreamEvent{{Type: ir.StreamError, Error: raw.Error.Message}}, nil
	default:
		return nil, nil
	}
}

func requestContentFromIR(blocks []ir.ContentBlock) ([]ContentBlock, error) {
	out := make([]ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case ir.BlockText:
			out = append(out, ContentBlock{Type: "text", Text: block.Text})
		case ir.BlockImage:
			image, err := anthropicImage(block.ImageURL)
			if err != nil {
				return nil, err
			}
			out = append(out, ContentBlock{Type: "image", Source: &image})
		case ir.BlockThinking:
			out = append(out, ContentBlock{Type: "thinking", Thinking: block.Text, Signature: block.Signature})
		case ir.BlockToolCall:
			out = append(out, ContentBlock{Type: "tool_use", ID: block.ToolCallID, Name: block.ToolName, Input: defaultRaw(block.Arguments, `{}`)})
		case ir.BlockToolResult:
			out = append(out, ContentBlock{Type: "tool_result", ToolUseID: block.ToolCallID, Content: block.Result})
		default:
			return nil, fmt.Errorf("Anthropic Messages request cannot represent IR block %q", block.Type)
		}
	}
	return out, nil
}

func anthropicImage(url string) (ImageSource, error) {
	if media, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ";base64,"); ok && media != "" {
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return ImageSource{}, err
		}
		return ImageSource{Type: "base64", MediaType: media, Data: data}, nil
	}
	return ImageSource{Type: "url", URL: url}, nil
}

func anthropicRole(role ir.Role) (string, error) {
	switch role {
	case ir.RoleUser, ir.RoleTool:
		return "user", nil
	case ir.RoleAssistant:
		return "assistant", nil
	default:
		return "", fmt.Errorf("unsupported role %q", role)
	}
}

func textOnly(blocks []ir.ContentBlock) string {
	var out string
	for _, block := range blocks {
		if block.Type == ir.BlockText {
			out += block.Text
		}
	}
	return out
}

func usageToIR(in *Usage) ir.Usage {
	if in == nil {
		return ir.Usage{}
	}
	return ir.Usage{
		InputTokens:              in.InputTokens,
		OutputTokens:             in.OutputTokens,
		CacheCreationInputTokens: in.CacheCreationInputTokens,
		CacheReadInputTokens:     in.CacheReadInputTokens,
	}
}

func openAIStop(stop string) string {
	switch stop {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return stop
	}
}

func defaultRaw(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}
