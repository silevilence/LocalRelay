package openaichat

import (
	"encoding/json"
	"strings"
	"testing"

	"localrelay/internal/capabilities"
	"localrelay/internal/ir"
)

func TestParseTextChatToIR(t *testing.T) {
	temp := 0.2
	req, err := Request{
		Model:       "gpt-4.1-mini",
		Temperature: &temp,
		Messages: []Message{
			{Role: "system", Content: "You are concise."},
			{Role: "user", Content: "Hello"},
		},
	}.ToIR()
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-4.1-mini" || req.Params.Temperature == nil || *req.Params.Temperature != temp {
		t.Fatalf("request = %#v", req)
	}
	if got := req.Messages[1].Content[0].Text; got != "Hello" {
		t.Fatalf("user text = %q", got)
	}
}

func TestParseToolsAndToolCallsToIR(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-4.1-mini",
		"tools":[{"type":"function","function":{"name":"get_weather","description":"Weather","parameters":{"type":"object"}}}],
		"messages":[
			{"role":"user","content":"Weather?"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Shanghai\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"31C"}
		]
	}`)
	req, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools = %#v", req.Tools)
	}
	call := req.Messages[1].Content[0]
	if call.Type != ir.BlockToolCall || call.ToolCallID != "call_1" || call.ToolName != "get_weather" {
		t.Fatalf("tool call = %#v", call)
	}
	result := req.Messages[2].Content[0]
	if result.Type != ir.BlockToolResult || result.Result != "31C" {
		t.Fatalf("tool result = %#v", result)
	}
}

func TestParseMultimodalChatToIR(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-4.1-mini",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"Describe this"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,abc","detail":"low"}}
		]}]
	}`)
	req, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	blocks := req.Messages[0].Content
	if len(blocks) != 2 || blocks[1].Type != ir.BlockImage || blocks[1].Detail != "low" {
		t.Fatalf("content blocks = %#v", blocks)
	}
}

func TestIRToOpenAICompatibleProviderRequest(t *testing.T) {
	maxTokens := 128
	req := ir.Request{
		Model: "deepseek-chat",
		Params: ir.Params{
			MaxTokens: &maxTokens,
			Stop:      []string{"END"},
		},
		Tools: []ir.Tool{{
			Type:       "function",
			Name:       "get_weather",
			Parameters: json.RawMessage(`{"type":"object"}`),
		}},
		Messages: []ir.Message{
			{Role: ir.RoleSystem, Content: []ir.ContentBlock{ir.Text("You are concise.")}},
			{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Text("Look"), ir.Image("https://example.test/a.png", "low")}},
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				ir.Thinking("Need a tool.", ""),
				ir.ToolCall("call_1", "get_weather", json.RawMessage(`{"city":"Shanghai"}`)),
			}},
			{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult("call_1", "31C")}},
		},
	}

	out, err := ToProviderRequest(req, mustCapabilities(t, capabilities.DefaultJSON("deepseek")))
	if err != nil {
		t.Fatal(err)
	}
	if out.Model != "deepseek-chat" || out.MaxTokens == nil || *out.MaxTokens != 128 || out.Stop != "END" {
		t.Fatalf("provider request = %#v", out)
	}
	if len(out.Tools) != 1 || out.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("tools = %#v", out.Tools)
	}
	if _, ok := out.Messages[1].Content.([]ContentPart); !ok {
		t.Fatalf("multimodal content = %#v", out.Messages[1].Content)
	}
	if len(out.Messages[2].ToolCalls) != 1 || out.Messages[2].ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls = %#v", out.Messages[2].ToolCalls)
	}
	if out.Messages[2].ToolCalls[0].Function.Arguments != `{"city":"Shanghai"}` {
		t.Fatalf("tool arguments = %#v", out.Messages[2].ToolCalls[0].Function.Arguments)
	}
	if out.Messages[3].ToolCallID != "call_1" || out.Messages[3].Content != "31C" {
		t.Fatalf("tool message = %#v", out.Messages[3])
	}

	if out.Messages[2].ReasoningContent != "Need a tool." || out.Messages[2].Content != "" {
		t.Fatalf("deepseek assistant tool message = %#v", out.Messages[2])
	}
}

