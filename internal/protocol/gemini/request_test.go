package gemini

import "testing"

func TestParseRequestToIR(t *testing.T) {
	request, err := ParseRequest([]byte(`{
		"systemInstruction":{"parts":[{"text":"Be concise."}]},
		"generationConfig":{"maxOutputTokens":64,"temperature":0.2,"stopSequences":["END"]},
		"tools":[{"functionDeclarations":[{"name":"weather","description":"Gets weather","parameters":{"type":"object"}}]}],
		"contents":[
			{"role":"user","parts":[{"text":"hello"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]},
			{"role":"model","parts":[{"text":"plan","thought":true,"thoughtSignature":"sig"},{"functionCall":{"name":"weather","args":{"city":"Shanghai"}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"weather","response":{"result":"sunny"}}}]}
		]
	}`), "relay/gemini", false)
	if err != nil {
		t.Fatal(err)
	}
	if request.Model != "relay/gemini" || request.Stream || request.Params.MaxTokens == nil || *request.Params.MaxTokens != 64 || len(request.Tools) != 1 || len(request.Messages) != 4 {
		t.Fatalf("request = %#v", request)
	}
	if request.Messages[0].Role != "system" || request.Messages[1].Content[1].ImageURL != "data:image/png;base64,aGVsbG8=" || request.Messages[2].Content[0].Type != "thinking" || request.Messages[2].Content[1].ToolName != "weather" || request.Messages[3].Role != "tool" || request.Messages[3].Content[0].Result != "sunny" {
		t.Fatalf("messages = %#v", request.Messages)
	}
}

func TestParseRequestKeepsFunctionResponseOrder(t *testing.T) {
	request, err := ParseRequest([]byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"name":"weather","args":{}}}]},{"role":"user","parts":[{"text":"before"},{"functionResponse":{"name":"weather","response":{"result":"sunny"}}},{"text":"after"}]}]}`), "relay/gemini", false)
	if err != nil || len(request.Messages) != 4 || request.Messages[1].Content[0].Text != "before" || request.Messages[2].Role != "tool" || request.Messages[2].Content[0].ToolCallID != "gemini-call-1" || request.Messages[3].Content[0].Text != "after" {
		t.Fatalf("request/err = %#v/%v", request, err)
	}
}
