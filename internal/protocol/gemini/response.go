package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"localrelay/internal/ir"
)

type Response struct {
	Candidates    []Candidate    `json:"candidates,omitempty"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string         `json:"modelVersion,omitempty"`
	ResponseID    string         `json:"responseId,omitempty"`
}

type Candidate struct {
	Index        int     `json:"index,omitempty"`
	Content      Content `json:"content,omitempty"`
	FinishReason string  `json:"finishReason,omitempty"`
}

type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts,omitempty"`
}

type Part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
}

type FunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type FunctionResponse struct {
	Name     string          `json:"name,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
}

type Blob struct {
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

type UsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

func FromIRResponse(resp ir.Response) (Response, error) {
	out := Response{
		ResponseID:    resp.ID,
		ModelVersion:  resp.Model,
		UsageMetadata: usageFromIR(resp.Usage),
		Candidates:    make([]Candidate, 0, len(resp.Choices)),
	}
	for _, choice := range resp.Choices {
		parts, err := partsFromIR(choice.Message.Content)
		if err != nil {
			return Response{}, err
		}
		out.Candidates = append(out.Candidates, Candidate{
			Index:        choice.Index,
			Content:      Content{Role: "model", Parts: parts},
			FinishReason: geminiStop(choice.StopReason),
		})
	}
	return out, nil
}

func partsFromIR(blocks []ir.ContentBlock) ([]Part, error) {
	out := make([]Part, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case ir.BlockText:
			out = append(out, Part{Text: block.Text})
		case ir.BlockThinking:
			out = append(out, Part{Text: block.Text, Thought: true, ThoughtSignature: block.Signature})
		case ir.BlockImage:
			image, err := inlineData(block.ImageURL)
			if err != nil {
				return nil, err
			}
			out = append(out, Part{InlineData: &image})
		case ir.BlockToolCall:
			args, err := objectRaw(block.Arguments)
			if err != nil {
				return nil, err
			}
			out = append(out, Part{FunctionCall: &FunctionCall{Name: block.ToolName, Args: args}})
		default:
			return nil, fmt.Errorf("Gemini response cannot represent IR block %q", block.Type)
		}
	}
	return out, nil
}

func WriteStreamEvent(w io.Writer, event ir.StreamEvent) error {
	switch event.Type {
	case ir.StreamContentBlockStart:
		if event.BlockType == ir.BlockToolCall {
			return writeData(w, responseForPart(event, Part{FunctionCall: &FunctionCall{Name: event.ToolName, Args: json.RawMessage(`{}`)}}))
		}
	case ir.StreamContentBlockDelta:
		part, ok, err := deltaPart(event)
		if err != nil || !ok {
			return err
		}
		return writeData(w, responseForPart(event, part))
	case ir.StreamMessageDelta:
		resp := Response{}
		if event.StopReason != "" {
			resp.Candidates = []Candidate{{Index: event.ChoiceIndex, FinishReason: geminiStop(event.StopReason)}}
		}
		if event.Usage != (ir.Usage{}) {
			resp.UsageMetadata = usageFromIR(event.Usage)
		}
		if len(resp.Candidates) > 0 || resp.UsageMetadata != nil {
			return writeData(w, resp)
		}
	case ir.StreamError:
		return writeData(w, map[string]any{"error": map[string]any{"message": event.Error}})
	}
	return nil
}

func deltaPart(event ir.StreamEvent) (Part, bool, error) {
	switch event.BlockType {
	case ir.BlockText:
		return Part{Text: event.Delta}, true, nil
	case ir.BlockThinking:
		return Part{Text: event.Delta, Thought: true}, true, nil
	case ir.BlockToolCall:
		if strings.TrimSpace(event.ArgumentsDelta) == "" {
			return Part{}, false, nil
		}
		if !json.Valid([]byte(event.ArgumentsDelta)) {
			// Gemini functionCall.args is an object, so partial JSON cannot be
			// represented as a valid official stream chunk.
			return Part{}, false, nil
		}
		return Part{FunctionCall: &FunctionCall{Name: event.ToolName, Args: json.RawMessage(event.ArgumentsDelta)}}, true, nil
	default:
		return Part{}, false, fmt.Errorf("Gemini stream cannot represent IR block %q", event.BlockType)
	}
}

func responseForPart(event ir.StreamEvent, part Part) Response {
	return Response{
		ResponseID:   event.ID,
		ModelVersion: event.Model,
		Candidates: []Candidate{{
			Index:   event.ChoiceIndex,
			Content: Content{Role: "model", Parts: []Part{part}},
		}},
	}
}

func writeData(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func usageFromIR(in ir.Usage) *UsageMetadata {
	if in == (ir.Usage{}) {
		return nil
	}
	return &UsageMetadata{
		PromptTokenCount:        in.InputTokens,
		CandidatesTokenCount:    in.OutputTokens,
		TotalTokenCount:         in.InputTokens + in.OutputTokens,
		CachedContentTokenCount: in.CacheReadInputTokens,
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

func geminiStop(stop string) string {
	switch stop {
	case "", "stop", "end_turn", "tool_calls", "tool_use":
		return "STOP"
	case "length", "max_tokens":
		return "MAX_TOKENS"
	default:
		return strings.ToUpper(stop)
	}
}
