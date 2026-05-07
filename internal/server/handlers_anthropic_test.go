package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func newAnthropicProxyTestServer(t *testing.T, engine inference.Engine) *Server {
	t.Helper()

	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "test",
		Name:         "model",
		Format:       model.FormatGGUF,
		Size:         1,
		Files:        []string{"model.gguf", "config.json"},
		DownloadedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	s := New(cfg, "test")
	s.engines["test/model"] = &managedEngine{engine: engine, numCtx: defaultAnthropicMaxInputTokens, numParallel: inference.ResolveNumParallel(0)}
	return s
}

func TestAnthropicPreferredNumCtxPromotesLocalModelDefault(t *testing.T) {
	s := newAnthropicProxyTestServer(t, &fakeChatCompletionEngine{})
	s.engines["test/model"].numCtx = 16384

	if got := s.anthropicPreferredNumCtx("test/model"); got != defaultAnthropicMaxInputTokens {
		t.Fatalf("anthropicPreferredNumCtx = %d, want %d", got, defaultAnthropicMaxInputTokens)
	}
}

func TestAnthropicPreferredNumCtxPrefersLargerLoadedEngine(t *testing.T) {
	s := newAnthropicProxyTestServer(t, &fakeChatCompletionEngine{})
	s.engines["test/model"].numCtx = 160000

	if got := s.anthropicPreferredNumCtx("test/model"); got != 160000 {
		t.Fatalf("anthropicPreferredNumCtx = %d, want 160000", got)
	}
}

func TestAnthropicPreferredNumCtxRespectsExplicitEnvOverride(t *testing.T) {
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "24576")
	s := newAnthropicProxyTestServer(t, &fakeChatCompletionEngine{})
	s.engines["test/model"].numCtx = 16384

	if got := s.anthropicPreferredNumCtx("test/model"); got != 24576 {
		t.Fatalf("anthropicPreferredNumCtx = %d, want 24576", got)
	}
}

func TestAnthropicMessagesToOpenAI_ToolLoop(t *testing.T) {
	req := api.AnthropicMessageRequest{
		Model:  "test/model",
		System: []interface{}{map[string]interface{}{"type": "text", "text": "You are helpful."}},
		Messages: []api.AnthropicMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "run pwd"},
				},
			},
			{
				Role: "assistant",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Let me check."},
					map[string]interface{}{
						"type":  "tool_use",
						"id":    "toolu_123",
						"name":  "exec",
						"input": map[string]interface{}{"command": "pwd"},
					},
				},
			},
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": "toolu_123",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "/tmp/project"},
						},
					},
				},
			},
		},
	}

	got, err := anthropicMessagesToOpenAI(req)
	if err != nil {
		t.Fatalf("anthropicMessagesToOpenAI returned error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d: %#v", len(got), got)
	}
	if got[0]["role"] != "system" || got[0]["content"] != "You are helpful." {
		t.Fatalf("unexpected system message: %#v", got[0])
	}
	if got[1]["role"] != "user" || got[1]["content"] != "run pwd" {
		t.Fatalf("unexpected user message: %#v", got[1])
	}
	if got[2]["role"] != "assistant" || got[2]["content"] != "Let me check." {
		t.Fatalf("unexpected assistant message: %#v", got[2])
	}

	assistantCalls, ok := got[2]["tool_calls"].([]map[string]interface{})
	if !ok || len(assistantCalls) != 1 {
		t.Fatalf("expected one assistant tool call, got %#v", got[2]["tool_calls"])
	}
	call := assistantCalls[0]
	if call["id"] != "toolu_123" {
		t.Fatalf("tool call id = %#v, want toolu_123", call["id"])
	}
	function, ok := call["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected function payload, got %#v", call["function"])
	}
	if function["name"] != "exec" {
		t.Fatalf("tool name = %#v, want exec", function["name"])
	}
	if function["arguments"] != "{\"command\":\"pwd\"}" {
		t.Fatalf("arguments = %#v, want JSON payload", function["arguments"])
	}

	if got[3]["role"] != "tool" {
		t.Fatalf("unexpected tool message role: %#v", got[3])
	}
	if got[3]["tool_call_id"] != "toolu_123" {
		t.Fatalf("tool_call_id = %#v, want toolu_123", got[3]["tool_call_id"])
	}
	if got[3]["name"] != "exec" {
		t.Fatalf("tool name = %#v, want exec", got[3]["name"])
	}
	if got[3]["content"] != "/tmp/project" {
		t.Fatalf("tool content = %#v, want /tmp/project", got[3]["content"])
	}
}

