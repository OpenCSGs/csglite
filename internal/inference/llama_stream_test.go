package inference

import (
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

func TestBuildLlamaChatRequestBodyLeavesLlamaModelsUntouched(t *testing.T) {
	reqBody := buildLlamaChatRequestBody("meta-llama/Llama-3.1-8B-Instruct", []Message{{Role: "user", Content: "hi"}}, DefaultOptions(), false)

	if _, ok := reqBody["chat_template_kwargs"]; ok {
		t.Fatalf("chat_template_kwargs unexpectedly set: %#v", reqBody["chat_template_kwargs"])
	}
}
