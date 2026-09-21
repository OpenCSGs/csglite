package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDisableThinkingRequestBodyByModelFamily(t *testing.T) {
	cases := []struct {
		name                    string
		model                   string
		wantThinkingTypeDisable bool
		wantEnableThinkingFalse bool
	}{
		{name: "glm-5", model: "glm-5", wantThinkingTypeDisable: true},
		{name: "glm-5.1", model: "glm-5.1", wantThinkingTypeDisable: true},
		{name: "glm-5.3-flash", model: "glm-5.3-flash", wantThinkingTypeDisable: false, wantEnableThinkingFalse: false},
		{name: "kimi-k2.6", model: "kimi-k2.6", wantThinkingTypeDisable: true},
		{name: "moonshot-v1-8k", model: "moonshot-v1-8k", wantThinkingTypeDisable: true},
		{name: "deepseek-v4-pro", model: "deepseek-v4-pro", wantThinkingTypeDisable: true},
		{name: "deepseek-v4-flash", model: "deepseek-v4-flash", wantThinkingTypeDisable: true},
		{name: "mimo-v2.5-pro", model: "mimo-v2.5-pro", wantThinkingTypeDisable: true},
		{name: "qwen3", model: "Qwen/Qwen3-32B-Instruct", wantEnableThinkingFalse: true},
		{name: "minimax", model: "MiniMax-M2.5", wantThinkingTypeDisable: false, wantEnableThinkingFalse: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]interface{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"action\":\"skip\",\"reason\":\"test\",\"confidence\":0.9}"}}]}`)
			}))
			defer ts.Close()

			eng := NewOpenAICompatibleEngine(ts.URL, tc.model, "test-token")
			opts := DefaultOptions()
			opts.MaxTokens = 96
			opts.DisableThinking = true
			_, err := eng.Chat(context.Background(), []Message{{Role: "user", Content: "你好"}}, opts, nil)
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}

			if tc.wantThinkingTypeDisable {
				thinking, ok := got["thinking"].(map[string]interface{})
				if !ok {
					t.Fatalf("thinking = %#v, want map with type disabled", got["thinking"])
				}
				if thinking["type"] != "disabled" {
					t.Fatalf("thinking.type = %#v, want disabled", thinking["type"])
				}
			} else if got["thinking"] != nil {
				t.Fatalf("thinking = %#v, want omitted", got["thinking"])
			}

			if tc.wantEnableThinkingFalse {
				if got["enable_thinking"] != false {
					t.Fatalf("enable_thinking = %#v, want false", got["enable_thinking"])
				}
			} else if _, ok := got["enable_thinking"]; ok {
				t.Fatalf("enable_thinking = %#v, want omitted", got["enable_thinking"])
			}
			if tc.model == "glm-5" && got["temperature"] != 0.6 {
				t.Fatalf("temperature = %#v, want 0.6 when thinking is disabled", got["temperature"])
			}
			if tc.model == "glm-5.3-flash" {
				if got["temperature"] == 0.6 {
					t.Fatalf("temperature = 0.6, want unchanged for always-thinking model")
				}
			}
		})
	}
}

func TestOpenAIEngineDisableThinkingUsesTypeDisabledForGLM(t *testing.T) {
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
	opts := DefaultOptions()
	opts.DisableThinking = true
	_, err := eng.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, opts, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	thinking, ok := got["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("thinking = %#v, want type disabled", got["thinking"])
	}
}

func TestOpenAIEngineDoesNotDisableThinkingForAlwaysThinkingModel(t *testing.T) {
	var got map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer ts.Close()

	eng := NewOpenAIEngine(ts.URL, "glm-5.3-flash", "test-token")
	opts := DefaultOptions()
	opts.DisableThinking = true
	_, err := eng.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, opts, nil)
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if _, ok := got["thinking"]; ok {
		t.Fatalf("thinking = %#v, want omitted for always-thinking model", got["thinking"])
	}
	if got["temperature"] == 0.6 {
		t.Fatalf("temperature = 0.6, want unchanged for always-thinking model")
	}
}
