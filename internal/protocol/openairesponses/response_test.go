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
	var message, tool *OutputItem
	for i := range resp.Output {
		switch resp.Output[i].Type {
		case "message":
			message = &resp.Output[i]
		case "function_call":
			tool = &resp.Output[i]
		}
	}
	if message == nil || message.Content[0].Type != "output_text" || message.ID == "" || len(message.Content) != 1 || message.Content[0].Annotations == nil || tool == nil || tool.ID == "" || tool.Arguments != `{"q":"x"}` {
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
	var message *struct {
		ID      string `json:"id"`
		Content []struct {
			Type        string `json:"type"`
			Annotations []any  `json:"annotations"`
		} `json:"content"`
	}
	for i := range body.Output {
		if body.Output[i].ID != "" && len(body.Output[i].Content) == 1 && body.Output[i].Content[0].Type == "output_text" {
			message = &body.Output[i]
			break
		}
	}
	if len(body.Output) != 2 || message == nil || message.Content[0].Annotations == nil {
		t.Fatalf("response = %s", data)
	}
}

func TestResponseIncludesStandaloneReasoningItem(t *testing.T) {
	resp, err := FromIRResponse(ir.Response{
		ID: "resp_1",
		Choices: []ir.Choice{{Message: ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			ir.Thinking("internal reasoning", ""),
			ir.Text("hi"),
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range resp.Output {
		if item.Type == "reasoning" && len(item.Content) == 1 && item.Content[0].Type == "reasoning_text" && item.Content[0].Text == "internal reasoning" {
			return
		}
	}
	t.Fatalf("response missing standalone reasoning item: %#v", resp.Output)
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
	if !strings.Contains(got, `"type":"reasoning"`) || !strings.Contains(got, `"type":"reasoning_text","text":"plan"`) {
		t.Fatalf("reasoning must be emitted as a standalone output item: %s", got)
	}
}

func TestStreamWriterFinalizesTextMessageWithItemID(t *testing.T) {
	var out strings.Builder
	writer := NewStreamWriter(&out)
	for _, event := range []ir.StreamEvent{
		{Type: ir.StreamMessageStart, ID: "resp_1", Model: "gpt-test"},
		{Type: ir.StreamChoiceStart, ChoiceIndex: 0, Role: ir.RoleAssistant},
		{Type: ir.StreamContentBlockStart, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockText},
		{Type: ir.StreamContentBlockDelta, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockText, Delta: "hi"},
		{Type: ir.StreamContentBlockStop, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockText},
		{Type: ir.StreamMessageStop},
	} {
		if err := writer.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	got := out.String()
	for _, want := range []string{`"item_id":"msg_resp_1_0"`, `event: response.output_item.done`, `"content":[{"type":"output_text","text":"hi","annotations":[]}]`, `"output":[`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestStreamWriterEmitsReasoningOutputEvents(t *testing.T) {
	var out strings.Builder
	writer := NewStreamWriter(&out)
	for _, event := range []ir.StreamEvent{
		{Type: ir.StreamMessageStart, ID: "resp_1", Model: "gpt-test"},
		{Type: ir.StreamChoiceStart, ChoiceIndex: 0, Role: ir.RoleAssistant},
		{Type: ir.StreamContentBlockStart, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockThinking},
		{Type: ir.StreamContentBlockDelta, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockThinking, Delta: "plan"},
		{Type: ir.StreamContentBlockStop, ChoiceIndex: 0, BlockIndex: 0, BlockType: ir.BlockThinking},
		{Type: ir.StreamMessageStop},
	} {
		if err := writer.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	got := out.String()
	for _, want := range []string{`"type":"reasoning"`, `"type":"reasoning_text","text":"plan"`, "event: response.reasoning_text.delta", "event: response.reasoning_text.done"} {
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
