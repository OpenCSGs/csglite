package server

import (
	"testing"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestNormalizeResponsesVisibleTextStripsThinkBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "leading think block",
			in:   "<think>The user greeted us.</think>\n\nHi! How can I help?",
			want: "Hi! How can I help?",
		},
		{
			name: "case insensitive tags",
			in:   "<THINK>hidden</THINK>\nOK",
			want: "OK",
		},
		{
			name: "middle think block",
			in:   "hello <think>hidden</think> world",
			want: "hello  world",
		},
		{
			name: "unfinished think block",
			in:   "visible <think>hidden",
			want: "visible ",
		},
		{
			name: "plain text",
			in:   "show <thinking> as normal text",
			want: "show <thinking> as normal text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeResponsesVisibleText(tt.in); got != tt.want {
				t.Fatalf("normalizeResponsesVisibleText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResponsesThinkTagFilterHandlesSplitTags(t *testing.T) {
	filter := newResponsesThinkTagFilter()
	chunks := []string{
		"<thi",
		"nk>hidden reasoning</th",
		"ink>\n\nHi",
		" there",
	}
	var got string
	for _, chunk := range chunks {
		got += filter.Push(chunk)
	}
	got += filter.Flush()

	if want := "Hi there"; got != want {
		t.Fatalf("streamed visible text = %q, want %q", got, want)
	}
}

func TestResponsesThinkTagFilterPreservesNonTagPrefixAcrossChunks(t *testing.T) {
	filter := newResponsesThinkTagFilter()
	got := filter.Push("use <thi")
	got += filter.Push("s normally")
	got += filter.Flush()

	if want := "use <this normally"; got != want {
		t.Fatalf("streamed visible text = %q, want %q", got, want)
	}
}

func TestResponsesInputReasoningMergesIntoAssistantMessage(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{
			"type": "reasoning",
			"content": []interface{}{
				map[string]interface{}{"type": "reasoning_text", "text": "hidden reasoning"},
			},
		},
		map[string]interface{}{
			"type": "message",
			"role": "assistant",
			"content": []interface{}{
				map[string]interface{}{"type": "output_text", "text": "visible answer"},
			},
		},
		map[string]interface{}{
			"type": "message",
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{"type": "input_text", "text": "continue"},
			},
		},
	}

	messages, err := responsesInputToOpenAIMessages(input)
	if err != nil {
		t.Fatalf("responsesInputToOpenAIMessages() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if messages[0].Role != "assistant" || messages[0].Content != "visible answer" || messages[0].ReasoningContent != "hidden reasoning" {
		t.Fatalf("assistant message = %#v, want content and reasoning_content preserved", messages[0])
	}
}

func TestResponsesInputReasoningAttachedToToolCallAssistant(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{
			"type": "reasoning",
			"content": []interface{}{
				map[string]interface{}{"type": "reasoning_text", "text": "plan to list dir"},
			},
		},
		map[string]interface{}{
			"type":      "function_call",
			"call_id":   "call_1",
			"name":      "list_dir",
			"arguments": `{}`,
		},
		map[string]interface{}{
			"type": "function_call_output",
			"name": "list_dir", "call_id": "call_1",
			"output": "ok",
		},
		map[string]interface{}{
			"type": "message", "role": "user",
			"content": []interface{}{
				map[string]interface{}{"type": "input_text", "text": "thanks"},
			},
		},
	}

	messages, err := responsesInputToOpenAIMessages(input)
	if err != nil {
		t.Fatalf("responsesInputToOpenAIMessages() error = %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("len(messages) = %d, want >= 2", len(messages))
	}
	toolAssistant := messages[0]
	if len(toolAssistant.ToolCalls) != 1 || toolAssistant.ToolCalls[0].Function.Name != "list_dir" {
		t.Fatalf("tool assistant = %#v", toolAssistant)
	}
	if toolAssistant.ReasoningContent != "plan to list dir" {
		t.Fatalf("ReasoningContent = %q, want plan to list dir", toolAssistant.ReasoningContent)
	}
}

func TestResponsesInputEncryptedReasoningAttachedToToolCallAssistant(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{
			"type":              "reasoning",
			"encrypted_content": "opaque reasoning replay",
		},
		map[string]interface{}{
			"type":      "function_call",
			"call_id":   "call_1",
			"name":      "list_dir",
			"arguments": `{}`,
		},
		map[string]interface{}{
			"type":    "function_call_output",
			"name":    "list_dir",
			"call_id": "call_1",
			"output":  "ok",
		},
	}

	messages, err := responsesInputToOpenAIMessages(input)
	if err != nil {
		t.Fatalf("responsesInputToOpenAIMessages() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	toolAssistant := messages[0]
	if len(toolAssistant.ToolCalls) != 1 || toolAssistant.ToolCalls[0].Function.Name != "list_dir" {
		t.Fatalf("tool assistant = %#v", toolAssistant)
	}
	if toolAssistant.ReasoningContent != "opaque reasoning replay" {
		t.Fatalf("ReasoningContent = %q, want opaque reasoning replay", toolAssistant.ReasoningContent)
	}
}

func TestResponsesOutputFromOpenAIChatResponseIncludesReasoningItem(t *testing.T) {
	resp := api.OpenAIChatResponse{
		Choices: []api.OpenAIChoice{{
			Message: &api.Message{
				Role:             "assistant",
				Content:          "visible answer",
				ReasoningContent: "hidden reasoning",
			},
		}},
	}

	output, outputText := responsesOutputFromOpenAIChatResponse(resp)
	if outputText != "visible answer" {
		t.Fatalf("outputText = %q, want visible answer", outputText)
	}
	if len(output) != 2 {
		t.Fatalf("len(output) = %d, want reasoning and message items", len(output))
	}
	reasoning, ok := output[0].(map[string]interface{})
	if !ok {
		t.Fatalf("reasoning item = %#v", output[0])
	}
	if reasoning["type"] != "reasoning" || responsesReasoningItemText(reasoning) != "hidden reasoning" {
		t.Fatalf("reasoning item = %#v, want hidden reasoning", reasoning)
	}
	if reasoning["encrypted_content"] != "hidden reasoning" {
		t.Fatalf("encrypted_content = %#v, want hidden reasoning", reasoning["encrypted_content"])
	}
	summary, ok := reasoning["summary"].([]map[string]interface{})
	if !ok || len(summary) != 1 || summary[0]["text"] != "hidden reasoning" {
		t.Fatalf("summary = %#v, want hidden reasoning summary", reasoning["summary"])
	}
}