func TestConfiguredProviderFields(t *testing.T) {
	effort := "xhigh"
	thinking := json.RawMessage(`{"type":"enabled"}`)
	enableThinking := false
	budget := 4096

	deepseek, err := ToProviderRequest(ir.Request{Params: ir.Params{
		ReasoningEffort: &effort,
		Thinking:        thinking,
	}}, mustCapabilities(t, capabilities.DefaultJSON("deepseek")))
	if err != nil {
		t.Fatal(err)
	}
	if deepseek.ReasoningEffort == nil || *deepseek.ReasoningEffort != "max" || string(deepseek.Thinking) != string(thinking) {
		t.Fatalf("deepseek configured fields = %#v", deepseek)
	}

	siliconflow, err := ToProviderRequest(ir.Request{Params: ir.Params{
		EnableThinking: &enableThinking,
		ThinkingBudget: &budget,
	}}, mustCapabilities(t, capabilities.DefaultJSON("siliconflow")))
	if err != nil {
		t.Fatal(err)
	}
	if siliconflow.EnableThinking == nil || *siliconflow.EnableThinking || siliconflow.ThinkingBudget == nil || *siliconflow.ThinkingBudget != 4096 {
		t.Fatalf("siliconflow configured fields = %#v", siliconflow)
	}

	if _, err := ToProviderRequest(ir.Request{Params: ir.Params{Thinking: thinking}}, mustCapabilities(t, capabilities.DefaultJSON("openai"))); err == nil {
		t.Fatal("expected unsupported thinking field error")
	}
	if _, err := ToProviderRequest(ir.Request{Params: ir.Params{ThinkingBudget: &budget}}, mustCapabilities(t, capabilities.DefaultJSON("openai"))); err == nil {
		t.Fatal("expected unsupported thinking_budget field error")
	}
}

func TestConfiguredReasoningContentResponse(t *testing.T) {
	resp, err := ParseResponseWithCapabilities([]byte(`{
		"model":"deepseek-v4-pro",
		"choices":[{"index":0,"message":{"role":"assistant","content":"done","reasoning_content":"think"}}]
	}`), mustCapabilities(t, capabilities.DefaultJSON("deepseek")))
	if err != nil {
		t.Fatal(err)
	}
	blocks := resp.Choices[0].Message.Content
	if len(blocks) != 2 || blocks[1].Type != ir.BlockThinking || blocks[1].Text != "think" {
		t.Fatalf("response blocks = %#v", blocks)
	}
	out, err := FromIRResponseWithCapabilities(resp, mustCapabilities(t, capabilities.DefaultJSON("deepseek")))
	if err != nil {
		t.Fatal(err)
	}
	if out.Choices[0].Message.ReasoningContent != "think" {
		t.Fatalf("client response = %#v", out.Choices[0].Message)
	}
}

func TestConfiguredReasoningContentHistory(t *testing.T) {
	cfg := mustCapabilities(t, capabilities.DefaultJSON("deepseek"))
	req, err := Request{Messages: []Message{{
		Role:             "assistant",
		Content:          "using a tool",
		ReasoningContent: "need tool",
		ToolCalls: []ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: FunctionCall{Name: "lookup", Arguments: `{}`},
		}},
	}}}.ToIRWithCapabilities(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages[0].Content) != 3 || req.Messages[0].Content[1].Type != ir.BlockThinking {
		t.Fatalf("ir history = %#v", req.Messages[0].Content)
	}
	out, err := ToProviderRequest(req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out.Messages[0].ReasoningContent != "need tool" || out.Messages[0].Content != "using a tool" {
		t.Fatalf("provider history = %#v", out.Messages[0])
	}
}

func TestOpenAIConfigDropsThinkingHistory(t *testing.T) {
	out, err := ToProviderRequest(ir.Request{Messages: []ir.Message{{
		Role: ir.RoleAssistant,
		Content: []ir.ContentBlock{
			ir.Thinking("private", ""),
			ir.Text("answer"),
		},
	}}}, mustCapabilities(t, capabilities.DefaultJSON("openai")))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") {
		t.Fatalf("thinking leaked into generic OpenAI request: %s", data)
	}
}

func TestProviderResponseRoundTripsThroughIR(t *testing.T) {
	raw := []byte(`{
		"id":"chatcmpl_1",
		"model":"gpt-test",
		"choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":2}}
	}`)
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "chatcmpl_1" || resp.Usage.InputTokens != 7 || resp.Usage.CacheReadInputTokens != 2 {
		t.Fatalf("ir response = %#v", resp)
	}
	resp.Model = "openai/gpt-test"
	out, err := FromIRResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if out.Model != "openai/gpt-test" || out.Usage.TotalTokens != 10 {
		t.Fatalf("client response = %#v", out)
	}
	if out.Choices[0].Message.Content != "Hello" || out.Choices[0].FinishReason != "stop" {
		t.Fatalf("choice = %#v", out.Choices[0])
	}
}

