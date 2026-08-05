package openairesponses

import (
	"testing"

	"localrelay/internal/ir"
)

func TestParseRequestToIR(t *testing.T) {
	request, err := ParseRequest([]byte(`{
		"model":"relay/gpt",
		"input":[
			{"role":"developer","content":[{"type":"input_text","text":"Be concise."}]},
			{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"https://example.test/a.png","detail":"low"}]},
			{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Shanghai\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"}
		],
		"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}],
		"stream":true,"max_output_tokens":64,"reasoning":{"effort":"medium"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.Model != "relay/gpt" || !request.Stream || request.Params.MaxTokens == nil || *request.Params.MaxTokens != 64 || len(request.Tools) != 1 || len(request.Messages) != 4 {
		t.Fatalf("request = %#v", request)
	}
	if request.Messages[0].Role != "system" || request.Messages[1].Content[1].ImageURL != "https://example.test/a.png" || request.Messages[1].Content[1].Detail != "low" || request.Messages[2].Role != "assistant" || request.Messages[2].Content[0].ToolCallID != "call_1" || request.Messages[3].Role != "tool" || request.Messages[3].Content[0].Result != "sunny" {
		t.Fatalf("messages = %#v", request.Messages)
	}
}

func TestParseRequestAcceptsStringInput(t *testing.T) {
	request, err := ParseRequest([]byte(`{"model":"relay/gpt","input":"hello"}`))
	if err != nil || len(request.Messages) != 1 || request.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("request/err = %#v/%v", request, err)
	}
}

func TestParseRequestAcceptsInstructionsAndStructuredStringContent(t *testing.T) {
	request, err := ParseRequest([]byte(`{"model":"relay/gpt","instructions":"Be concise.","input":[{"role":"user","content":"hello"}]}`))
	if err != nil || len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[0].Content[0].Text != "Be concise." || request.Messages[1].Content[0].Text != "hello" {
		t.Fatalf("request/err = %#v/%v", request, err)
	}
}

func TestParseRequestMapsReasoningEffortWithoutThinkingPayload(t *testing.T) {
	request, err := ParseRequest([]byte(`{"model":"relay/gpt","input":"hello","reasoning":{"effort":"high"}}`))
	if err != nil || request.Params.ReasoningEffort == nil || *request.Params.ReasoningEffort != "high" || len(request.Params.Thinking) != 0 {
		t.Fatalf("request/err = %#v/%v", request, err)
	}
	provider, err := ToProviderRequest(request)
	if err != nil || string(provider.Reasoning) != `{"effort":"high"}` {
		t.Fatalf("provider/err = %#v/%v", provider, err)
	}
}

func TestToProviderRequestRejectsTopK(t *testing.T) {
	topK := 40
	if _, err := ToProviderRequest(ir.Request{Params: ir.Params{TopK: &topK}}); err == nil {
		t.Fatal("expected OpenAI Responses top_k error")
	}
}
