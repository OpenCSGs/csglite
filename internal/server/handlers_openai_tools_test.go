package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestOpenAIChatLegacyFunctionsNormalizedToTools(t *testing.T) {
	engine := &fakeChatCompletionEngine{resp: api.OpenAIChatResponse{
		ID: "chatcmpl-legacy", Object: "chat.completion", Created: 1, Model: "test/model",
		Choices: []api.OpenAIChoice{{
			Index: 0,
			Message: &api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
				Index: intPointer(0), ID: "call_1", Type: "function",
				Function: api.ToolFunction{Name: "get_time", Arguments: `{}`},
			}}},
			FinishReason: stringPointer("tool_calls"),
		}},
	}}
	s := newTestServerWithConfig(t, &config.Config{ModelDir: t.TempDir()})
	s.engines["test/model"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1}

	body := `{
	  "model": "test/model",
	  "messages": [
	    {"role":"user","content":"time?"},
	    {"role":"assistant","tool_calls":[{"id":"call_0","type":"function","function":{"name":"get_time","arguments":"{}"}}]},
	    {"role":"tool","tool_call_id":"call_0","name":"get_time","content":"12:00"}
	  ],
	  "functions": [{"name":"get_time","description":"Get time","parameters":{"type":"object"}}],
	  "function_call": {"name":"get_time"},
	  "stream": false
	}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleOpenAIChatCompletions(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	tools, ok := engine.lastReq["tools"].([]api.Tool)
	if !ok || len(tools) != 1 || tools[0].Function.Name != "get_time" {
		t.Fatalf("legacy functions not normalized to tools: %#v", engine.lastReq["tools"])
	}
	choice, ok := engine.lastReq["tool_choice"].(map[string]interface{})
	if !ok || choice["type"] != "function" {
		t.Fatalf("legacy function_call not normalized: %#v", engine.lastReq["tool_choice"])
	}
	if _, forwarded := engine.lastReq["functions"]; forwarded {
		t.Fatalf("legacy functions forwarded alongside tools: %#v", engine.lastReq)
	}

	messages, _ := engine.lastReq["messages"].([]map[string]interface{})
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", engine.lastReq["messages"])
	}
	if _, ok := messages[1]["tool_calls"]; !ok {
		t.Fatalf("assistant tool_calls dropped: %#v", messages[1])
	}
	if messages[2]["tool_call_id"] != "call_0" || messages[2]["role"] != "tool" {
		t.Fatalf("tool result history dropped: %#v", messages[2])
	}
}

func TestOpenAIChatNativeToolStreamPreservesDeltas(t *testing.T) {
	base := &fakeChatCompletionEngine{streamBody: strings.Join([]string{
		`data: {"id":"chatcmpl-s","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_time","arguments":""}}]}}]}`,
		``,
		`data: {"id":"chatcmpl-s","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")}
	engine := &fakeNativeToolStreamingEngine{fakeChatCompletionEngine: base}
	s := newTestServerWithConfig(t, &config.Config{ModelDir: t.TempDir()})
	s.engines["test/model"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1}

	body := `{
	  "model":"test/model",
	  "messages":[{"role":"user","content":"time?"}],
	  "tools":[{"type":"function","function":{"name":"get_time","parameters":{"type":"object"}}}],
	  "tool_choice":"auto",
	  "parallel_tool_calls":true,
	  "stream":true
	}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleOpenAIChatCompletions(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	stream := recorder.Body.String()
	for _, want := range []string{`"index":0`, `"id":"call_a"`, `"name":"get_time"`, `"arguments":"{}"`} {
		if !strings.Contains(stream, want) {
			t.Fatalf("stream missing %q:\n%s", want, stream)
		}
	}
	if base.lastReq["parallel_tool_calls"] != true {
		t.Fatalf("parallel_tool_calls = %#v", base.lastReq["parallel_tool_calls"])
	}
}

func TestOpenAIChatNonStreamToolResponsePreserved(t *testing.T) {
	engine := &fakeChatCompletionEngine{resp: api.OpenAIChatResponse{
		ID: "chatcmpl-tools", Object: "chat.completion", Created: 1, Model: "test/model",
		Choices: []api.OpenAIChoice{{
			Index: 0,
			Message: &api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
				Index: intPointer(0), ID: "call_1", Type: "function",
				Function: api.ToolFunction{Name: "get_time", Arguments: `{"tz":"UTC"}`},
			}}},
			FinishReason: stringPointer("tool_calls"),
		}},
	}}
	s := newTestServerWithConfig(t, &config.Config{ModelDir: t.TempDir()})
	s.engines["test/model"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1}

	body := `{"model":"test/model","messages":[{"role":"user","content":"time?"}],"tools":[{"type":"function","function":{"name":"get_time","parameters":{"type":"object"}}}],"stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleOpenAIChatCompletions(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response api.OpenAIChatResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || len(response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool calls lost: %+v", response)
	}
	call := response.Choices[0].Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "get_time" || call.Function.Arguments != `{"tz":"UTC"}` {
		t.Fatalf("tool call = %+v", call)
	}
	if call.Index == nil || *call.Index != 0 {
		t.Fatalf("tool call index lost: %+v", call)
	}
}

