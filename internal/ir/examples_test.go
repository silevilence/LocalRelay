package ir_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"localrelay/internal/ir"
)

func TestIRManualExamples(t *testing.T) {
	t.Run("plain text", func(t *testing.T) {
		req := ir.Request{
			Model: "openai/gpt-4.1-mini",
			Messages: []ir.Message{
				{Role: ir.RoleSystem, Content: []ir.ContentBlock{ir.Text("You are concise.")}},
				{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Text("Hello")}},
			},
		}
		requireJSONContains(t, req, `"role":"system"`, `"text":"Hello"`)
	})

	t.Run("tool call", func(t *testing.T) {
		req := ir.Request{
			Model: "openai/gpt-4.1-mini",
			Tools: []ir.Tool{{
				Type:       "function",
				Name:       "get_weather",
				Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
			}},
			Messages: []ir.Message{
				{Role: ir.RoleUser, Content: []ir.ContentBlock{ir.Text("Weather in Shanghai?")}},
				{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
					ir.Thinking("Need current weather.", ""),
					ir.ToolCall("call_1", "get_weather", json.RawMessage(`{"city":"Shanghai"}`)),
				}},
				{Role: ir.RoleTool, Content: []ir.ContentBlock{ir.ToolResult("call_1", `{"tempC":31}`)}},
			},
		}
		requireJSONContains(t, req, `"type":"tool_call"`, `"toolCallId":"call_1"`, `"arguments":{"city":"Shanghai"}`)
	})

	t.Run("multimodal", func(t *testing.T) {
		req := ir.Request{
			Model: "openai/gpt-4.1-mini",
			Messages: []ir.Message{{
				Role: ir.RoleUser,
				Content: []ir.ContentBlock{
					ir.Text("What is in this image?"),
					ir.Image("data:image/png;base64,iVBORw0KGgo=", "low"),
				},
			}},
		}
		requireJSONContains(t, req, `"type":"image"`, `"imageUrl":"data:image/png;base64,iVBORw0KGgo="`, `"detail":"low"`)
	})

	t.Run("response", func(t *testing.T) {
		resp := ir.Response{
			ID:    "chatcmpl_1",
			Model: "openai/gpt-4.1-mini",
			Choices: []ir.Choice{{
				Index:      0,
				StopReason: "end_turn",
				Message:    ir.Message{Role: ir.RoleAssistant, Content: []ir.ContentBlock{ir.Text("Hi")}},
			}},
			Usage: ir.Usage{InputTokens: 3, OutputTokens: 1},
		}
		requireJSONContains(t, resp, `"choices":[`, `"outputTokens":1`)
	})
}

func requireJSONContains(t *testing.T, v any, needles ...string) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range needles {
		if !bytes.Contains(data, []byte(needle)) {
			t.Fatalf("JSON %s does not contain %s", data, needle)
		}
	}
}