func TestAnthropicMessagesToOpenAI_PreservesThinkingAsReasoningContent(t *testing.T) {
	req := api.AnthropicMessageRequest{
		Model: "test/model",
		Messages: []api.AnthropicMessage{
			{
				Role: "assistant",
				Content: []interface{}{
					map[string]interface{}{"type": "thinking", "thinking": "plan the command"},
					map[string]interface{}{"type": "text", "text": "Let me check."},
					map[string]interface{}{
						"type":  "tool_use",
						"id":    "toolu_123",
						"name":  "exec",
						"input": map[string]interface{}{"command": "pwd"},
					},
				},
			},
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": "toolu_123",
						"content":     "ok",
					},
				},
			},
		},
	}

	got, err := anthropicMessagesToOpenAI(req)
	if err != nil {
		t.Fatalf("anthropicMessagesToOpenAI returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d: %#v", len(got), got)
	}
	if got[0]["role"] != "assistant" || got[0]["content"] != "Let me check." {
		t.Fatalf("unexpected assistant message: %#v", got[0])
	}
	if got[0]["reasoning_content"] != "plan the command" {
		t.Fatalf("reasoning_content = %#v, want plan the command", got[0]["reasoning_content"])
	}
	if _, ok := got[0]["tool_calls"]; !ok {
		t.Fatalf("assistant tool_calls missing: %#v", got[0])
	}
	if got[1]["role"] != "tool" || got[1]["content"] != "ok" {
		t.Fatalf("unexpected tool message: %#v", got[1])
	}
}

func TestAnthropicMessagesToInference_PreservesThinkingAsReasoningContent(t *testing.T) {
	req := api.AnthropicMessageRequest{
		Model: "test/model",
		Messages: []api.AnthropicMessage{{
			Role: "assistant",
			Content: []interface{}{
				map[string]interface{}{"type": "thinking", "thinking": "hidden reasoning"},
				map[string]interface{}{"type": "text", "text": "visible answer"},
			},
		}},
	}

	got := anthropicMessagesToInference(req)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %#v", got)
	}
	if got[0].Role != "assistant" || got[0].Content != "visible answer" || got[0].ReasoningContent != "hidden reasoning" {
		t.Fatalf("unexpected inference message: %#v", got[0])
	}
}

func TestAnthropicMessageResponseFromOpenAI_ToolUse(t *testing.T) {
	openAIResp := api.OpenAIChatResponse{
		Model: "test/model",
		Usage: api.OpenAIUsage{
			PromptTokens:     42,
			CompletionTokens: 7,
		},
		Choices: []api.OpenAIChoice{{
			Index: 0,
			Message: &api.Message{
				Role:    "assistant",
				Content: "Checking...",
				ToolCalls: []api.ToolCall{{
					ID:   "call_123",
					Type: "function",
					Function: api.ToolFunction{
						Name:      "exec",
						Arguments: "{\"command\":\"pwd\"}",
					},
				}},
			},
		}},
	}

	got, err := anthropicMessageResponseFromOpenAI("msg_test", "test/model", openAIResp, 5)
	if err != nil {
		t.Fatalf("anthropicMessageResponseFromOpenAI returned error: %v", err)
	}
	if got.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want tool_use", got.StopReason)
	}
	if got.Usage.InputTokens != 42 || got.Usage.OutputTokens != 7 {
		t.Fatalf("unexpected usage: %#v", got.Usage)
	}
	if len(got.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %#v", got.Content)
	}
	if got.Content[0].Type != "text" || got.Content[0].Text != "Checking..." {
		t.Fatalf("unexpected first block: %#v", got.Content[0])
	}
	if got.Content[1].Type != "tool_use" || got.Content[1].ID != "call_123" || got.Content[1].Name != "exec" {
		t.Fatalf("unexpected tool block: %#v", got.Content[1])
	}
	input, ok := got.Content[1].Input.(map[string]interface{})
	if !ok {
		t.Fatalf("expected decoded tool input, got %#v", got.Content[1].Input)
	}
	if input["command"] != "pwd" {
		t.Fatalf("tool input = %#v, want command=pwd", input)
	}
}

