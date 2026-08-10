package inference

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestHandleStreamReasoningContent(t *testing.T) {
	e := &llamaEngine{}
	var tokens strings.Builder
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n\n" +
		"data: [DONE]\n\n"

	full, err := e.handleStream(strings.NewReader(sse), func(s string) {
		tokens.WriteString(s)
	}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	want := "Hi there"
	if full != want {
		t.Errorf("full = %q, want %q", full, want)
	}
	if tokens.String() != want {
		t.Errorf("streamed tokens = %q, want %q", tokens.String(), want)
	}
}

func TestHandleNonStreamReasoningOnly(t *testing.T) {
	e := &llamaEngine{}
	body := `{"choices":[{"message":{"reasoning_content":"Answer","content":""}}]}`
	got, err := e.handleNonStream(strings.NewReader(body), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got != "Answer" {
		t.Errorf("got %q, want Answer", got)
	}
}

func TestHandleNonStreamBothReasoningAndContent(t *testing.T) {
	e := &llamaEngine{}
	body := `{"choices":[{"message":{"reasoning_content":"think","content":"ok"}}]}`
	got, err := e.handleNonStream(strings.NewReader(body), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got != "thinkok" {
		t.Errorf("got %q, want thinkok", got)
	}
}

func TestHandleStreamSameChunkDuplicateContentAndReasoning(t *testing.T) {
	e := &llamaEngine{}
	var n int
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"你好\",\"reasoning_content\":\"你好\"}}]}\n\n" +
		"data: [DONE]\n\n"
	_, err := e.handleStream(strings.NewReader(sse), func(s string) {
		n++
		if s != "你好" {
			t.Errorf("unexpected token %q", s)
		}
	}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("onToken called %d times, want 1 (no duplicate fields)", n)
	}
}

func TestHandleStreamMissingDoneReturnsUnexpectedEOF(t *testing.T) {
	e := &llamaEngine{}
	var tokens strings.Builder
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"

	full, err := e.handleStream(strings.NewReader(sse), func(s string) {
		tokens.WriteString(s)
	}, DefaultOptions())
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("handleStream() error = %v, want unexpected EOF", err)
	}
	if full != "partial" || tokens.String() != "partial" {
		t.Fatalf("streamed = (%q, %q), want partial", full, tokens.String())
	}
}

func TestHandleNonStreamDuplicateReasoningAndContent(t *testing.T) {
	e := &llamaEngine{}
	body := `{"choices":[{"message":{"reasoning_content":"你好","content":"你好"}}]}`
	got, err := e.handleNonStream(strings.NewReader(body), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got != "你好" {
		t.Errorf("got %q, want single 你好", got)
	}
}

func TestHandleNonStreamDisableThinkingIgnoresReasoning(t *testing.T) {
	e := &llamaEngine{}
	body := `{"choices":[{"message":{"reasoning_content":"long thinking","content":"{\"action\":\"skip\"}"}}]}`
	opts := DefaultOptions()
	opts.DisableThinking = true
	got, err := e.handleNonStream(strings.NewReader(body), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"action":"skip"}` {
		t.Fatalf("got %q, want content only", got)
	}
}

func TestBuildLlamaChatRequestBodyDisablesThinkingForQwen3508B(t *testing.T) {
	opts := DefaultOptions()
	opts.Seed = 7
	opts.Stop = []string{"</stop>"}

	reqBody := buildLlamaChatRequestBody("Qwen/Qwen3.5-0.8B", []Message{{Role: "user", Content: "hi"}}, opts, true)

	kwargs, ok := reqBody["chat_template_kwargs"].(map[string]interface{})
	if !ok {
		t.Fatalf("chat_template_kwargs missing or wrong type: %#v", reqBody["chat_template_kwargs"])
	}
	if got, ok := kwargs["enable_thinking"].(bool); !ok || got {
		t.Fatalf("enable_thinking = %#v, want false", kwargs["enable_thinking"])
	}
	if got, ok := reqBody["seed"].(int); !ok || got != 7 {
		t.Fatalf("seed = %#v, want 7", reqBody["seed"])
	}
	if got, ok := reqBody["stop"].([]string); !ok || len(got) != 1 || got[0] != "</stop>" {
		t.Fatalf("stop = %#v, want [\"</stop>\"]", reqBody["stop"])
	}
	if got, ok := reqBody["max_tokens"].(int); !ok || got != -1 {
		t.Fatalf("max_tokens = %#v, want -1", reqBody["max_tokens"])
	}
	streamOptions, ok := reqBody["stream_options"].(map[string]interface{})
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options = %#v, want include_usage=true", reqBody["stream_options"])
	}
}

func TestHandleStreamRecordsLlamaCacheUsage(t *testing.T) {
	e := &llamaEngine{}
	collector := &CacheUsageCollector{}
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":22,\"prompt_tokens_details\":{\"cached_tokens\":18}},\"timings\":{\"cache_n\":18,\"prompt_n\":4}}\n\n" +
		"data: [DONE]\n\n"

	if _, err := e.handleStreamWithCacheUsage(strings.NewReader(sse), func(string) {}, DefaultOptions(), collector); err != nil {
		t.Fatal(err)
	}
	usage := collector.Snapshot()
	if usage.ReadInputTokens != 18 || usage.CreationInputTokens != 4 || usage.EligibleInputTokens != 22 {
		t.Fatalf("cache usage = %+v, want read=18 creation=4 eligible=22", usage)
	}
}

func TestHandleNonStreamRecordsLlamaCacheUsage(t *testing.T) {
	e := &llamaEngine{}
	collector := &CacheUsageCollector{}
	body := `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":75}},"timings":{"cache_n":75,"prompt_n":25}}`

	if _, err := e.handleNonStreamWithCacheUsage(strings.NewReader(body), DefaultOptions(), collector); err != nil {
		t.Fatal(err)
	}
	usage := collector.Snapshot()
	if usage.ReadInputTokens != 75 || usage.CreationInputTokens != 25 || usage.EligibleInputTokens != 100 {
		t.Fatalf("cache usage = %+v, want read=75 creation=25 eligible=100", usage)
	}
}

func TestBuildLlamaChatRequestBodyDisablesThinkingForQwen3Family(t *testing.T) {
	reqBody := buildLlamaChatRequestBody("Qwen/Qwen3-0.6B-GGUF", []Message{{Role: "user", Content: "hi"}}, DefaultOptions(), false)

	kwargs, ok := reqBody["chat_template_kwargs"].(map[string]interface{})
	if !ok {
		t.Fatalf("chat_template_kwargs missing or wrong type: %#v", reqBody["chat_template_kwargs"])
	}
	if got, ok := kwargs["enable_thinking"].(bool); !ok || got {
		t.Fatalf("enable_thinking = %#v, want false", kwargs["enable_thinking"])
	}
}

func TestApplyLlamaThinkingControlsPreservesExplicitEnableThinking(t *testing.T) {
	reqBody := map[string]interface{}{
		"chat_template_kwargs": map[string]interface{}{"enable_thinking": true},
	}

	applyLlamaThinkingControls("Qwen3.5-2B", reqBody, false)

	kwargs := reqBody["chat_template_kwargs"].(map[string]interface{})
	if got, ok := kwargs["enable_thinking"].(bool); !ok || !got {
		t.Fatalf("enable_thinking = %#v, want true", kwargs["enable_thinking"])
	}
}

func TestApplyLlamaThinkingControlsForceDisableOverridesExplicitValue(t *testing.T) {
	reqBody := map[string]interface{}{
		"chat_template_kwargs": map[string]interface{}{"enable_thinking": true},
	}

	applyLlamaThinkingControls("Qwen3.5-2B", reqBody, true)

	kwargs := reqBody["chat_template_kwargs"].(map[string]interface{})
	if got, ok := kwargs["enable_thinking"].(bool); !ok || got {
		t.Fatalf("enable_thinking = %#v, want false", kwargs["enable_thinking"])
	}
}

func TestBuildLlamaChatRequestBodyLeavesLlamaModelsUntouched(t *testing.T) {
	reqBody := buildLlamaChatRequestBody("meta-llama/Llama-3.1-8B-Instruct", []Message{{Role: "user", Content: "hi"}}, DefaultOptions(), false)

	if _, ok := reqBody["chat_template_kwargs"]; ok {
		t.Fatalf("chat_template_kwargs unexpectedly set: %#v", reqBody["chat_template_kwargs"])
	}
}