func TestCompatibilityWrappers(t *testing.T) {
	resp, err := (Response{
		Model: "m",
		Choices: []ResponseChoice{{
			Index:   0,
			Message: Message{Role: "assistant", Content: "ok"},
		}},
	}).ToIR()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content[0].Text != "ok" {
		t.Fatalf("response wrapper = %#v", resp)
	}

	msg, err := (Message{Role: "user", Content: "hello"}).toIR()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content[0].Text != "hello" {
		t.Fatalf("message wrapper = %#v", msg)
	}

	out, err := messageFromIR(ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.Text("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "hi" {
		t.Fatalf("from ir wrapper = %#v", out)
	}
}

func TestParseRejectsBadInputs(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "bad json", raw: []byte(`{`)},
		{name: "bad role", raw: []byte(`{"messages":[{"role":"developer","content":"x"}]}`)},
		{name: "bad tool type", raw: []byte(`{"tools":[{"type":"web_search","function":{"name":"x"}}]}`)},
		{name: "bad tool call type", raw: []byte(`{"messages":[{"role":"assistant","tool_calls":[{"type":"web_search","function":{"name":"x","arguments":"{}"}}]}]}`)},
		{name: "bad arguments json", raw: []byte(`{"messages":[{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"x","arguments":"not json"}}]}]}`)},
		{name: "bad content part", raw: []byte(`{"messages":[{"role":"user","content":["nope"]}]}`)},
		{name: "bad image part", raw: []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"nope"}]}]}`)},
		{name: "unknown content part", raw: []byte(`{"messages":[{"role":"user","content":[{"type":"audio"}]}]}`)},
		{name: "bad content shape", raw: []byte(`{"messages":[{"role":"user","content":42}]}`)},
		{name: "bad tool content", raw: []byte(`{"messages":[{"role":"tool","tool_call_id":"call_1","content":["nope"]}]}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(tt.raw); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseResponseRejectsBadJSON(t *testing.T) {
	if _, err := ParseResponseWithCapabilities([]byte(`{`), mustCapabilities(t, capabilities.DefaultJSON("openai"))); err == nil {
		t.Fatal("expected bad response json error")
	}
}

func TestParseStopAndArgumentShapes(t *testing.T) {
	req, err := Request{
		Stop: []string{"END", ""},
		Messages: []Message{{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: FunctionCall{Name: "empty", Arguments: ""},
			}},
		}},
	}.ToIR()
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Params.Stop) != 1 || req.Params.Stop[0] != "END" {
		t.Fatalf("stop = %#v", req.Params.Stop)
	}
	if string(req.Messages[0].Content[0].Arguments) != "{}" {
		t.Fatalf("arguments = %s", req.Messages[0].Content[0].Arguments)
	}

	req, err = Request{
		Stop: []any{"A", "", "B"},
		Messages: []Message{{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: FunctionCall{Name: "object", Arguments: map[string]any{"x": float64(1)}},
			}},
		}},
	}.ToIR()
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Params.Stop) != 2 || req.Params.Stop[1] != "B" {
		t.Fatalf("stop = %#v", req.Params.Stop)
	}
	if string(req.Messages[0].Content[0].Arguments) != `{"x":1}` {
		t.Fatalf("arguments = %s", req.Messages[0].Content[0].Arguments)
	}
}

func TestToProviderRejectsBadIR(t *testing.T) {
	tests := []struct {
		name string
		req  ir.Request
		prov capabilities.Provider
	}{
		{name: "bad provider", prov: capabilities.Provider{Protocol: "x"}},
		{name: "bad tool type", prov: mustCapabilities(t, capabilities.DefaultJSON("openai")), req: ir.Request{Tools: []ir.Tool{{Type: "web_search"}}}},
		{name: "bad role", prov: mustCapabilities(t, capabilities.DefaultJSON("openai")), req: ir.Request{Messages: []ir.Message{{Role: ir.Role("developer")}}}},
		{name: "bad block type", prov: mustCapabilities(t, capabilities.DefaultJSON("openai")), req: ir.Request{Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockType("audio")}}}}}},
		{name: "bad arguments", prov: mustCapabilities(t, capabilities.DefaultJSON("openai")), req: ir.Request{Messages: []ir.Message{{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.ToolCall("call_1", "x", json.RawMessage(`nope`))}}}}},
		{name: "tool result on assistant", prov: mustCapabilities(t, capabilities.DefaultJSON("openai")), req: ir.Request{Messages: []ir.Message{{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.ToolResult("call_1", "x")}}}}},
		{name: "tool without result", prov: mustCapabilities(t, capabilities.DefaultJSON("openai")), req: ir.Request{Messages: []ir.Message{{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.Text("x")}}}}},
		{name: "two tool results", prov: mustCapabilities(t, capabilities.DefaultJSON("openai")), req: ir.Request{Messages: []ir.Message{{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult("a", "1"), ir.ToolResult("b", "2")}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ToProviderRequest(tt.req, tt.prov); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestAssistantImageWithToolCallDropsImageForProvider(t *testing.T) {
	out, err := ToProviderRequest(ir.Request{Messages: []ir.Message{{
		Role: ir.RoleAssistant,
		Content: []ir.ContentBlock{
			ir.Text("using a tool"),
			ir.Image("https://example.test/a.png", "low"),
			ir.ToolCall("call_1", "x", nil),
		},
	}}}, mustCapabilities(t, capabilities.DefaultJSON("openai")))
	if err != nil {
		t.Fatal(err)
	}
	if out.Messages[0].Content != "using a tool" {
		t.Fatalf("assistant content = %#v", out.Messages[0].Content)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "image_url") || strings.Contains(string(data), "a.png") {
		t.Fatalf("image leaked into assistant tool_call message: %s", data)
	}
	if out.Messages[0].ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("default arguments = %#v", out.Messages[0].ToolCalls[0].Function.Arguments)
	}
}

func mustCapabilities(t *testing.T, raw string) capabilities.Provider {
	t.Helper()
	cfg, err := capabilities.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
