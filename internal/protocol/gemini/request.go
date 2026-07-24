package gemini

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
	Contents          []Content        `json:"contents"`
	SystemInstruction *Content         `json:"systemInstruction,omitempty"`
	Tools             []Tool           `json:"tools,omitempty"`
	GenerationConfig  GenerationConfig `json:"generationConfig,omitempty"`
}

type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type FunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type GenerationConfig struct {
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

func ToProviderRequest(req ir.Request) (Request, error) {
	out := Request{GenerationConfig: GenerationConfig{
		MaxOutputTokens: req.Params.MaxTokens,
		Temperature:     req.Params.Temperature,
		TopP:            req.Params.TopP,
		StopSequences:   req.Params.Stop,
	}}
	if len(req.Tools) > 0 {
		tool := Tool{FunctionDeclarations: make([]FunctionDeclaration, 0, len(req.Tools))}
		for _, in := range req.Tools {
			if in.Type != "" && in.Type != "function" {
				return Request{}, fmt.Errorf("unsupported IR tool type %q", in.Type)
			}
			tool.FunctionDeclarations = append(tool.FunctionDeclarations, FunctionDeclaration{Name: in.Name, Description: in.Description, Parameters: objectOrEmpty(in.Parameters)})
		}
		out.Tools = []Tool{tool}
	}
	for _, msg := range req.Messages {
		parts, err := requestPartsFromIR(msg.Content)
		if err != nil {
			return Request{}, err
		}
		if msg.Role == ir.RoleSystem {
			out.SystemInstruction = &Content{Parts: parts}
			continue
		}
		role, err := geminiRole(msg.Role)
		if err != nil {
			return Request{}, err
		}
		out.Contents = append(out.Contents, Content{Role: role, Parts: parts})
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
	out := ir.Response{ID: r.ResponseID, Model: r.ModelVersion, Usage: usageToIR(r.UsageMetadata)}
	for _, candidate := range r.Candidates {
		blocks, err := blocksFromParts(candidate.Content.Parts)
		if err != nil {
			return ir.Response{}, err
		}
		out.Choices = append(out.Choices, ir.Choice{
			Index:      candidate.Index,
			Message:    ir.Message{Role: ir.RoleAssistant, Content: blocks},
			StopReason: openAIStop(candidate.FinishReason),
		})
	}
	return out, nil
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
	started := false
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		var chunk Response
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			if yieldErr := yield(ir.StreamEvent{Type: ir.StreamError, Error: err.Error()}); yieldErr != nil {
				return yieldErr
			}
			return err
		}
		if !started {
			started = true
			if err := yield(ir.StreamEvent{Type: ir.StreamMessageStart, ID: chunk.ResponseID, Model: chunk.ModelVersion}); err != nil {
				return err
			}
		}
		for _, event := range streamEvents(chunk) {
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

func streamEvents(chunk Response) []ir.StreamEvent {
	var out []ir.StreamEvent
	for _, candidate := range chunk.Candidates {
		out = append(out, ir.StreamEvent{Type: ir.StreamChoiceStart, ChoiceIndex: candidate.Index, Role: ir.RoleAssistant})
		for i, part := range candidate.Content.Parts {
			blockType := ir.BlockText
			delta := part.Text
			if part.Thought {
				blockType = ir.BlockThinking
			}
			if part.FunctionCall != nil {
				blockType = ir.BlockToolCall
			}
			out = append(out, ir.StreamEvent{Type: ir.StreamContentBlockStart, ChoiceIndex: candidate.Index, BlockIndex: i, BlockType: blockType})
			event := ir.StreamEvent{Type: ir.StreamContentBlockDelta, ChoiceIndex: candidate.Index, BlockIndex: i, BlockType: blockType, Delta: delta}
			if part.FunctionCall != nil {
				event.ToolName = part.FunctionCall.Name
				event.ArgumentsDelta = string(part.FunctionCall.Args)
			}
			out = append(out, event, ir.StreamEvent{Type: ir.StreamContentBlockStop, ChoiceIndex: candidate.Index, BlockIndex: i})
		}
		if candidate.FinishReason != "" {
			out = append(out, ir.StreamEvent{Type: ir.StreamMessageDelta, ChoiceIndex: candidate.Index, StopReason: openAIStop(candidate.FinishReason)})
		}
	}
	if chunk.UsageMetadata != nil {
		out = append(out, ir.StreamEvent{Type: ir.StreamMessageDelta, Usage: usageToIR(chunk.UsageMetadata)})
	}
	return out
}

func requestPartsFromIR(blocks []ir.ContentBlock) ([]Part, error) {
	out := make([]Part, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case ir.BlockText:
			out = append(out, Part{Text: block.Text})
		case ir.BlockImage:
			blob, err := inlineData(block.ImageURL)
			if err != nil {
				return nil, err
			}
			out = append(out, Part{InlineData: &blob})
		case ir.BlockThinking:
			out = append(out, Part{Text: block.Text, Thought: true, ThoughtSignature: block.Signature})
		case ir.BlockToolCall:
			out = append(out, Part{FunctionCall: &FunctionCall{Name: block.ToolName, Args: objectOrEmpty(block.Arguments)}})
		case ir.BlockToolResult:
			out = append(out, Part{FunctionResponse: &FunctionResponse{Response: json.RawMessage(fmt.Sprintf(`{"result":%q}`, block.Result))}})
		default:
			return nil, fmt.Errorf("Gemini request cannot represent IR block %q", block.Type)
		}
	}
	return out, nil
}

func blocksFromParts(parts []Part) ([]ir.ContentBlock, error) {
	out := make([]ir.ContentBlock, 0, len(parts))
	for _, part := range parts {
		switch {
		case part.FunctionCall != nil:
			out = append(out, ir.ToolCall("", part.FunctionCall.Name, objectOrEmpty(part.FunctionCall.Args)))
		case part.Thought:
			out = append(out, ir.Thinking(part.Text, part.ThoughtSignature))
		case part.Text != "":
			out = append(out, ir.Text(part.Text))
		}
	}
	return out, nil
}

func inlineData(url string) (Blob, error) {
	media, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ";base64,")
	if !ok || media == "" {
		return Blob{}, fmt.Errorf("Gemini request only supports data URL images, got %q", url)
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return Blob{}, err
	}
	return Blob{MIMEType: media, Data: data}, nil
}

func geminiRole(role ir.Role) (string, error) {
	switch role {
	case ir.RoleUser, ir.RoleTool:
		return "user", nil
	case ir.RoleAssistant:
		return "model", nil
	default:
		return "", fmt.Errorf("unsupported role %q", role)
	}
}

func usageToIR(in *UsageMetadata) ir.Usage {
	if in == nil {
		return ir.Usage{}
	}
	return ir.Usage{InputTokens: in.PromptTokenCount, OutputTokens: in.CandidatesTokenCount, CacheReadInputTokens: in.CachedContentTokenCount}
}

func objectOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func openAIStop(stop string) string {
	switch strings.ToUpper(stop) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	default:
		return strings.ToLower(stop)
	}
}
