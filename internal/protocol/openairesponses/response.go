package openairesponses

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"localrelay/internal/ir"
)

type Response struct {
	ID     string       `json:"id,omitempty"`
	Object string       `json:"object"`
	Status string       `json:"status"`
	Model  string       `json:"model"`
	Output []OutputItem `json:"output,omitempty"`
	Usage  *Usage       `json:"usage,omitempty"`
}

type OutputItem struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	Status    string        `json:"status,omitempty"`
	Role      string        `json:"role,omitempty"`
	Content   []ContentPart `json:"content,omitempty"`
	CallID    string        `json:"call_id,omitempty"`
	Name      string        `json:"name,omitempty"`
	Arguments string        `json:"arguments,omitempty"`
}

type ContentPart struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type Usage struct {
	InputTokens         int                 `json:"input_tokens,omitempty"`
	OutputTokens        int                 `json:"output_tokens,omitempty"`
	TotalTokens         int                 `json:"total_tokens,omitempty"`
	InputTokensDetails  *InputTokenDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *OutputTokenDetails `json:"output_tokens_details,omitempty"`
}

type InputTokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type OutputTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type StreamWriter struct {
	w         io.Writer
	response  Response
	usage     ir.Usage
	items     map[int]OutputItem
	text      map[string]string
	completed bool
}

func NewStreamWriter(w io.Writer) *StreamWriter {
	return &StreamWriter{
		w:     w,
		items: map[int]OutputItem{},
		text:  map[string]string{},
	}
}

func FromIRResponse(resp ir.Response) (Response, error) {
	out := Response{
		ID:     resp.ID,
		Object: "response",
		Status: "completed",
		Model:  resp.Model,
		Usage:  usageFromIR(resp.Usage),
	}
	for _, choice := range resp.Choices {
		items, err := outputItemsFromIR(resp.ID, choice)
		if err != nil {
			return Response{}, err
		}
		out.Output = append(out.Output, items...)
	}
	return out, nil
}

func outputItemsFromIR(responseID string, choice ir.Choice) ([]OutputItem, error) {
	var items []OutputItem
	var parts []ContentPart
	for blockIndex, block := range choice.Message.Content {
		switch block.Type {
		case ir.BlockText:
			parts = append(parts, ContentPart{Type: "output_text", Text: block.Text, Annotations: []any{}})
		case ir.BlockThinking:
			// The Responses clients targeted by this gateway accept only
			// output_text inside message content. IR reasoning is therefore
			// intentionally omitted at this protocol boundary.
			continue
		case ir.BlockToolCall:
			args, err := argumentString(block.Arguments)
			if err != nil {
				return nil, err
			}
			items = append(items, OutputItem{
				ID:        functionItemID(responseID, choice.Index, blockIndex),
				Type:      "function_call",
				Status:    "completed",
				CallID:    block.ToolCallID,
				Name:      block.ToolName,
				Arguments: args,
			})
		default:
			return nil, fmt.Errorf("OpenAI Responses response cannot represent IR block %q", block.Type)
		}
	}
	if len(parts) > 0 || len(items) == 0 {
		msg := OutputItem{
			ID:      messageItemID(responseID, choice.Index),
			Type:    "message",
			Status:  "completed",
			Role:    string(ir.RoleAssistant),
			Content: parts,
		}
		items = append([]OutputItem{msg}, items...)
	}
	return items, nil
}

func WriteStreamEvent(w io.Writer, event ir.StreamEvent) error {
	return NewStreamWriter(w).Write(event)
}

func (s *StreamWriter) Write(event ir.StreamEvent) error {
	switch event.Type {
	case ir.StreamMessageStart:
		s.response = Response{ID: event.ID, Object: "response", Status: "in_progress", Model: event.Model}
		return writeEvent(s.w, "response.created", map[string]any{
			"response": s.response,
		})
	case ir.StreamChoiceStart:
		item := OutputItem{
			ID:     messageItemID(s.response.ID, event.ChoiceIndex),
			Type:   "message",
			Status: "in_progress",
			Role:   string(event.Role),
		}
		s.items[event.ChoiceIndex] = item
		return writeEvent(s.w, "response.output_item.added", map[string]any{
			"output_index": event.ChoiceIndex,
			"item":         item,
		})
	case ir.StreamContentBlockStart:
		return s.writeBlockStart(event)
	case ir.StreamContentBlockDelta:
		return s.writeBlockDelta(event)
	case ir.StreamContentBlockStop:
		return s.writeBlockStop(event)
	case ir.StreamMessageDelta:
		if event.Usage != (ir.Usage{}) {
			s.usage = event.Usage
		}
		return nil
	case ir.StreamMessageStop:
		return s.complete()
	case ir.StreamError:
		return writeEvent(s.w, "error", map[string]any{"error": map[string]any{"message": event.Error}})
	}
	return nil
}

