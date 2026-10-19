package anthropic

import (
	"encoding/json"
	"localrelay/internal/ir"
	"testing"
)

func TestProviderRequestMultimodalContract(t *testing.T) {
	input := ir.Request{Model: "model", Tools: []ir.Tool{{Name: "search", Parameters: json.RawMessage(`{"type":"object"}`)}}, Messages: []ir.Message{
		{Role: ir.RoleSystem, Content: []ir.ContentBlock{ir.Text("instructions")}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Text("question"), ir.Image("data:image/png;base64,YQ==", ""), ir.Image("https://example.com/image.png", "")}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.Thinking("plan", "signature"), ir.ToolCall("call", "search", nil)}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult("call", "result")}},
	}}
	out, err := ToProviderRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if out.System != "instructions" || len(out.Messages) != 3 || len(out.Tools) != 1 || out.Tools[0].Name != "search" {
		t.Fatal(out)
	}
	blocks := out.Messages[0].Content
	if blocks[1].Source.Type != "base64" || blocks[1].Source.Data != "YQ==" || blocks[2].Source.Type != "url" {
		t.Fatal(blocks)
	}
	if out.Messages[1].Content[0].Signature != "signature" || string(out.Messages[1].Content[1].Input) != "{}" || out.Messages[2].Content[0].ToolUseID != "call" {
		t.Fatal(out)
	}
	for _, bad := range []ir.Request{
		{Tools: []ir.Tool{{Type: "unsupported"}}},
		{Messages: []ir.Message{{Role: "invalid"}}},
		{Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Image("data:image/png;base64,%%%", "")}}}},
		{Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: "unknown"}}}}},
	} {
		if _, err := ToProviderRequest(bad); err == nil {
			t.Fatal("unsupported request accepted")
		}
	}
}

func TestResponsePreservesThinkingToolsAndUsage(t *testing.T) {
	for stop, want := range map[string]string{"end_turn": "stop", "max_tokens": "length", "tool_use": "tool_calls", "other": "other"} {
		raw := `{"id":"r","model":"m","stop_reason":"` + stop + `","content":[{"type":"text","text":"answer"},{"type":"thinking","thinking":"plan","signature":"sig"},{"type":"tool_use","id":"c","name":"search"}],"usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}`
		out, err := ParseResponse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		blocks := out.Choices[0].Message.Content
		if len(blocks) != 3 || blocks[0].Text != "answer" || blocks[1].Signature != "sig" || blocks[2].ToolCallID != "c" || string(blocks[2].Arguments) != "{}" || out.Choices[0].StopReason != want || out.Usage.CacheCreationInputTokens != 1 || out.Usage.CacheReadInputTokens != 2 {
			t.Fatal(out)
		}
	}
	for _, raw := range []string{`{`, `{"content":[{"type":"unknown"}]}`} {
		if _, err := ParseResponse([]byte(raw)); err == nil {
			t.Fatal("bad response accepted")
		}
	}
	out, err := ParseResponse([]byte(`{"content":[]}`))
	if err != nil || out.Usage != (ir.Usage{}) {
		t.Fatal(out, err)
	}
}
