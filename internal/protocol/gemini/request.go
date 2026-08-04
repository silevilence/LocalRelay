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
	MaxOutputTokens *int            `json:"maxOutputTokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"topP,omitempty"`
	StopSequences   []string        `json:"stopSequences,omitempty"`
	ThinkingConfig  json.RawMessage `json:"thinkingConfig,omitempty"`
}

// ParseRequest converts a Gemini generateContent request into the gateway's
// protocol-neutral representation. Gemini chooses the model and stream mode
// from its URL, so callers supply those values separately.
func ParseRequest(data []byte, model string, stream bool) (ir.Request, error) {
	var in Request
	if err := json.Unmarshal(data, &in); err != nil {
		return ir.Request{}, err
	}
	return in.ToIR(model, stream)
}

func (r Request) ToIR(model string, stream bool) (ir.Request, error) {
	out := ir.Request{
		Model:  model,
		Stream: stream,
		Params: ir.Params{
			MaxTokens:   r.GenerationConfig.MaxOutputTokens,
			Temperature: r.GenerationConfig.Temperature,
			TopP:        r.GenerationConfig.TopP,
			Stop:        r.GenerationConfig.StopSequences,
			Thinking:    r.GenerationConfig.ThinkingConfig,
		},
	}
	if r.SystemInstruction != nil {
		blocks, err := inputBlocks(r.SystemInstruction.Parts)
		if err != nil {
			return ir.Request{}, err
		}
		if len(blocks) > 0 {
			out.Messages = append(out.Messages, ir.Message{Role: ir.RoleSystem, Content: blocks})
		}
	}
	for _, tool := range r.Tools {
		for _, declaration := range tool.FunctionDeclarations {
			out.Tools = append(out.Tools, ir.Tool{Type: "function", Name: declaration.Name, Description: declaration.Description, Parameters: objectOrEmpty(declaration.Parameters)})
		}
	}
	callIDs := map[string][]string{}
	nextCallID := 1
	for _, content := range r.Contents {
		role, err := irRole(content.Role)
		if err != nil {
			return ir.Request{}, err
		}
		var ordinary []ir.ContentBlock
		flushOrdinary := func() {
			if len(ordinary) > 0 {
				out.Messages = append(out.Messages, ir.Message{Role: role, Content: ordinary})
				ordinary = nil
			}
		}
		for _, part := range content.Parts {
			switch {
			case part.FunctionResponse != nil:
				result, err := functionResponseResult(part.FunctionResponse.Response)
				if err != nil {
					return ir.Request{}, err
				}
				ids := callIDs[part.FunctionResponse.Name]
				if len(ids) == 0 {
					// Gemini function responses identify calls only by name. A
					// response without a preceding call is ambiguous in IR, so it
					// is rejected rather than silently misrouting the result.
					return ir.Request{}, fmt.Errorf("Gemini functionResponse %q has no preceding functionCall", part.FunctionResponse.Name)
				}
				flushOrdinary()
				out.Messages = append(out.Messages, ir.Message{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult(ids[0], result)}})
				callIDs[part.FunctionResponse.Name] = ids[1:]
			case part.FunctionCall != nil:
				id := fmt.Sprintf("gemini-call-%d", nextCallID)
				nextCallID++
				callIDs[part.FunctionCall.Name] = append(callIDs[part.FunctionCall.Name], id)
				ordinary = append(ordinary, ir.ToolCall(id, part.FunctionCall.Name, objectOrEmpty(part.FunctionCall.Args)))
			case part.InlineData != nil:
				if part.InlineData.MIMEType == "" || part.InlineData.Data == "" {
					return ir.Request{}, fmt.Errorf("Gemini inlineData requires mimeType and data")
				}
				if _, err := base64.StdEncoding.DecodeString(part.InlineData.Data); err != nil {
					return ir.Request{}, err
				}
				ordinary = append(ordinary, ir.Image("data:"+part.InlineData.MIMEType+";base64,"+part.InlineData.Data, ""))
			case part.Thought:
				ordinary = append(ordinary, ir.Thinking(part.Text, part.ThoughtSignature))
			case part.Text != "":
				ordinary = append(ordinary, ir.Text(part.Text))
			default:
				return ir.Request{}, fmt.Errorf("unsupported Gemini content part")
			}
		}
		flushOrdinary()
	}
	return out, nil
}

func inputBlocks(parts []Part) ([]ir.ContentBlock, error) {
	blocks := make([]ir.ContentBlock, 0, len(parts))
	for _, part := range parts {
		if part.Text == "" {
			return nil, fmt.Errorf("unsupported Gemini system instruction part")
		}
		blocks = append(blocks, ir.Text(part.Text))
	}
	return blocks, nil
}

func irRole(role string) (ir.Role, error) {
	switch role {
	case "", "user":
		return ir.RoleUser, nil
	case "model":
		return ir.RoleAssistant, nil
	default:
		return "", fmt.Errorf("unsupported Gemini role %q", role)
	}
}

func functionResponseResult(response json.RawMessage) (string, error) {
	if len(response) == 0 {
		return "", nil
	}
	var result struct {
		Result any `json:"result"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return "", err
	}
	if result.Result == nil {
		return string(response), nil
	}
	switch value := result.Result.(type) {
	case string:
		return value, nil
	default:
		data, err := json.Marshal(value)
		return string(data), err
	}
}

func ToProviderRequest(req ir.Request) (Request, error) {
	out := Request{GenerationConfig: GenerationConfig{
		MaxOutputTokens: req.Params.MaxTokens,
		Temperature:     req.Params.Temperature,
		TopP:            req.Params.TopP,
		StopSequences:   req.Params.Stop,
		ThinkingConfig:  req.Params.Thinking,
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
