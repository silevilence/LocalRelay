package openairesponses

import (
	"encoding/json"
	"strings"
	"testing"

	"localrelay/internal/ir"
)

func TestFromIRResponse(t *testing.T) {
	resp, err := FromIRResponse(ir.Response{
		ID:    "resp_1",
		Model: "gpt-test",
		Usage: ir.Usage{InputTokens: 4, OutputTokens: 6, CacheReadInputTokens: 2},
		Choices: []ir.Choice{{
			Message: ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				ir.Text("hi"),
				ir.Thinking("plan", ""),
				ir.ToolCall("call_1", "lookup", json.RawMessage(`{"q":"x"}`)),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Object != "response" || resp.Status != "completed" || resp.Usage.TotalTokens != 10 {
		t.Fatalf("response = %#v", resp)
	}
	if resp.Output[0].Type != "message" || resp.Output[0].Content[0].Type != "output_text" {
		t.Fatalf("message item = %#v", resp.Output[0])
	}
	if resp.Output[0].Content[1].Type != "reasoning_text" || resp.Output[1].Arguments != `{"q":"x"}` {
		t.Fatalf("reasoning/tool = %#v", resp.Output)
	}
}

func TestWriteStreamEvent(t *testing.T) {
	var out strings.Builder
	writer := NewStreamWriter(&out)
	for _, event := range []ir.StreamEvent{
		{Type: ir.StreamMessageStart, ID: "resp_1", Model: "gpt-test"},
		{Type: ir.StreamChoiceStart, ChoiceIndex: 0, Role: ir.RoleAssistant},
		{Type: ir.StreamContentBlockStart, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockText},
		{Type: ir.StreamContentBlockDelta, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockText, Delta: "hi"},
		{Type: ir.StreamContentBlockStop, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockText},
		{Type: ir.StreamContentBlockStart, ChoiceIndex: 0, BlockIndex: 1, BlockType: ir.BlockThinking},
		{Type: ir.StreamContentBlockDelta, ChoiceIndex: 0, BlockIndex: 1, BlockType: ir.BlockThinking, Delta: "plan"},
		{Type: ir.StreamContentBlockStop, ChoiceIndex: 0, BlockIndex: 1, BlockType: ir.BlockThinking},
		{Type: ir.StreamContentBlockStart, BlockIndex: 2, BlockType: ir.BlockToolCall, ToolCallID: "call_1", ToolName: "lookup"},
		{Type: ir.StreamContentBlockDelta, BlockIndex: 2, BlockType: ir.BlockToolCall, ToolCallID: "call_1", ArgumentsDelta: `{"q"`},
		{Type: ir.StreamContentBlockDelta, BlockIndex: 2, BlockType: ir.BlockToolCall, ToolCallID: "call_1", ArgumentsDelta: `:"x"}`},
		{Type: ir.StreamContentBlockStop, BlockIndex: 2, BlockType: ir.BlockToolCall, ToolCallID: "call_1"},
		{Type: ir.StreamMessageDelta, ID: "resp_1", Model: "gpt-test", Usage: ir.Usage{InputTokens: 1, OutputTokens: 2}},
		{Type: ir.StreamMessageStop, ID: "resp_1", Model: "gpt-test"},
	} {
		if err := writer.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	got := out.String()
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", `"text":"hi"`, "event: response.reasoning_text.delta", `"text":"plan"`, "event: response.function_call_arguments.delta", "event: response.function_call_arguments.done", `"arguments":"{\"q\":\"x\"}"`, "event: response.output_item.done", "event: response.completed", `"total_tokens":3`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestRejectsUnsupportedResponseBlock(t *testing.T) {
	_, err := FromIRResponse(ir.Response{Choices: []ir.Choice{{
		Message: ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.Image("https://example.test/a.png", "")}},
	}}})
	if err == nil {
		t.Fatal("expected image error")
	}
}
