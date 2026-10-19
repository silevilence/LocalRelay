package gemini

import (
	"encoding/json"
	"localrelay/internal/ir"
	"testing"
)

func TestProviderRequestMultimodalContract(t *testing.T) {
	input := ir.Request{Tools: []ir.Tool{{Name: "search"}}, Messages: []ir.Message{
		{Role: ir.RoleSystem, Content: []ir.ContentBlock{ir.Text("instructions")}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Image("data:image/png;base64,YQ==", "")}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.Thinking("plan", "sig"), ir.ToolCall("c", "search", nil)}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult("c", "result")}},
	}}
	out, err := ToProviderRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if out.SystemInstruction.Parts[0].Text != "instructions" || len(out.Contents) != 3 || out.Contents[0].Parts[0].InlineData.Data != "YQ==" {
		t.Fatal(out)
	}
	if out.Contents[1].Role != "model" || out.Contents[1].Parts[0].ThoughtSignature != "sig" || out.Contents[1].Parts[1].FunctionCall.Name != "search" {
		t.Fatal(out)
	}
	var result struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(out.Contents[2].Parts[0].FunctionResponse.Response, &result) != nil || result.Result != "result" {
		t.Fatal(out)
	}
	for _, bad := range []ir.Request{
		{Tools: []ir.Tool{{Type: "unsupported"}}},
		{Messages: []ir.Message{{Role: "invalid"}}},
		{Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Image("https://example.com/image", "")}}}},
		{Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Image("data:image/png;base64,%%%", "")}}}},
		{Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: "unknown"}}}}},
	} {
		if _, err := ToProviderRequest(bad); err == nil {
			t.Fatal("unsupported request accepted")
		}
	}
}

func TestResponsePreservesThinkingToolsAndUsage(t *testing.T) {
	for stop, want := range map[string]string{"STOP": "stop", "MAX_TOKENS": "length", "SAFETY": "safety"} {
		raw := `{"responseId":"r","modelVersion":"m","candidates":[{"index":1,"finishReason":"` + stop + `","content":{"parts":[{"text":"answer"},{"thought":true,"text":"plan","thoughtSignature":"sig"},{"functionCall":{"name":"search"}}]}}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"cachedContentTokenCount":2}}`
		out, err := ParseResponse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		blocks := out.Choices[0].Message.Content
		if len(blocks) != 3 || blocks[0].Text != "answer" || blocks[1].Signature != "sig" || blocks[2].ToolName != "search" || string(blocks[2].Arguments) != "{}" || out.Choices[0].StopReason != want || out.Usage.CacheReadInputTokens != 2 {
			t.Fatal(out)
		}
	}
	if _, err := ParseResponse([]byte("{")); err == nil {
		t.Fatal("bad JSON accepted")
	}
	out, err := ParseResponse([]byte(`{}`))
	if err != nil || out.Usage != (ir.Usage{}) {
		t.Fatal(out, err)
	}
}
