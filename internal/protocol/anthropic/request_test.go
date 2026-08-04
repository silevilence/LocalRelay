package anthropic

import "testing"

func TestParseRequestToIR(t *testing.T) {
	request, err := ParseRequest([]byte(`{
		"model":"relay/claude",
		"max_tokens":128,
		"system":[{"type":"text","text":"Be concise."}],
		"thinking":{"type":"enabled","budget_tokens":32},
		"tools":[{"name":"weather","description":"Gets weather","input_schema":{"type":"object"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"url","url":"https://example.test/a.png"}}]},
			{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"sig"},{"type":"tool_use","id":"tool_1","name":"weather","input":{"city":"Shanghai"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"sunny"}]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.Model != "relay/claude" || request.Params.MaxTokens == nil || *request.Params.MaxTokens != 128 || len(request.Messages) != 4 || len(request.Tools) != 1 {
		t.Fatalf("request = %#v", request)
	}
	if request.Messages[0].Role != "system" || request.Messages[0].Content[0].Text != "Be concise." {
		t.Fatalf("system message = %#v", request.Messages[0])
	}
	if request.Messages[1].Content[1].ImageURL != "https://example.test/a.png" || request.Messages[2].Content[1].ToolName != "weather" || request.Messages[3].Role != "tool" || request.Messages[3].Content[0].Result != "sunny" {
		t.Fatalf("messages = %#v", request.Messages)
	}
}

func TestParseRequestAcceptsStringContent(t *testing.T) {
	request, err := ParseRequest([]byte(`{"model":"relay/claude","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`))
	if err != nil || len(request.Messages) != 1 || request.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("request/err = %#v/%v", request, err)
	}
}

func TestParseRequestKeepsToolResultOrder(t *testing.T) {
	request, err := ParseRequest([]byte(`{"model":"relay/claude","max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"before"},{"type":"tool_result","tool_use_id":"tool_1","content":"result"},{"type":"text","text":"after"}]}]}`))
	if err != nil || len(request.Messages) != 3 || request.Messages[0].Content[0].Text != "before" || request.Messages[1].Role != "tool" || request.Messages[2].Content[0].Text != "after" {
		t.Fatalf("request/err = %#v/%v", request, err)
	}
}
