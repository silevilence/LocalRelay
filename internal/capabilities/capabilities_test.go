package capabilities

import "testing"

func TestParseValidateAndDefaults(t *testing.T) {
	cfg, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Protocol != ProtocolOpenAIChat {
		t.Fatalf("empty config = %#v", cfg)
	}
	if err := Validate(`{"protocol":"openai_chat"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(`{`); err == nil {
		t.Fatal("expected json error")
	}
	if _, err := Parse(`{"thinking":{}}`); err == nil {
		t.Fatal("expected missing protocol error")
	}
	if _, err := Parse(`{"protocol":"wat"}`); err == nil {
		t.Fatal("expected unsupported protocol error")
	}
	if _, err := Parse(`{"protocol":"openai_chat","thinking":{"requestMessageField":"thinking_content"}}`); err == nil {
		t.Fatal("expected unsupported request message field error")
	}
	if _, err := Parse(`{"protocol":"openai_chat","thinking":{"responseContentField":"thinking_content"}}`); err == nil {
		t.Fatal("expected unsupported response content field error")
	}
	if got := DefaultJSON("missing"); got == "" || got == "{}" {
		t.Fatalf("fallback default = %q", got)
	}
	if cfg, err := Parse(DefaultJSON("gemini")); err != nil || cfg.Protocol != ProtocolGemini {
		t.Fatalf("gemini default = %#v/%v", cfg, err)
	}
	if cfg, err := Parse(DefaultJSON("volcengine-coding")); err != nil || !cfg.Streaming.IncludeUsage {
		t.Fatalf("volcengine streaming default = %#v/%v", cfg, err)
	}
}

func TestCapabilitySupportChecks(t *testing.T) {
	cfg, err := Parse(DefaultJSON("deepseek"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SupportsRequestField("thinking") {
		t.Fatal("deepseek should support thinking")
	}
	if !cfg.SupportsTopK() {
		t.Fatal("deepseek should support top_k")
	}
	if cfg.Thinking.RequestMessageField != "reasoning_content" || !cfg.Thinking.DefaultEnabled || !cfg.ToolCalls.RequireAssistantContent || !cfg.ToolCalls.RequireReasoningContent {
		t.Fatalf("deepseek history/tool config = %#v", cfg)
	}
	if !cfg.SupportsReasoningEffort("high") {
		t.Fatal("deepseek should support high reasoning effort")
	}
	if (Provider{}).SupportsReasoningEffort("high") {
		t.Fatal("empty config should not support reasoning effort")
	}
	if !cfg.SupportsReasoningEffort("xhigh") {
		t.Fatal("deepseek should support mapped xhigh reasoning effort")
	}
	if cfg.MapReasoningEffort("xhigh") != "max" {
		t.Fatalf("xhigh mapped to %q", cfg.MapReasoningEffort("xhigh"))
	}
	openai, err := Parse(DefaultJSON("openai"))
	if err != nil {
		t.Fatal(err)
	}
	if openai.MapReasoningEffort("high") != "high" {
		t.Fatalf("openai high mapped to %q", openai.MapReasoningEffort("high"))
	}
	if openai.SupportsTopK() {
		t.Fatal("OpenAI standard preset should not support top_k")
	}
	if cfg.SupportsReasoningEffort("extreme") {
		t.Fatal("unexpected reasoning effort support")
	}
	if cfg.UnsupportedFieldError("x") == nil {
		t.Fatal("expected unsupported field error")
	}
}

func TestApplyModelCapabilityOverrides(t *testing.T) {
	base, err := Parse(DefaultJSON("openai-compatible"))
	if err != nil {
		t.Fatal(err)
	}
	model := `{
		"tools": true,
		"thinking": true,
		"allowThinkingDowngrade": true,
		"providerCapabilityOverrides": {
			"thinking": {
				"requestFields": ["thinking"],
				"requestMessageField": "reasoning_content",
				"responseContentField": "reasoning_content",
				"defaultEnabled": true
			},
			"toolCalls": {
				"requireAssistantContent": true,
				"requireReasoningContent": true
			}
		}
	}`
	merged, err := ApplyModel(base, model)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Protocol != ProtocolOpenAIChat || !merged.SupportsRequestField("thinking") || merged.Thinking.RequestMessageField != ThinkingFieldReasoningContent || !merged.Thinking.DefaultEnabled || !merged.ToolCalls.RequireAssistantContent || !merged.ToolCalls.RequireReasoningContent || !merged.AllowThinkingDowngrade {
		t.Fatalf("merged model capabilities = %#v", merged)
	}
	if base.Thinking.RequestMessageField != "" || base.ToolCalls.RequireReasoningContent || base.AllowThinkingDowngrade {
		t.Fatalf("base capabilities were mutated = %#v", base)
	}
}

func TestValidateModelCapabilities(t *testing.T) {
	for _, raw := range []string{
		`{`,
		`{"allowThinkingDowngrade":"yes"}`,
		`{"providerCapabilityOverrides":[]}`,
		`{"providerCapabilityOverrides":{"thinking":{"requestMessageField":"unsupported"}}}`,
	} {
		if err := ValidateModel(raw); err == nil {
			t.Fatalf("expected invalid model capabilities for %s", raw)
		}
	}
	if err := ValidateModel(`{"tools":true}`); err != nil {
		t.Fatal(err)
	}
}
