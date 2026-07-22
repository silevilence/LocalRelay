package openaichat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
)

const (
	// OpenAI Chat SSE has no explicit content block indexes for text/thinking.
	// The fixed indexes keep IR events stable while arrival order remains the
	// real ordering signal. Tool calls keep OpenAI's own delta index.
	thinkingBlockIndex = 0
	textBlockIndex     = 1
	toolBlockOffset    = 2
)

type StreamResponse struct {
	ID      string         `json:"id,omitempty"`
	Object  string         `json:"object,omitempty"`
	Created int64          `json:"created,omitempty"`
	Model   string         `json:"model,omitempty"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type StreamDelta struct {
	Role             string                `json:"role,omitempty"`
	Content          *string               `json:"content,omitempty"`
	ReasoningContent string                `json:"reasoning_content,omitempty"`
	ToolCalls        []StreamToolCallDelta `json:"tool_calls,omitempty"`
}

type StreamToolCallDelta struct {
	Index    int                      `json:"index"`
	ID       string                   `json:"id,omitempty"`
	Type     string                   `json:"type,omitempty"`
	Function *StreamFunctionCallDelta `json:"function,omitempty"`
}

type StreamFunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type StreamDecoder struct {
	cfg          capabilities.Provider
	started      bool
	textBlocks   map[int]bool
	thinkBlocks  map[int]bool
	toolBlocks   map[int]map[int]bool
	activeBlocks map[int][]int
}

func NewStreamDecoder(cfg capabilities.Provider) *StreamDecoder {
	return &StreamDecoder{
		cfg:          cfg,
		textBlocks:   map[int]bool{},
		thinkBlocks:  map[int]bool{},
		toolBlocks:   map[int]map[int]bool{},
		activeBlocks: map[int][]int{},
	}
}

func ParseStream(r io.Reader, cfg capabilities.Provider) ([]ir.StreamEvent, error) {
	var events []ir.StreamEvent
	err := ForEachStreamEvent(r, cfg, func(event ir.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

func ForEachStreamEvent(r io.Reader, cfg capabilities.Provider, yield func(ir.StreamEvent) error) error {
	decoder := NewStreamDecoder(cfg)
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
		if strings.HasPrefix(line, ":") {
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

func (d *StreamDecoder) DecodePayload(data []byte) ([]ir.StreamEvent, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return []ir.StreamEvent{{Type: ir.StreamMessageStop}}, nil
	}
	var chunk StreamResponse
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, err
	}
	events := make([]ir.StreamEvent, 0, 2+len(chunk.Choices)*3)
	base := ir.StreamEvent{ID: chunk.ID, Model: chunk.Model}
	if !d.started {
		d.started = true
		events = append(events, ir.StreamEvent{Type: ir.StreamMessageStart, ID: chunk.ID, Model: chunk.Model})
	}
	for _, choice := range chunk.Choices {
		events = append(events, d.choiceEvents(base, choice)...)
	}
	if chunk.Usage != nil {
		usage := usageToIR(chunk.Usage)
		events = append(events, ir.StreamEvent{Type: ir.StreamMessageDelta, ID: chunk.ID, Model: chunk.Model, Usage: usage})
	}
	return events, nil
}

func (d *StreamDecoder) choiceEvents(base ir.StreamEvent, choice StreamChoice) []ir.StreamEvent {
	var events []ir.StreamEvent
	base.ChoiceIndex = choice.Index
	if choice.Delta.Role != "" {
		event := base
		event.Type = ir.StreamChoiceStart
		event.Role = ir.Role(choice.Delta.Role)
		events = append(events, event)
	}
	if choice.Delta.ReasoningContent != "" && d.cfg.Thinking.ResponseContentField == capabilities.ThinkingFieldReasoningContent {
		events = append(events, d.blockDelta(base, d.thinkBlocks, thinkingBlockIndex, ir.BlockThinking, choice.Delta.ReasoningContent)...)
	}
	if choice.Delta.Content != nil && *choice.Delta.Content != "" {
		events = append(events, d.blockDelta(base, d.textBlocks, textBlockIndex, ir.BlockText, *choice.Delta.Content)...)
	}
	for _, call := range choice.Delta.ToolCalls {
		events = append(events, d.toolCallEvents(base, call)...)
	}
	if choice.FinishReason != "" {
		for _, blockIndex := range d.activeBlocks[choice.Index] {
			event := base
			event.Type = ir.StreamContentBlockStop
			event.BlockIndex = blockIndex
			events = append(events, event)
		}
		delete(d.activeBlocks, choice.Index)
		event := base
		event.Type = ir.StreamMessageDelta
		event.StopReason = choice.FinishReason
		events = append(events, event)
	}
	return events
}

func (d *StreamDecoder) blockDelta(base ir.StreamEvent, started map[int]bool, blockIndex int, blockType ir.BlockType, delta string) []ir.StreamEvent {
	var events []ir.StreamEvent
	if !started[base.ChoiceIndex] {
		started[base.ChoiceIndex] = true
		start := base
		start.Type = ir.StreamContentBlockStart
		start.BlockIndex = blockIndex
		start.BlockType = blockType
		events = append(events, start)
		d.activeBlocks[base.ChoiceIndex] = append(d.activeBlocks[base.ChoiceIndex], blockIndex)
	}
	event := base
	event.Type = ir.StreamContentBlockDelta
	event.BlockIndex = blockIndex
	event.BlockType = blockType
	event.Delta = delta
	return append(events, event)
}

func (d *StreamDecoder) toolCallEvents(base ir.StreamEvent, call StreamToolCallDelta) []ir.StreamEvent {
	if d.toolBlocks[base.ChoiceIndex] == nil {
		d.toolBlocks[base.ChoiceIndex] = map[int]bool{}
	}
	blockIndex := call.Index + toolBlockOffset
	var events []ir.StreamEvent
	if !d.toolBlocks[base.ChoiceIndex][call.Index] {
		d.toolBlocks[base.ChoiceIndex][call.Index] = true
		start := base
		start.Type = ir.StreamContentBlockStart
		start.BlockIndex = blockIndex
		start.BlockType = ir.BlockToolCall
		start.ToolCallID = call.ID
		if call.Function != nil {
			start.ToolName = call.Function.Name
		}
		events = append(events, start)
		d.activeBlocks[base.ChoiceIndex] = append(d.activeBlocks[base.ChoiceIndex], blockIndex)
	}
	if call.Function != nil && call.Function.Arguments != "" {
		event := base
		event.Type = ir.StreamContentBlockDelta
		event.BlockIndex = blockIndex
		event.BlockType = ir.BlockToolCall
		event.ToolCallID = call.ID
		event.ToolName = call.Function.Name
		event.ArgumentsDelta = call.Function.Arguments
		events = append(events, event)
	}
	return events
}

func WriteStreamEvent(w io.Writer, event ir.StreamEvent, cfg capabilities.Provider) error {
	if event.Type == ir.StreamMessageStop {
		_, err := io.WriteString(w, "data: [DONE]\n\n")
		return err
	}
	if event.Type == ir.StreamError {
		data, err := json.Marshal(map[string]any{"error": map[string]any{
			"message": event.Error,
			"type":    "upstream_stream_error",
			"code":    "upstream_stream_error",
		}})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "data: %s\n\n", data)
		return err
	}
	chunk, ok := streamChunkFromEvent(event, cfg)
	if !ok {
		return nil
	}
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func streamChunkFromEvent(event ir.StreamEvent, cfg capabilities.Provider) (StreamResponse, bool) {
	chunk := StreamResponse{ID: event.ID, Object: "chat.completion.chunk", Model: event.Model, Choices: []StreamChoice{}}
	switch event.Type {
	case ir.StreamChoiceStart:
		chunk.Choices = []StreamChoice{{
			Index: event.ChoiceIndex,
			Delta: StreamDelta{Role: string(event.Role)},
		}}
	case ir.StreamContentBlockStart:
		if event.BlockType != ir.BlockToolCall {
			// OpenAI Chat SSE has no start marker for text/thinking blocks; the
			// first content or reasoning delta carries that information.
			return StreamResponse{}, false
		}
		chunk.Choices = []StreamChoice{{
			Index: event.ChoiceIndex,
			Delta: StreamDelta{ToolCalls: []StreamToolCallDelta{{
				Index: event.BlockIndex - toolBlockOffset,
				ID:    event.ToolCallID,
				Type:  "function",
				Function: &StreamFunctionCallDelta{
					Name: event.ToolName,
				},
			}}},
		}}
	case ir.StreamContentBlockDelta:
		delta := StreamDelta{}
		switch event.BlockType {
		case ir.BlockText:
			delta.Content = &event.Delta
		case ir.BlockThinking:
			if cfg.Thinking.ResponseContentField != capabilities.ThinkingFieldReasoningContent {
				// Generic OpenAI Chat has no standard thinking delta field, so
				// providers without reasoning_content support cannot receive it.
				return StreamResponse{}, false
			}
			delta.ReasoningContent = event.Delta
		case ir.BlockToolCall:
			delta.ToolCalls = []StreamToolCallDelta{{
				Index: event.BlockIndex - toolBlockOffset,
				ID:    event.ToolCallID,
				Function: &StreamFunctionCallDelta{
					Name:      event.ToolName,
					Arguments: event.ArgumentsDelta,
				},
			}}
		default:
			return StreamResponse{}, false
		}
		chunk.Choices = []StreamChoice{{Index: event.ChoiceIndex, Delta: delta}}
	case ir.StreamMessageDelta:
		if event.Usage != (ir.Usage{}) {
			chunk.Usage = usageFromIR(event.Usage)
		}
		if event.StopReason != "" {
			chunk.Choices = []StreamChoice{{
				Index:        event.ChoiceIndex,
				Delta:        StreamDelta{},
				FinishReason: event.StopReason,
			}}
		}
		if len(chunk.Choices) == 0 && chunk.Usage == nil {
			return StreamResponse{}, false
		}
	default:
		// message_start/content_block_stop are Anthropic-style IR events. OpenAI
		// Chat represents them implicitly through role, finish_reason, and DONE.
		return StreamResponse{}, false
	}
	return chunk, true
}

func usageToIR(in *Usage) ir.Usage {
	inputTokens := in.PromptTokens
	if inputTokens == 0 {
		inputTokens = in.PromptCacheHit + in.PromptCacheMiss
	}
	out := ir.Usage{
		InputTokens:  inputTokens,
		OutputTokens: in.CompletionTokens,
	}
	if in.PromptCacheHit > 0 {
		out.CacheReadInputTokens = in.PromptCacheHit
	}
	if in.PromptTokensDetails != nil && in.PromptTokensDetails.CachedTokens > 0 {
		out.CacheReadInputTokens = in.PromptTokensDetails.CachedTokens
	}
	return out
}

func usageFromIR(in ir.Usage) *Usage {
	out := &Usage{
		PromptTokens:     in.InputTokens,
		CompletionTokens: in.OutputTokens,
		TotalTokens:      in.InputTokens + in.OutputTokens,
	}
	if in.CacheReadInputTokens > 0 {
		out.PromptTokensDetails = &PromptTokenDetails{CachedTokens: in.CacheReadInputTokens}
	}
	return out
}
