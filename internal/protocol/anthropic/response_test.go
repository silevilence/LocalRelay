package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"localrelay/internal/ir"
)

func TestFromIRResponse(t *testing.T) {
	resp, err := FromIRResponse(ir.Response{
		ID:    "msg_1",
		Model: "claude-test",
		Usage: ir.Usage{InputTokens: 5, OutputTokens: 2, CacheReadInputTokens: 1},
		Choices: []ir.Choice{{
			StopReason: "tool_calls",
			Message: ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				ir.Thinking("plan", "sig"),
				ir.Text("hi"),
				ir.ToolCall("toolu_1", "lookup", json.RawMessage(`{"q":"x"}`)),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Type != "message" || resp.Role != "assistant" || resp.StopReason != "tool_use" {
		t.Fatalf("response = %#v", resp)
	}
	if resp.Content[0].Thinking != "plan" || resp.Content[0].Signature != "sig" {
		t.Fatalf("thinking = %#v", resp.Content[0])
	}
	if string(resp.Content[2].Input) != `{"q":"x"}` || resp.Usage.CacheReadInputTokens != 1 {
		t.Fatalf("tool/usage = %#v/%#v", resp.Content[2], resp.Usage)
	}
}

func TestFromIRResponseRejectsUnsupported(t *testing.T) {
	_, err := FromIRResponse(ir.Response{Choices: []ir.Choice{
		{Message: ir.Message{Role: ir.RoleAssistant}},
		{Message: ir.Message{Role: ir.RoleAssistant}},
	}})
	if err == nil {
		t.Fatal("expected multiple-choice error")
	}
	_, err = FromIRResponse(ir.Response{Choices: []ir.Choice{{
		Message: ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.Image("https://example.test/a.png", "")}},
	}}})
	if err == nil {
		t.Fatal("expected image error")
	}
}

func TestWriteStreamEvent(t *testing.T) {
	var out strings.Builder
	events := []ir.StreamEvent{
		{Type: ir.StreamMessageStart, ID: "msg_1", Model: "claude-test"},
		{Type: ir.StreamContentBlockStart, BlockIndex: 0, BlockType: ir.BlockText},
		{Type: ir.StreamContentBlockDelta, BlockIndex: 0, BlockType: ir.BlockText, Delta: "hi"},
		{Type: ir.StreamContentBlockStop, BlockIndex: 0},
		{Type: ir.StreamContentBlockStart, BlockIndex: 1, BlockType: ir.BlockToolCall, ToolCallID: "toolu_1", ToolName: "lookup"},
		{Type: ir.StreamContentBlockDelta, BlockIndex: 1, BlockType: ir.BlockToolCall, ArgumentsDelta: `{"q"`},
		{Type: ir.StreamMessageDelta, StopReason: "tool_calls", Usage: ir.Usage{OutputTokens: 2}},
		{Type: ir.StreamMessageStop},
	}
	for _, event := range events {
		if err := WriteStreamEvent(&out, event); err != nil {
			t.Fatal(err)
		}
	}
	got := out.String()
	for _, want := range []string{"event: message_start", `"usage":{}`, `"type":"text_delta"`, `"text":"hi"`, `"type":"input_json_delta"`, `"partial_json":"{\"q\""`, `"stop_reason":"tool_use"`, "event: message_stop"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestForEachStreamEventIgnoresPing(t *testing.T) {
	events, err := ParseStream(strings.NewReader(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-test"}}`,
		``,
		`event: ping`,
		`data: {"type":"ping"}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[2].Type != ir.StreamContentBlockDelta || events[2].Delta != "hi" {
		t.Fatalf("events = %#v", events)
	}
}

func TestParseStreamSkipsAnthropicMetadataAndUnknowns(t *testing.T) {
	events, err := ParseStream(strings.NewReader(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-test"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"citations_delta","citation":{"type":"char_location"}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"x\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":2}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"x\"}"}}`,
		``,
		`event: future_event`,
		`data: {"type":"future_event","value":true}`,
		``,
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !hasAnthropicDelta(events, ir.BlockThinking, "plan", "") || !hasAnthropicDelta(events, ir.BlockText, "hi", "") {
		t.Fatalf("missing content deltas: %#v", events)
	}
	if hasAnthropicDelta(events, ir.BlockToolCall, "", `{"query":"x"}`) {
		t.Fatalf("server tool input should not become client tool call: %#v", events)
	}
	if !hasAnthropicDelta(events, ir.BlockToolCall, "", `{"q":"x"}`) {
		t.Fatalf("missing client tool input delta: %#v", events)
	}
}

func hasAnthropicDelta(events []ir.StreamEvent, blockType ir.BlockType, delta, args string) bool {
	for _, event := range events {
		if event.Type == ir.StreamContentBlockDelta && event.BlockType == blockType && event.Delta == delta && event.ArgumentsDelta == args {
			return true
		}
	}
	return false
}