func intPointer(v int) *int          { return &v }
func stringPointer(v string) *string { return &v }

func TestLegacyFunctionHistoryDecodesAndResponseRoundTrips(t *testing.T) {
	engine := &fakeChatCompletionEngine{resp: api.OpenAIChatResponse{
		ID: "chatcmpl-legacy-rt", Object: "chat.completion", Created: 1, Model: "test/model",
		Choices: []api.OpenAIChoice{{
			Index: 0,
			Message: &api.Message{Role: "assistant", FunctionCall: &api.OpenAIFunctionCall{
				Name: "get_time", Arguments: `{"tz":"UTC"}`,
			}},
			FinishReason: stringPointer("function_call"),
		}},
	}}
	s := newTestServerWithConfig(t, &config.Config{ModelDir: t.TempDir()})
	s.engines["test/model"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1}

	body := `{
	  "model":"test/model",
	  "messages":[
	    {"role":"user","content":"time?"},
	    {"role":"assistant","content":null,"function_call":{"name":"get_time","arguments":"{}"}},
	    {"role":"function","name":"get_time","content":"12:00"}
	  ],
	  "functions":[{"name":"get_time","description":"Get time","parameters":{"type":"object"}}],
	  "function_call":"auto",
	  "stream":false
	}`
	recorder := httptest.NewRecorder()
	s.handleOpenAIChatCompletions(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	messages, _ := engine.lastReq["messages"].([]map[string]interface{})
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", engine.lastReq["messages"])
	}
	assistant := messages[1]
	calls, _ := assistant["tool_calls"].([]map[string]interface{})
	if len(calls) != 1 {
		t.Fatalf("assistant legacy function_call was not normalized: %#v", assistant)
	}
	function, _ := calls[0]["function"].(map[string]interface{})
	if function["name"] != "get_time" {
		t.Fatalf("function name lost: %#v", calls[0])
	}
	toolMessage := messages[2]
	if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] == "" || toolMessage["name"] != "get_time" {
		t.Fatalf("legacy function result was not normalized: %#v", toolMessage)
	}

	var response api.OpenAIChatResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	message := response.Choices[0].Message
	if len(message.ToolCalls) != 1 || message.ToolCalls[0].Function.Name != "get_time" {
		t.Fatalf("legacy response function_call was not normalized: %#v", message)
	}
	if message.FunctionCall != nil {
		t.Fatalf("legacy function_call leaked into the response: %#v", message.FunctionCall)
	}
	if response.Choices[0].FinishReason == nil || *response.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %#v", response.Choices[0].FinishReason)
	}
}
