package openairesponses

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"localrelay/internal/ir"
)

type Request struct {
	Model           string          `json:"model"`
	Input           Input           `json:"input"`
	Instructions    string          `json:"instructions,omitempty"`
	Tools           []Tool          `json:"tools,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Reasoning       json.RawMessage `json:"reasoning,omitempty"`
	Text            json.RawMessage `json:"text,omitempty"`
}

// Input accepts both forms supported by the Responses API: a convenience
// string and a structured sequence of input items.
type Input []InputItem

func (in *Input) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*in = Input{{Type: "message", Role: string(ir.RoleUser), Content: []InputContentPart{{Type: "input_text", Text: text}}}}
		return nil
	}
	var items []InputItem
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	*in = items
	return nil
}

type InputItem struct {
	Type      string       `json:"type,omitempty"`
	Role      string       `json:"role,omitempty"`
	Content   InputContent `json:"content,omitempty"`
	CallID    string       `json:"call_id,omitempty"`
	Name      string       `json:"name,omitempty"`
	Arguments string       `json:"arguments,omitempty"`
	Output    string       `json:"output,omitempty"`
}

type InputContent []InputContentPart

func (content *InputContent) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*content = InputContent{{Type: "input_text", Text: text}}
		return nil
	}
	var parts []InputContentPart
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	*content = parts
	return nil
}

type InputContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ParseRequest converts an OpenAI Responses request into IR. Developer
// messages are deliberately represented as system messages because IR has no
// separate developer role; this is the closest lossless routing semantics.
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
			MaxTokens:      r.MaxOutputTokens,
			Temperature:    r.Temperature,
			TopP:           r.TopP,
			Thinking:       r.Reasoning,
			ResponseFormat: r.Text,
		},
	}
	if r.Instructions != "" {
		out.Messages = append(out.Messages, ir.Message{Role: ir.RoleSystem, Content: []ir.ContentBlock{ir.Text(r.Instructions)}})
	}
	for _, tool := range r.Tools {
		if tool.Type != "function" {
			return ir.Request{}, fmt.Errorf("unsupported OpenAI Responses tool type %q", tool.Type)
		}
		out.Tools = append(out.Tools, ir.Tool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: objectOrEmpty(tool.Parameters)})
	}
	for _, item := range r.Input {
		switch item.Type {
		case "", "message":
			role, err := responseInputRole(item.Role)
			if err != nil {
				return ir.Request{}, err
			}
			blocks, err := responseInputBlocks(item.Content)
			if err != nil {
				return ir.Request{}, err
			}
			out.Messages = append(out.Messages, ir.Message{Role: role, Content: blocks})
		case "function_call":
			arguments := json.RawMessage(item.Arguments)
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			if !json.Valid(arguments) {
				return ir.Request{}, fmt.Errorf("OpenAI Responses function_call arguments must contain JSON")
			}
			out.Messages = append(out.Messages, ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.ToolCall(item.CallID, item.Name, arguments)}})
		case "function_call_output":
			out.Messages = append(out.Messages, ir.Message{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult(item.CallID, item.Output)}})
		default:
			return ir.Request{}, fmt.Errorf("unsupported OpenAI Responses input item type %q", item.Type)
		}
	}
	return out, nil
}

func responseInputRole(role string) (ir.Role, error) {
	switch role {
	case "system", "developer":
		// IR intentionally has no developer role; system is its closest
		// available control-message representation.
		return ir.RoleSystem, nil
	case "", "user":
		return ir.RoleUser, nil
	case "assistant":
		return ir.RoleAssistant, nil
	default:
		return "", fmt.Errorf("unsupported OpenAI Responses input role %q", role)
	}
}

func responseInputBlocks(parts InputContent) ([]ir.ContentBlock, error) {
	blocks := make([]ir.ContentBlock, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text":
			blocks = append(blocks, ir.Text(part.Text))
		case "input_image":
			if part.ImageURL == "" {
				return nil, fmt.Errorf("OpenAI Responses input_image requires image_url")
			}
			blocks = append(blocks, ir.Image(part.ImageURL, part.Detail))
		default:
			return nil, fmt.Errorf("unsupported OpenAI Responses content part %q", part.Type)
		}
	}
	return blocks, nil
}

func ToProviderRequest(req ir.Request) (Request, error) {
	out := Request{
		Model:           req.Model,
		Stream:          req.Stream,
		MaxOutputTokens: req.Params.MaxTokens,
		Temperature:     req.Params.Temperature,
		TopP:            req.Params.TopP,
		Reasoning:       req.Params.Thinking,
		Text:            req.Params.ResponseFormat,
	}
	for _, tool := range req.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return Request{}, fmt.Errorf("unsupported IR tool type %q", tool.Type)
		}
		out.Tools = append(out.Tools, Tool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: objectOrEmpty(tool.Parameters)})
	}
	for _, msg := range req.Messages {
		items, err := inputItemsFromIR(msg)
		if err != nil {
			return Request{}, err
		}
		out.Input = append(out.Input, items...)
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
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					msg.Content = append(msg.Content, ir.Text(part.Text))
				case "reasoning_text":
					msg.Content = append(msg.Content, ir.Thinking(part.Text, ""))
				}
			}
		case "function_call":
			msg.Content = append(msg.Content, ir.ToolCall(item.CallID, item.Name, json.RawMessage(item.Arguments)))
		case "reasoning":
			for _, part := range item.Content {
				msg.Content = append(msg.Content, ir.Thinking(part.Text, ""))
			}
		}
	}
	return ir.Response{
		ID:    r.ID,
		Model: r.Model,
		Choices: []ir.Choice{{
			Index:      0,
			Message:    msg,
			StopReason: "stop",
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

func ForEachStreamEvent(r io.Reader, yield func(ir.StreamEvent) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 20<<20)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		events, err := decodeStreamPayload([]byte(payload))
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
	var raw struct {
		Type         string          `json:"type"`
		Response     Response        `json:"response"`
		OutputIndex  int             `json:"output_index"`
		ContentIndex int             `json:"content_index"`
		Item         OutputItem      `json:"item"`
		Delta        string          `json:"delta"`
		Error        json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	switch raw.Type {
	case "response.created":
		return []ir.StreamEvent{{Type: ir.StreamMessageStart, ID: raw.Response.ID, Model: raw.Response.Model}}, nil
	case "response.output_item.added":
		if raw.Item.Type == "message" {
			return []ir.StreamEvent{{Type: ir.StreamChoiceStart, ChoiceIndex: raw.OutputIndex, Role: ir.Role(raw.Item.Role)}}, nil
		}
		if raw.Item.Type == "function_call" {
			return []ir.StreamEvent{{Type: ir.StreamContentBlockStart, BlockIndex: raw.OutputIndex, BlockType: ir.BlockToolCall, ToolCallID: raw.Item.CallID, ToolName: raw.Item.Name}}, nil
		}
	case "response.content_part.added":
		return nil, nil
	case "response.output_text.delta":
		return []ir.StreamEvent{{Type: ir.StreamContentBlockDelta, ChoiceIndex: raw.OutputIndex, BlockIndex: raw.ContentIndex, BlockType: ir.BlockText, Delta: raw.Delta}}, nil
	case "response.reasoning_text.delta":
		return []ir.StreamEvent{{Type: ir.StreamContentBlockDelta, ChoiceIndex: raw.OutputIndex, BlockIndex: raw.ContentIndex, BlockType: ir.BlockThinking, Delta: raw.Delta}}, nil
	case "response.function_call_arguments.delta":
		return []ir.StreamEvent{{Type: ir.StreamContentBlockDelta, BlockIndex: raw.OutputIndex, BlockType: ir.BlockToolCall, ArgumentsDelta: raw.Delta}}, nil
	case "response.output_text.done", "response.reasoning_text.done":
		return []ir.StreamEvent{{Type: ir.StreamContentBlockStop, ChoiceIndex: raw.OutputIndex, BlockIndex: raw.ContentIndex}}, nil
	case "response.function_call_arguments.done":
		return []ir.StreamEvent{{Type: ir.StreamContentBlockStop, BlockIndex: raw.OutputIndex}}, nil
	case "response.completed":
		return []ir.StreamEvent{{Type: ir.StreamMessageDelta, Usage: usageToIR(raw.Response.Usage)}, {Type: ir.StreamMessageStop}}, nil
	case "error":
		return []ir.StreamEvent{{Type: ir.StreamError, Error: string(raw.Error)}}, nil
	}
	return nil, nil
}

func inputItemsFromIR(msg ir.Message) ([]InputItem, error) {
	item := InputItem{Type: "message", Role: string(msg.Role)}
	for _, block := range msg.Content {
		switch block.Type {
		case ir.BlockText:
			item.Content = append(item.Content, InputContentPart{Type: "input_text", Text: block.Text})
		case ir.BlockImage:
			item.Content = append(item.Content, InputContentPart{Type: "input_image", ImageURL: block.ImageURL, Detail: block.Detail})
		case ir.BlockThinking:
			continue
		case ir.BlockToolCall:
			args := string(objectOrEmpty(block.Arguments))
			return []InputItem{item, InputItem{Type: "function_call", CallID: block.ToolCallID, Name: block.ToolName, Arguments: args}}, nil
		case ir.BlockToolResult:
			return []InputItem{{Type: "function_call_output", CallID: block.ToolCallID, Output: block.Result}}, nil
		default:
			return nil, fmt.Errorf("OpenAI Responses request cannot represent IR block %q", block.Type)
		}
	}
	return []InputItem{item}, nil
}

func usageToIR(in *Usage) ir.Usage {
	if in == nil {
		return ir.Usage{}
	}
	out := ir.Usage{InputTokens: in.InputTokens, OutputTokens: in.OutputTokens}
	if in.InputTokensDetails != nil {
		out.CacheReadInputTokens = in.InputTokensDetails.CachedTokens
	}
	return out
}

func objectOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
