package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestToolResponseKeepsTruncationFinishReason(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		finishReason string
		stream       bool
		want         string
	}{
		{"openai length", "/v1/chat/completions", "length", false, `"finish_reason":"length"`},
		{"openai stream length", "/v1/chat/completions", "length", true, `"finish_reason":"length"`},
		{"openai content filter", "/v1/chat/completions", "content_filter", false, `"finish_reason":"content_filter"`},
		{"openai stop with tool calls", "/v1/chat/completions", "stop", false, `"finish_reason":"tool_calls"`},
		{"anthropic length", "/v1/messages", "length", false, `"stop_reason":"max_tokens"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &fakeChatCompletionEngine{resp: api.OpenAIChatResponse{
				ID: "chatcmpl-t", Object: "chat.completion", Created: 1, Model: "test/model",
				Choices: []api.OpenAIChoice{{
					Message: &api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
						Index: intPointer(0), ID: "call_1", Type: "function",
						Function: api.ToolFunction{Name: "write_file", Arguments: `{"text":"abc`},
					}}},
					FinishReason: stringPointer(tt.finishReason),
				}},
			}}
			s := newAnthropicProxyTestServer(t, engine)

			tool := `{"type":"function","function":{"name":"write_file","parameters":{"type":"object"}}}`
			handler := s.handleOpenAIChatCompletions
			if tt.path == "/v1/messages" {
				tool = `{"name":"write_file","input_schema":{"type":"object"}}`
				handler = s.handleAnthropicMessages
			}
			stream := "false"
			if tt.stream {
				stream = "true"
			}
			body := `{"model":"test/model","max_tokens":16,"messages":[{"role":"user","content":"write"}],"tools":[` +
				tool + `],"stream":` + stream + `}`
			recorder := httptest.NewRecorder()
			handler(recorder, httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(body)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Body.String(); !strings.Contains(got, tt.want) {
				t.Fatalf("response missing %s:\n%s", tt.want, got)
			}
		})
	}
}
