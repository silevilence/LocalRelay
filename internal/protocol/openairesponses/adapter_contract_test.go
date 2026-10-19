package openairesponses

import (
	"encoding/json"
	"localrelay/internal/ir"
	"testing"
)

func TestProviderRequestToolsImagesAndReasoningContract(t *testing.T) {
	effort := "high"
	input := ir.Request{Model: "m", Params: ir.Params{ReasoningEffort: &effort}, Tools: []ir.Tool{{Name: "search"}}, Messages: []ir.Message{
		{Role: ir.RoleSystem, Content: []ir.ContentBlock{ir.Text("instructions")}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Image("https://example.com/image", "high")}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.ToolCall("c", "search", nil)}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult("c", "result")}},
	}}
	out, err := ToProviderRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Reasoning) != `{"effort":"high"}` || out.Tools[0].Name != "search" || out.Input[1].Content[0].ImageURL != "https://example.com/image" {
		t.Fatal(out)
	}
	foundCall, foundResult := false, false
	for _, item := range out.Input {
		if item.Type == "function_call" {
			foundCall = item.CallID == "c" && item.Arguments == "{}"
		}
		if item.Type == "function_call_output" {
			foundResult = item.CallID == "c" && item.Output == "result"
		}
	}
	if !foundCall || !foundResult {
		t.Fatal(out.Input)
	}
	for _, bad := range []ir.Request{
		{Tools: []ir.Tool{{Type: "unsupported"}}},
		{Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: "unknown"}}}}},
	} {
		if _, err := ToProviderRequest(bad); err == nil {
			t.Fatal("unsupported request accepted")
		}
	}
}

func TestResponsePreservesReasoningAndToolContent(t *testing.T) {
	raw := `{"id":"r","model":"m","output":[{"type":"message","content":[{"type":"output_text","text":"answer"},{"type":"reasoning_text","text":"plan"}]},{"type":"reasoning","content":[{"type":"reasoning_text","text":"more"}]},{"type":"function_call","call_id":"c","name":"search","arguments":"{}"}],"usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}`
	out, err := ParseResponse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	blocks := out.Choices[0].Message.Content
	if len(blocks) != 4 || blocks[0].Text != "answer" || blocks[1].Type != ir.BlockThinking || blocks[2].Text != "more" || blocks[3].ToolCallID != "c" || out.Usage.CacheReadInputTokens != 2 {
		t.Fatal(out)
	}
	if _, err := ParseResponse([]byte("{")); err == nil {
		t.Fatal("bad JSON accepted")
	}
	out, err = ParseResponse([]byte(`{}`))
	if err != nil || out.Usage != (ir.Usage{}) {
		t.Fatal(out, err)
	}
}

func TestRequestRejectsMalformedToolAndImageInput(t *testing.T) {
	for _, raw := range []string{
		`{"input":42}`, `{"input":[{"role":"user","content":42}]}`,
		`{"reasoning":"invalid","input":"q"}`, `{"input":[{"role":"invalid","content":"q"}]}`,
		`{"input":[{"role":"user","content":[{"type":"input_image"}]}]}`,
		`{"input":[{"role":"user","content":[{"type":"unsupported"}]}]}`,
		`{"input":[{"type":"function_call","arguments":"{"}]}`,
	} {
		if _, err := ParseRequest([]byte(raw)); err == nil {
			t.Fatal("invalid request accepted", raw)
		}
	}
	out, err := ParseRequest([]byte(`{"reasoning":{},"input":[{"type":"function_call","call_id":"c","name":"search"}]}`))
	if err != nil || out.Params.ReasoningEffort != nil || !json.Valid(out.Messages[0].Content[0].Arguments) {
		t.Fatal(out, err)
	}
}