func TestAnthropicMessageResponseFromOpenAI_PreservesReasoningContent(t *testing.T) {
	openAIResp := api.OpenAIChatResponse{
		Model: "test/model",
		Choices: []api.OpenAIChoice{{
			Index: 0,
			Message: &api.Message{
				Role:             "assistant",
				ReasoningContent: "hidden reasoning",
				Content:          "visible answer",
				ToolCalls: []api.ToolCall{{
					ID:   "call_123",
					Type: "function",
					Function: api.ToolFunction{
						Name:      "exec",
						Arguments: "{\"command\":\"pwd\"}",
					},
				}},
			},
		}},
	}

	got, err := anthropicMessageResponseFromOpenAI("msg_test", "test/model", openAIResp, 5)
	if err != nil {
		t.Fatalf("anthropicMessageResponseFromOpenAI returned error: %v", err)
	}
	if len(got.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %#v", got.Content)
	}
	if got.Content[0].Type != "thinking" || got.Content[0].Thinking != "hidden reasoning" {
		t.Fatalf("unexpected thinking block: %#v", got.Content[0])
	}
	if got.Content[1].Type != "text" || got.Content[1].Text != "visible answer" {
		t.Fatalf("unexpected text block: %#v", got.Content[1])
	}
	if got.Content[2].Type != "tool_use" || got.Content[2].ID != "call_123" {
		t.Fatalf("unexpected tool block: %#v", got.Content[2])
	}
}

func TestHandleAnthropicMessagesWithTools(t *testing.T) {
	engine := &fakeChatCompletionEngine{
		resp: api.OpenAIChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 123,
			Model:   "test/model",
			Choices: []api.OpenAIChoice{{
				Index: 0,
				Message: &api.Message{
					Role:    "assistant",
					Content: nil,
					ToolCalls: []api.ToolCall{{
						ID:   "call_123",
						Type: "function",
						Function: api.ToolFunction{
							Name:      "exec",
							Arguments: "{\"command\":\"pwd\"}",
						},
					}},
				},
			}},
		},
	}
	s := newAnthropicProxyTestServer(t, engine)

	body := `{
	  "model": "test/model",
	  "messages": [{"role":"user","content":[{"type":"text","text":"run pwd"}]}],
	  "tools": [{
	    "name":"exec",
	    "description":"Run a command",
	    "input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}
	  }],
	  "tool_choice":{"type":"any"},
	  "stream": false
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()

	s.handleAnthropicMessages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}

	var resp api.AnthropicMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" {
		t.Fatalf("unexpected content blocks: %#v", resp.Content)
	}
	if resp.Content[0].Name != "exec" || resp.Content[0].ID != "call_123" {
		t.Fatalf("unexpected tool block: %#v", resp.Content[0])
	}

	if engine.lastReq["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %#v, want required", engine.lastReq["tool_choice"])
	}
	tools, ok := engine.lastReq["tools"].([]api.Tool)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected forwarded tools, got %#v", engine.lastReq["tools"])
	}
	messages, ok := engine.lastReq["messages"].([]map[string]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("expected one forwarded message, got %#v", engine.lastReq["messages"])
	}
	if messages[0]["role"] != "user" || messages[0]["content"] != "run pwd" {
		t.Fatalf("unexpected forwarded message: %#v", messages[0])
	}
}

func TestHandleAnthropicMessagesProxyPreservesReasoningContent(t *testing.T) {
	engine := &fakeChatCompletionEngine{
		resp: api.OpenAIChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 123,
			Model:   "test/model",
			Choices: []api.OpenAIChoice{{
				Index: 0,
				Message: &api.Message{
					Role:             "assistant",
					ReasoningContent: "next hidden reasoning",
					Content:          "next visible answer",
				},
			}},
		},
	}
	s := newAnthropicProxyTestServer(t, engine)

	body := `{
	  "model": "test/model",
	  "messages": [
	    {"role":"assistant","content":[
	      {"type":"thinking","thinking":"previous hidden reasoning"},
	      {"type":"text","text":"previous visible answer"}
	    ]},
	    {"role":"user","content":"continue"}
	  ],
	  "stream": false
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()

	s.handleAnthropicMessages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	messages, ok := engine.lastReq["messages"].([]map[string]interface{})
	if !ok || len(messages) != 2 {
		t.Fatalf("expected two forwarded messages, got %#v", engine.lastReq["messages"])
	}
	if messages[0]["role"] != "assistant" || messages[0]["content"] != "previous visible answer" {
		t.Fatalf("unexpected forwarded assistant message: %#v", messages[0])
	}
	if messages[0]["reasoning_content"] != "previous hidden reasoning" {
		t.Fatalf("forwarded reasoning_content = %#v", messages[0]["reasoning_content"])
	}

	var resp api.AnthropicMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected thinking and text blocks, got %#v", resp.Content)
	}
	if resp.Content[0].Type != "thinking" || resp.Content[0].Thinking != "next hidden reasoning" {
		t.Fatalf("unexpected thinking block: %#v", resp.Content[0])
	}
	if resp.Content[1].Type != "text" || resp.Content[1].Text != "next visible answer" {
		t.Fatalf("unexpected text block: %#v", resp.Content[1])
	}
}

