package cli

import (
	"net"
	"strings"
	"testing"
)

func TestPsAPIUsageIncludesEndpointAndCurlExample(t *testing.T) {
	got := psOpenAIAPIUsage("http://127.0.0.1:11435", "Qwen/Qwen3-0.6B-GGUF")
	want := "\nAI Gateway (publish a model service at /ai-gateway; use its service ID):\n  GET  http://127.0.0.1:11435/ai-gateway/v1/models\n  POST http://127.0.0.1:11435/ai-gateway/v1/chat/completions\n  curl http://127.0.0.1:11435/ai-gateway/v1/chat/completions \\\n    -H \"Content-Type: application/json\" \\\n    -d '{\"model\":\"Qwen/Qwen3-0.6B-GGUF\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello!\"}]}'\n"
	if got != want {
		t.Fatalf("psOpenAIAPIUsage() = %q, want %q", got, want)
	}
}

func TestPsOpenAIAPIUsageFallsBackToPlaceholderModel(t *testing.T) {
	got := psOpenAIAPIUsage("http://127.0.0.1:11435", "")
	if got == "" {
		t.Fatal("psOpenAIAPIUsage() should not be empty")
	}
	if want := "\"model\":\"<service-id>\""; !strings.Contains(got, want) {
		t.Fatalf("psOpenAIAPIUsage() = %q, want substring %q", got, want)
	}
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

func TestFormatPsRequestErrorUsesBusyMessageForTimeout(t *testing.T) {
	err := formatPsRequestError("http://127.0.0.1:11435", timeoutNetError{})
	if err == nil {
		t.Fatal("formatPsRequestError() returned nil")
	}
	if !strings.Contains(err.Error(), "did not respond within 5s") {
		t.Fatalf("formatPsRequestError() = %q, want timeout hint", err)
	}
}

func TestFormatPsRequestErrorUsesConnectionMessageForRefused(t *testing.T) {
	err := formatPsRequestError("http://127.0.0.1:11435", &net.OpError{Op: "dial", Err: net.ErrClosed})
	if err == nil {
		t.Fatal("formatPsRequestError() returned nil")
	}
	if !strings.Contains(err.Error(), "cannot connect to csghub-lite server") {
		t.Fatalf("formatPsRequestError() = %q, want connection hint", err)
	}
}
