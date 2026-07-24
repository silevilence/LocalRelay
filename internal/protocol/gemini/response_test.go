package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"localrelay/internal/ir"
)

func TestFromIRResponse(t *testing.T) {
	resp, err := FromIRResponse(ir.Response{
		ID:    "resp_1",
		Model: "gemini-test",
		Usage: ir.Usage{InputTokens: 8, OutputTokens: 3, CacheReadInputTokens: 2},
		Choices: []ir.Choice{{
			Index:      0,
			StopReason: "length",
			Message: ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				ir.Thinking("plan", "sig"),
				ir.Text("hi"),
				ir.Image("data:image/png;base64,aGk=", ""),
				ir.ToolCall("call_1", "lookup", json.RawMessage(`{"q":"x"}`)),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := resp.Candidates[0]
	if resp.ResponseID != "resp_1" || candidate.FinishReason != "MAX_TOKENS" {
		t.Fatalf("response = %#v", resp)
	}
	if !candidate.Content.Parts[0].Thought || candidate.Content.Parts[0].ThoughtSignature != "sig" {
		t.Fatalf("thinking part = %#v", candidate.Content.Parts[0])
	}
	if candidate.Content.Parts[2].InlineData.MIMEType != "image/png" || candidate.Content.Parts[2].InlineData.Data != "aGk=" {
		t.Fatalf("image part = %#v", candidate.Content.Parts[2])
	}
	if candidate.Content.Parts[3].FunctionCall.Name != "lookup" || resp.UsageMetadata.CachedContentTokenCount != 2 {
		t.Fatalf("tool/usage = %#v/%#v", candidate.Content.Parts[3], resp.UsageMetadata)
	}
}

func TestWriteStreamEvent(t *testing.T) {
	var out strings.Builder
	for _, event := range []ir.StreamEvent{
		{Type: ir.StreamContentBlockDelta, ID: "resp_1", Model: "gemini-test", BlockType: ir.BlockThinking, Delta: "plan"},
		{Type: ir.StreamContentBlockDelta, ID: "resp_1", Model: "gemini-test", BlockType: ir.BlockText, Delta: "hi"},
		{Type: ir.StreamContentBlockStart, ID: "resp_1", Model: "gemini-test", BlockType: ir.BlockToolCall, ToolName: "lookup"},
		{Type: ir.StreamContentBlockDelta, ID: "resp_1", Model: "gemini-test", BlockType: ir.BlockToolCall, ToolName: "lookup", ArgumentsDelta: `{"q":"x"}`},
		{Type: ir.StreamMessageDelta, StopReason: "stop", Usage: ir.Usage{InputTokens: 1, OutputTokens: 2}},
	} {
		if err := WriteStreamEvent(&out, event); err != nil {
			t.Fatal(err)
		}
	}
	got := out.String()
	for _, want := range []string{`"thought":true`, `"text":"hi"`, `"functionCall":{"name":"lookup","args":{"q":"x"}}`, `"finishReason":"STOP"`, `"totalTokenCount":3`} {
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