func TestHandleAnthropicMessagesProxyStreamsReasoningContent(t *testing.T) {
	engine := &fakeChatCompletionEngine{
		resp: api.OpenAIChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 123,
			Model:   "test/model",
			Choices: []api.OpenAIChoice{{
				Index: 0,
				Message: &api.Message{
					Role:             "assistant",
					ReasoningContent: "stream hidden reasoning",
					Content:          "stream visible answer",
				},
			}},
		},
	}
	s := newAnthropicProxyTestServer(t, engine)

	body := `{
	  "model": "test/model",
	  "messages": [{"role":"user","content":"hi"}],
	  "stream": true
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()

	s.handleAnthropicMessages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", w.Header().Get("Content-Type"))
	}
	bodyText := w.Body.String()
	if !strings.Contains(bodyText, `"type":"thinking"`) {
		t.Fatalf("expected thinking block in stream, got %s", bodyText)
	}
	if !strings.Contains(bodyText, `"type":"thinking_delta"`) || !strings.Contains(bodyText, `"thinking":"stream hidden reasoning"`) {
		t.Fatalf("expected thinking delta in stream, got %s", bodyText)
	}
	if !strings.Contains(bodyText, `"type":"text_delta"`) || !strings.Contains(bodyText, `"text":"stream visible answer"`) {
		t.Fatalf("expected text delta in stream, got %s", bodyText)
	}
}

func TestHandleAnthropicMessagesStreamWithTools(t *testing.T) {
	engine := &fakeChatCompletionEngine{
		resp: api.OpenAIChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 123,
			Model:   "test/model",
			Choices: []api.OpenAIChoice{{
				Index: 0,
				Message: &api.Message{
					Role:    "assistant",
					Content: "Checking...",
					ToolCalls: []api.ToolCall{{
						ID:   "call_123",
						Type: "function",
						Function: api.ToolFunction{
							Name:      "exec",
							Arguments: "{\"command\":\"pwd\"}",
						},
					}},
				},
			}},
		},
	}
	s := newAnthropicProxyTestServer(t, engine)

	body := `{
	  "model": "test/model",
	  "messages": [{"role":"user","content":"run pwd"}],
	  "tools": [{
	    "name":"exec",
	    "description":"Run a command",
	    "input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}
	  }],
	  "tool_choice":{"type":"auto"},
	  "stream": true
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()

	s.handleAnthropicMessages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", w.Header().Get("Content-Type"))
	}
	bodyText := w.Body.String()
	if !strings.Contains(bodyText, `event: content_block_start`) {
		t.Fatalf("expected content_block_start event, got %s", bodyText)
	}
	if !strings.Contains(bodyText, `"type":"tool_use"`) {
		t.Fatalf("expected tool_use block in stream, got %s", bodyText)
	}
	if !strings.Contains(bodyText, `"type":"input_json_delta"`) {
		t.Fatalf("expected input_json_delta in stream, got %s", bodyText)
	}
	if !strings.Contains(bodyText, `"stop_reason":"tool_use"`) {
		t.Fatalf("expected tool_use stop reason in stream, got %s", bodyText)
	}
}
