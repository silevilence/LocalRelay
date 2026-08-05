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
	if resp.Output[0].ID == "" || len(resp.Output[0].Content) != 1 || resp.Output[0].Content[0].Annotations == nil || resp.Output[1].ID == "" || resp.Output[1].Arguments != `{"q":"x"}` {
		t.Fatalf("message/tool = %#v", resp.Output)
	}
}

func TestResponseOutputUsesClientCompatibleMessageShape(t *testing.T) {
	resp, err := FromIRResponse(ir.Response{
		ID:    "resp_1",
		Model: "gpt-test",
		Choices: []ir.Choice{{Message: ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			ir.Text("hi"),
			ir.Thinking("internal reasoning", ""),
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Output []struct {
			ID      string `json:"id"`
			Content []struct {
				Type        string `json:"type"`
				Annotations []any  `json:"annotations"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Output) != 1 || body.Output[0].ID == "" || len(body.Output[0].Content) != 1 || body.Output[0].Content[0].Type != "output_text" || body.Output[0].Content[0].Annotations == nil {
		t.Fatalf("response = %s", data)
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
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", `"text":"hi"`, "event: response.function_call_arguments.delta", "event: response.function_call_arguments.done", `"arguments":"{\"q\":\"x\"}"`, "event: response.output_item.done", "event: response.completed", `"total_tokens":3`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "reasoning_text") {
		t.Fatalf("reasoning must not be emitted as message content: %s", got)
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
