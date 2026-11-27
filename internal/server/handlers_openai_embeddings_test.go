package server

import (
	"encoding/json"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestOpenAIEmbeddingPromptTokens(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		body  string
		want  int
	}{
		{"token IDs", "[10,20,30]", `{"data":[]}`, 3},
		{"batch of token IDs", "[[10,20],[30,40,50]]", `{"usage":{"prompt_tokens":0}}`, 5},
		{"upstream usage", "[10,20,30]", `{"usage":{"prompt_tokens":7}}`, 7},
		{"text", `"hello"`, `{}`, estimateAnthropicTokens("hello")},
		{"batch of text", `["hello","world"]`, `{}`, estimateAnthropicTokens("hello") + estimateAnthropicTokens("world")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req api.OpenAIEmbeddingsRequest
			if err := json.Unmarshal([]byte(`{"model":"embedding","input":`+tc.input+`}`), &req); err != nil {
				t.Fatal(err)
			}
			if got := openAIEmbeddingPromptTokens([]byte(tc.body), req.Input); got != tc.want {
				t.Fatalf("prompt tokens = %d, want %d", got, tc.want)
			}
		})
	}
}
