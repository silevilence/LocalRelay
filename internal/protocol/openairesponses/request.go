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
	Input           []InputItem     `json:"input"`
	Tools           []Tool          `json:"tools,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Reasoning       json.RawMessage `json:"reasoning,omitempty"`
	Text            json.RawMessage `json:"text,omitempty"`
}

type InputItem struct {
	Type      string             `json:"type,omitempty"`
	Role      string             `json:"role,omitempty"`
	Content   []InputContentPart `json:"content,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Output    string             `json:"output,omitempty"`
}

type InputContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
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
			item.Content = append(item.Content, InputContentPart{Type: "input_image", ImageURL: block.ImageURL})
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
