package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"localrelay/internal/ir"
)

type Response struct {
	ID           string         `json:"id,omitempty"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   string         `json:"stop_reason,omitempty"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        *Usage         `json:"usage,omitempty"`
}

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Source    *ImageSource    `json:"source,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
}

type Usage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

func FromIRResponse(resp ir.Response) (Response, error) {
	out := Response{
		ID:           resp.ID,
		Type:         "message",
		Role:         string(ir.RoleAssistant),
		Model:        resp.Model,
		StopSequence: nil,
		Usage:        usageFromIR(resp.Usage),
	}
	if len(resp.Choices) == 0 {
		return out, nil
	}
	if len(resp.Choices) > 1 {
		// Anthropic Messages has a single assistant message, not choices.
		return Response{}, errors.New("Anthropic Messages cannot represent multiple IR choices")
	}
	choice := resp.Choices[0]
	out.StopReason = anthropicStop(choice.StopReason)
	blocks, err := contentFromIR(choice.Message.Content)
	if err != nil {
		return Response{}, err
	}
	out.Content = blocks
	return out, nil
}

func contentFromIR(blocks []ir.ContentBlock) ([]ContentBlock, error) {
	out := make([]ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case ir.BlockText:
			out = append(out, ContentBlock{Type: "text", Text: block.Text})
		case ir.BlockThinking:
			out = append(out, ContentBlock{Type: "thinking", Thinking: block.Text, Signature: block.Signature})
		case ir.BlockToolCall:
			args, err := objectRaw(block.Arguments)
			if err != nil {
				return nil, err
			}
			out = append(out, ContentBlock{Type: "tool_use", ID: block.ToolCallID, Name: block.ToolName, Input: args})
		default:
			return nil, fmt.Errorf("Anthropic Messages response cannot represent IR block %q", block.Type)
		}
	}
	return out, nil
}

func WriteStreamEvent(w io.Writer, event ir.StreamEvent) error {
	switch event.Type {
	case ir.StreamMessageStart:
		return writeSSE(w, "message_start", map[string]any{
			"type": "message_start",
			"message": Response{
				ID:           event.ID,
				Type:         "message",
				Role:         string(ir.RoleAssistant),
				Model:        event.Model,
				Content:      []ContentBlock{},
				StopSequence: nil,
				Usage:        &Usage{},
			},
		})
	case ir.StreamContentBlockStart:
		block, err := streamBlock(event)
		if err != nil {
			return err
		}
		return writeSSE(w, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         event.BlockIndex,
			"content_block": block,
		})
	case ir.StreamContentBlockDelta:
		delta, err := streamDelta(event)
		if err != nil {
			return err
		}
		return writeSSE(w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": event.BlockIndex,
			"delta": delta,
		})
	case ir.StreamContentBlockStop:
		return writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": event.BlockIndex})
	case ir.StreamMessageDelta:
		data := map[string]any{"type": "message_delta"}
		if event.StopReason != "" {
			data["delta"] = map[string]any{"stop_reason": anthropicStop(event.StopReason), "stop_sequence": nil}
		}
		if event.Usage != (ir.Usage{}) {
			data["usage"] = usageFromIR(event.Usage)
		}
		return writeSSE(w, "message_delta", data)
	case ir.StreamMessageStop:
		return writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
	case ir.StreamError:
		return writeSSE(w, "error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": event.Error}})
	default:
		return nil
	}
}

func streamBlock(event ir.StreamEvent) (ContentBlock, error) {
	switch event.BlockType {
	case ir.BlockText:
		return ContentBlock{Type: "text", Text: ""}, nil
	case ir.BlockThinking:
		return ContentBlock{Type: "thinking", Thinking: ""}, nil
	case ir.BlockToolCall:
		return ContentBlock{Type: "tool_use", ID: event.ToolCallID, Name: event.ToolName, Input: json.RawMessage(`{}`)}, nil
	default:
		return ContentBlock{}, fmt.Errorf("Anthropic Messages stream cannot represent IR block %q", event.BlockType)
	}
}

func streamDelta(event ir.StreamEvent) (map[string]any, error) {
	switch event.BlockType {
	case ir.BlockText:
		return map[string]any{"type": "text_delta", "text": event.Delta}, nil
	case ir.BlockThinking:
		return map[string]any{"type": "thinking_delta", "thinking": event.Delta}, nil
	case ir.BlockToolCall:
		return map[string]any{"type": "input_json_delta", "partial_json": event.ArgumentsDelta}, nil
	default:
		return nil, fmt.Errorf("Anthropic Messages stream cannot represent IR block %q", event.BlockType)
	}
}

func writeSSE(w io.Writer, event string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}

func usageFromIR(in ir.Usage) *Usage {
	if in == (ir.Usage{}) {
		return nil
	}
	return &Usage{
		InputTokens:              in.InputTokens,
		OutputTokens:             in.OutputTokens,
		CacheCreationInputTokens: in.CacheCreationInputTokens,
		CacheReadInputTokens:     in.CacheReadInputTokens,
	}
}

func objectRaw(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, errors.New("IR tool_call arguments must contain JSON")
	}
	return raw, nil
}

func anthropicStop(stop string) string {
	switch stop {
	case "", "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return stop
	}
}
