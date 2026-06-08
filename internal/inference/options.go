package inference

// Options controls generation parameters.
type Options struct {
	Temperature float64
	TopP        float64
	TopK        int
	MaxTokens   int
	Seed        int
	NumCtx      int
	Stop        []string
	// DisableThinking forces routing-style requests to skip provider thinking
	// modes (Qwen enable_thinking=false, GLM/Kimi/DeepSeek thinking.type=disabled).
	DisableThinking bool
}

// DefaultOptions returns sensible defaults. MaxTokens follows Ollama and
// llama.cpp semantics: -1 means no explicit generation cap.
func DefaultOptions() Options {
	return Options{
		Temperature: 0.7,
		TopP:        0.9,
		TopK:        40,
		MaxTokens:   -1,
		Seed:        -1,
		NumCtx:      4096,
	}
}

// Message represents a chat message.
// Content can be a string for text-only, or an array of content parts
// for multimodal (e.g., image + text) messages.
type Message struct {
	Role             string      `json:"role"`
	Content          interface{} `json:"content"`
	ReasoningContent string      `json:"reasoning_content,omitempty"`
}

// ContentPart represents one part of a multimodal message.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

// TokenCallback is called for each generated token during streaming.
type TokenCallback func(token string)
