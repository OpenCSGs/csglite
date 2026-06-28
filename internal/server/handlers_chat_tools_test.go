package server

import (
	"encoding/json"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestOllamaMessagesToOpenAI_ToolLoop(t *testing.T) {
	messages := []api.Message{
		{Role: "user", Content: "scan files"},
		{
			Role: "assistant",
			ToolCalls: []api.ToolCall{
				{
					Type: "function",
					Function: api.ToolFunction{
						Name:      "exec",
						Arguments: map[string]interface{}{"command": "pwd"},
					},
				},
			},
		},
		{
			Role:     "tool",
			ToolName: "exec",
			Content:  "/Users/xiangzhen",
		},
	}

	got, err := ollamaMessagesToOpenAI(messages, nil)
	if err != nil {
		t.Fatalf("ollamaMessagesToOpenAI returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}

	assistantCalls, ok := got[1]["tool_calls"].([]map[string]interface{})
	if !ok || len(assistantCalls) != 1 {
		t.Fatalf("expected one assistant tool call, got %#v", got[1]["tool_calls"])
	}
	call := assistantCalls[0]
	if call["id"] == "" {
		t.Fatalf("expected generated tool call id, got %#v", call)
	}
	function, ok := call["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected function payload, got %#v", call["function"])
	}
	if function["name"] != "exec" {
		t.Fatalf("expected exec tool name, got %#v", function["name"])
	}
	if function["arguments"] != "{\"command\":\"pwd\"}" {
		t.Fatalf("unexpected arguments payload: %#v", function["arguments"])
	}

	if got[2]["tool_call_id"] != call["id"] {
		t.Fatalf("expected tool result to reference generated call id, got %#v vs %#v", got[2]["tool_call_id"], call["id"])
	}
}

func TestOllamaMessagesToOpenAI_ToolArgumentsStringIsJSON(t *testing.T) {
	messages := []api.Message{
		{
			Role: "assistant",
			ToolCalls: []api.ToolCall{
				{
					Type: "function",
					Function: api.ToolFunction{
						Name:      "exec",
						Arguments: "pwd",
					},
				},
			},
		},
	}

	tools := []api.Tool{
		{
			Type: "function",
			Function: api.ToolFunction{
				Name: "exec",
				Parameters: map[string]interface{}{
					"type":     "object",
					"required": []interface{}{"command"},
					"properties": map[string]interface{}{
						"command": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	}

	got, err := ollamaMessagesToOpenAI(messages, tools)
	if err != nil {
		t.Fatalf("ollamaMessagesToOpenAI returned error: %v", err)
	}

	assistantCalls, ok := got[0]["tool_calls"].([]map[string]interface{})
	if !ok || len(assistantCalls) != 1 {
		t.Fatalf("expected one assistant tool call, got %#v", got[0]["tool_calls"])
	}
	function, ok := assistantCalls[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected function payload, got %#v", assistantCalls[0]["function"])
	}
	arguments, ok := function["arguments"].(string)
	if !ok {
		t.Fatalf("expected string arguments, got %#v", function["arguments"])
	}
	if !json.Valid([]byte(arguments)) {
		t.Fatalf("expected JSON arguments, got %q", arguments)
	}
	if arguments != `{"command":"pwd"}` {
		t.Fatalf("unexpected arguments payload: %#v", arguments)
	}
}

func TestOpenAIChatResponseToOllama_ToolCalls(t *testing.T) {
	resp := api.OpenAIChatResponse{
		Choices: []api.OpenAIChoice{
			{
				Message: &api.Message{
					Role:    "assistant",
					Content: nil,
					ToolCalls: []api.ToolCall{
						{
							ID:   "call_123",
							Type: "function",
							Function: api.ToolFunction{
								Name:      "exec",
								Arguments: "{\"command\":\"pwd\"}",
							},
						},
					},
				},
			},
		},
	}

	got, err := openAIChatResponseToOllama("Qwen/Qwen3.5-2B", resp)
	if err != nil {
		t.Fatalf("openAIChatResponseToOllama returned error: %v", err)
	}
	if got.Message == nil {
		t.Fatal("expected message in response")
	}
	if content, ok := got.Message.Content.(string); !ok || content != "" {
		t.Fatalf("expected empty string content, got %#v", got.Message.Content)
	}
	if len(got.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", got.Message.ToolCalls)
	}
	call := got.Message.ToolCalls[0]
	if call.Function.Index == nil || *call.Function.Index != 0 {
		t.Fatalf("expected tool call index 0, got %#v", call.Function.Index)
	}
	args, ok := call.Function.Arguments.(map[string]interface{})
	if !ok {
		t.Fatalf("expected decoded arguments map, got %#v", call.Function.Arguments)
	}
	if args["command"] != "pwd" {
		t.Fatalf("expected decoded command, got %#v", args["command"])
	}
}
