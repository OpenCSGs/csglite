package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIEngineChatUnauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"code":"AUTH-ERR-1","msg":"AUTH-ERR-1: User not found, please login first"}`)
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "deepseek-v3", "test-token")
	_, err := eng.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, DefaultOptions(), nil)
	if err == nil {
		t.Fatal("expected unauthorized error")
	}
	if got := HTTPStatusCode(err); got != http.StatusUnauthorized {
		t.Fatalf("HTTPStatusCode = %d, want %d", got, http.StatusUnauthorized)
	}
	if HTTPErrorMessage(err) != "AUTH-ERR-1: User not found, please login first" {
		t.Fatalf("error = %q", HTTPErrorMessage(err))
	}
}

func TestOpenAIEngineChatStream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"total_tokens\":102,\"prompt_tokens_details\":{\"cached_tokens\":75,\"write_cached_tokens\":10}}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "deepseek-v3", "test-token")
	var streamed string
	ctx, collector := WithCacheUsageCollector(context.Background())
	got, err := eng.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, DefaultOptions(), func(token string) {
		streamed += token
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("got = %q, want hello", got)
	}
	if streamed != "hello" {
		t.Fatalf("streamed = %q, want hello", streamed)
	}
	usage := collector.Snapshot()
	if usage.ReadInputTokens != 75 || usage.CreationInputTokens != 10 || usage.EligibleInputTokens != 100 {
		t.Fatalf("cache usage = %+v, want read=75 creation=10 eligible=100", usage)
	}
}

func TestOpenAIEngineChatStreamReasoningContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "kimi-k2.6", "test-token")
	var streamed string
	got, err := eng.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, DefaultOptions(), func(token string) {
		streamed += token
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	want := "<think>think</think>answer"
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
	if streamed != want {
		t.Fatalf("streamed = %q, want %q", streamed, want)
	}
}

func TestOpenAIEngineChatRequestBodyMatchesCloudDefaults(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "Qwen/Qwen3.5-35B-A3B-FP8:s-qwen-qwen3-5-35b-a3b-fp8-6dp9", "test-token")
	opts := DefaultOptions()
	opts.Temperature = 0.2
	opts.TopP = 0.9
	opts.MaxTokens = 200

	_, err := eng.Chat(context.Background(), []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "hi"},
	}, opts, func(string) {})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if got["model"] != "Qwen/Qwen3.5-35B-A3B-FP8:s-qwen-qwen3-5-35b-a3b-fp8-6dp9" {
		t.Fatalf("model = %v", got["model"])
	}
	if got["stream"] != true {
		t.Fatalf("stream = %v, want true", got["stream"])
	}
	streamOptions, ok := got["stream_options"].(map[string]interface{})
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options = %#v, want include_usage=true", got["stream_options"])
	}
	if got["temperature"] != 0.2 {
		t.Fatalf("temperature = %v, want 0.2", got["temperature"])
	}
	if got["top_p"] != 0.9 {
		t.Fatalf("top_p = %v, want 0.9", got["top_p"])
	}
	if got["top_k"] != float64(10) {
		t.Fatalf("top_k = %v, want 10", got["top_k"])
	}
	if got["repetition_penalty"] != float64(1) {
		t.Fatalf("repetition_penalty = %v, want 1", got["repetition_penalty"])
	}
	if got["max_tokens"] != float64(200) {
		t.Fatalf("max_tokens = %v, want 200", got["max_tokens"])
	}
	if got["enable_thinking"] != false {
		t.Fatalf("enable_thinking = %v, want false for qwen3 family", got["enable_thinking"])
	}
	messages, ok := got["messages"].([]interface{})
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %T %v, want 2 items", got["messages"], got["messages"])
	}
}

func TestOpenAICompatibleEngineChatOmitsNonStandardSamplingParams(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAICompatibleEngine(ts.URL, "gpt-4o-2024-05-13", "test-token")
	opts := DefaultOptions()
	opts.Temperature = 0.2
	opts.TopP = 0.9
	opts.TopK = 40

	_, err := eng.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"},
	}, opts, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if _, ok := got["top_k"]; ok {
		t.Fatalf("top_k = %v, want omitted for third-party OpenAI providers", got["top_k"])
	}
	if _, ok := got["repetition_penalty"]; ok {
		t.Fatalf("repetition_penalty = %v, want omitted for third-party OpenAI providers", got["repetition_penalty"])
	}
	if got["temperature"] != 0.2 {
		t.Fatalf("temperature = %v, want 0.2", got["temperature"])
	}
	if got["top_p"] != 0.9 {
		t.Fatalf("top_p = %v, want 0.9", got["top_p"])
	}
}

func TestOpenAIEngineChatRequestBodyDropsTopPForClaudeModels(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "claude-opus-4-6", "test-token")
	opts := DefaultOptions()
	opts.Temperature = 0.2
	opts.TopP = 0.9

	_, err := eng.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"},
	}, opts, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if got["temperature"] != 0.2 {
		t.Fatalf("temperature = %v, want 0.2", got["temperature"])
	}
	if _, ok := got["top_p"]; ok {
		t.Fatalf("top_p = %v, want omitted for claude models", got["top_p"])
	}
}

func TestOpenAIEngineChatRequestBodyForcesSamplingParamsForKimiModels(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "kimi-k2.6", "test-token")
	opts := DefaultOptions()
	opts.Temperature = 0.2
	opts.TopP = 0.9

	_, err := eng.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"},
	}, opts, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if got["temperature"] != float64(1) {
		t.Fatalf("temperature = %v, want 1", got["temperature"])
	}
	if got["top_p"] != 0.95 {
		t.Fatalf("top_p = %v, want 0.95", got["top_p"])
	}
}

func TestOpenAIEngineChatKeepsKimiTemperatureWhenThinkingDisabled(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAICompatibleEngine(ts.URL, "kimi-k2.6", "test-token")
	opts := DefaultOptions()
	opts.Temperature = 0.95
	opts.DisableThinking = true

	_, err := eng.Chat(context.Background(), []Message{
		{Role: "user", Content: "hi"},
	}, opts, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	if got["temperature"] != float64(1) {
		t.Fatalf("temperature = %v, want kimi default 1", got["temperature"])
	}
	thinking, ok := got["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("thinking = %#v, want type disabled", got["thinking"])
	}
}

func TestOpenAIEngineChatCompletionAddsKimiReasoningContentToToolCalls(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "kimi-k2.6", "test-token")
	resp, err := eng.(ChatCompletionProxier).ChatCompletion(context.Background(), map[string]interface{}{
		"model":       "kimi-k2.6",
		"temperature": 0.2,
		"top_p":       0.9,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "use a tool"},
			{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]interface{}{{
					"id":   "call_1",
					"type": "function",
					"function": map[string]interface{}{
						"name":      "lookup",
						"arguments": "{}",
					},
				}},
			},
			{"role": "tool", "tool_call_id": "call_1", "content": "result"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	resp.Body.Close()

	messages, ok := got["messages"].([]interface{})
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v", got["messages"])
	}
	assistant, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("assistant message = %#v", messages[1])
	}
	if value, ok := assistant["reasoning_content"]; !ok || value != "" {
		t.Fatalf("reasoning_content = %#v, want empty string", assistant["reasoning_content"])
	}
	if got["temperature"] != float64(1) {
		t.Fatalf("temperature = %v, want 1", got["temperature"])
	}
	if got["top_p"] != 0.95 {
		t.Fatalf("top_p = %v, want 0.95", got["top_p"])
	}
}

func TestOpenAIEngineChatCompletionAddsDeepSeekV4ReasoningContentToToolCalls(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "deepseek-v4-pro", "test-token")
	resp, err := eng.(ChatCompletionProxier).ChatCompletion(context.Background(), map[string]interface{}{
		"model": "deepseek-v4-pro",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "use a tool"},
			{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]interface{}{{
					"id":   "call_1",
					"type": "function",
					"function": map[string]interface{}{
						"name":      "lookup",
						"arguments": "{}",
					},
				}},
			},
			{"role": "tool", "tool_call_id": "call_1", "content": "result"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	resp.Body.Close()

	messages, ok := got["messages"].([]interface{})
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v", got["messages"])
	}
	assistant, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("assistant message = %#v", messages[1])
	}
	if value, ok := assistant["reasoning_content"]; !ok || value != "" {
		t.Fatalf("reasoning_content = %#v, want empty string", assistant["reasoning_content"])
	}
}

func TestOpenAIEngineChatCompletionDoesNotDisableGLMThinking(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "glm-5", "test-token")
	resp, err := eng.(ChatCompletionProxier).ChatCompletion(context.Background(), map[string]interface{}{
		"model":       "glm-5",
		"temperature": 0.95,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "hi"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	resp.Body.Close()

	if _, ok := got["thinking"]; ok {
		t.Fatalf("thinking = %#v, want omitted for gateway proxy", got["thinking"])
	}
	if got["temperature"] != 0.95 {
		t.Fatalf("temperature = %#v, want unchanged for gateway proxy", got["temperature"])
	}
}

func TestOpenAICompatibleEngineDoesNotForceDisableThinkingForQwen3(t *testing.T) {
	var got map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAICompatibleEngine(ts.URL, "Qwen/Qwen3-32B-Instruct", "test-token")
	resp, err := eng.(ChatCompletionProxier).ChatCompletion(context.Background(), map[string]interface{}{
		"model":       "Qwen/Qwen3-32B-Instruct",
		"temperature": 0.2,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "hi"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	resp.Body.Close()

	if _, ok := got["enable_thinking"]; ok {
		t.Fatalf("enable_thinking = %#v, want omitted for openai-compatible providers", got["enable_thinking"])
	}
}

func TestOpenAIEngineForwardsROMAHeadersToOpenAIUpstreams(t *testing.T) {
	type receivedHeaders struct {
		host       string
		hwID       string
		appKey     string
		custom     string
		customName string
	}
	received := make(chan receivedHeaders, 2)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customName := ""
		for name := range r.Header {
			if strings.EqualFold(name, "X-Custom-Case") {
				customName = name
				break
			}
		}
		received <- receivedHeaders{
			host:       r.Host,
			hwID:       r.Header.Get("X-HW-ID"),
			appKey:     r.Header.Get("X-HW-AppKey"),
			custom:     r.Header.Get("X-Custom-Case"),
			customName: customName,
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	call := func(t *testing.T, eng Engine, ctx context.Context) receivedHeaders {
		t.Helper()
		resp, err := eng.(ChatCompletionProxier).ChatCompletion(ctx, map[string]interface{}{
			"messages": []map[string]interface{}{{"role": "user", "content": "hi"}},
		})
		if err != nil {
			t.Fatalf("ChatCompletion returned error: %v", err)
		}
		resp.Body.Close()
		return <-received
	}

	t.Run("configured provider", func(t *testing.T) {
		ctx := context.Background()
		got := call(t, NewOpenAICompatibleEngineWithHeaders(ts.URL, "deepseek-r1", "token", []ForwardHeader{
			{Name: "Host", Value: "roma.example.gov.cn"},
			{Name: "X-HW-ID", Value: "integration-key"},
			{Name: "X-HW-AppKey", Value: "integration-secret"},
			{Name: "X-Custom-Case", Value: "Keep-Me"},
		}), ctx)
		if got.host != "roma.example.gov.cn" {
			t.Fatalf("Host = %q, want ROMA group domain", got.host)
		}
		if got.hwID != "integration-key" || got.appKey != "integration-secret" {
			t.Fatalf("ROMA headers = %#v", got)
		}
		if got.custom != "Keep-Me" {
			t.Fatalf("custom header value = %q, want original value", got.custom)
		}
		if got.customName != "X-Custom-Case" {
			t.Fatalf("custom header name = %q, want original casing", got.customName)
		}
	})

	t.Run("standard request", func(t *testing.T) {
		ctx := context.Background()
		got := call(t, NewOpenAIEngine(ts.URL, "deepseek-r1", "token"), ctx)
		wantHost := strings.TrimPrefix(ts.URL, "http://")
		if got.host != wantHost {
			t.Fatalf("Host = %q, want upstream host %q", got.host, wantHost)
		}
		if got.hwID != "" || got.appKey != "" {
			t.Fatalf("unexpected ROMA headers: %#v", got)
		}
	})

}

func TestAddHeaderPreservingName(t *testing.T) {
	headers := http.Header{"Content-Type": []string{"application/json"}}
	addHeaderPreservingName(headers, "content-type", "application/custom")

	if _, ok := headers["Content-Type"]; ok {
		t.Fatalf("original header casing remained: %#v", headers)
	}
	if got := headers["content-type"]; len(got) != 2 || got[0] != "application/json" || got[1] != "application/custom" {
		t.Fatalf("content-type values = %#v", got)
	}
}