func (s *StreamWriter) writeBlockStart(event ir.StreamEvent) error {
	switch event.BlockType {
	case ir.BlockText:
		return writeEvent(s.w, "response.content_part.added", map[string]any{
			"output_index":  event.ChoiceIndex,
			"content_index": event.BlockIndex,
			"part":          ContentPart{Type: "output_text", Text: "", Annotations: []any{}},
		})
	case ir.BlockThinking:
		return nil
	case ir.BlockToolCall:
		item := OutputItem{
			ID:     functionItemID(s.response.ID, event.ChoiceIndex, event.BlockIndex),
			Type:   "function_call",
			Status: "in_progress",
			CallID: event.ToolCallID,
			Name:   event.ToolName,
		}
		s.items[event.BlockIndex] = item
		return writeEvent(s.w, "response.output_item.added", map[string]any{
			"output_index": event.BlockIndex,
			"item":         item,
		})
	default:
		return fmt.Errorf("OpenAI Responses stream cannot represent IR block %q", event.BlockType)
	}
}

func (s *StreamWriter) writeBlockDelta(event ir.StreamEvent) error {
	switch event.BlockType {
	case ir.BlockText:
		s.text[contentKey(event)] += event.Delta
		return writeEvent(s.w, "response.output_text.delta", map[string]any{
			"output_index":  event.ChoiceIndex,
			"content_index": event.BlockIndex,
			"delta":         event.Delta,
		})
	case ir.BlockThinking:
		return nil
	case ir.BlockToolCall:
		item := s.items[event.BlockIndex]
		item.CallID = firstNonEmpty(item.CallID, event.ToolCallID)
		item.Name = firstNonEmpty(item.Name, event.ToolName)
		item.Arguments += event.ArgumentsDelta
		s.items[event.BlockIndex] = item
		return writeEvent(s.w, "response.function_call_arguments.delta", map[string]any{
			"output_index": event.BlockIndex,
			"item_id":      event.ToolCallID,
			"delta":        event.ArgumentsDelta,
		})
	default:
		return fmt.Errorf("OpenAI Responses stream cannot represent IR block %q", event.BlockType)
	}
}

func (s *StreamWriter) writeBlockStop(event ir.StreamEvent) error {
	switch event.BlockType {
	case ir.BlockText:
		return writeEvent(s.w, "response.output_text.done", map[string]any{"output_index": event.ChoiceIndex, "content_index": event.BlockIndex, "text": s.text[contentKey(event)]})
	case ir.BlockThinking:
		return nil
	case ir.BlockToolCall:
		item := s.items[event.BlockIndex]
		item.Status = "completed"
		return writeEvents(s.w,
			streamEvent{"response.function_call_arguments.done", map[string]any{"output_index": event.BlockIndex, "item_id": firstNonEmpty(event.ToolCallID, item.CallID), "arguments": item.Arguments}},
			streamEvent{"response.output_item.done", map[string]any{"output_index": event.BlockIndex, "item": item}},
		)
	default:
		return nil
	}
}

func (s *StreamWriter) complete() error {
	if s.completed {
		return nil
	}
	s.completed = true
	s.response.Status = "completed"
	s.response.Usage = usageFromIR(s.usage)
	return writeEvent(s.w, "response.completed", map[string]any{"response": s.response})
}

type streamEvent struct {
	name  string
	value map[string]any
}

func writeEvents(w io.Writer, events ...streamEvent) error {
	for _, event := range events {
		if err := writeEvent(w, event.name, event.value); err != nil {
			return err
		}
	}
	return nil
}

func writeEvent(w io.Writer, eventType string, value map[string]any) error {
	value["type"] = eventType
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
	return err
}

func contentKey(event ir.StreamEvent) string {
	return fmt.Sprintf("%d:%d", event.ChoiceIndex, event.BlockIndex)
}

func messageItemID(responseID string, choiceIndex int) string {
	return fmt.Sprintf("msg_%s_%d", responseID, choiceIndex)
}

func functionItemID(responseID string, choiceIndex, blockIndex int) string {
	return fmt.Sprintf("fc_%s_%d_%d", responseID, choiceIndex, blockIndex)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func usageFromIR(in ir.Usage) *Usage {
	if in == (ir.Usage{}) {
		return nil
	}
	out := &Usage{
		InputTokens:  in.InputTokens,
		OutputTokens: in.OutputTokens,
		TotalTokens:  in.InputTokens + in.OutputTokens,
	}
	if in.CacheReadInputTokens > 0 {
		out.InputTokensDetails = &InputTokenDetails{CachedTokens: in.CacheReadInputTokens}
	}
	return out
}

func argumentString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if !json.Valid(raw) {
		return "", errors.New("IR tool_call arguments must contain JSON")
	}
	return string(raw), nil
}
