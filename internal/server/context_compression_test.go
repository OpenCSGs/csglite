package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/ctxcompress"
	"github.com/opencsgs/csglite/internal/observability"
	"github.com/opencsgs/csglite/pkg/api"
)

func contextCompressionTestBody(t *testing.T) (string, string) {
	t.Helper()
	var grep strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&grep, "internal/server/routes.go:%d:\tmux.HandleFunc(%d)\n", i, i)
	}
	body, err := json.Marshal(map[string]any{
		"model": "test/model",
		"messages": []any{
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "grep", "arguments": "{}"}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": grep.String()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body), grep.String()
}

func TestWithContextCompressionRewritesToolOutput(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Inference.ContextCompression = config.ContextCompressionSafe
	var seen []byte
	var seenLength int64
	handler := s.observabilityMiddleware(s.withContextCompression(ctxcompress.ProtocolOpenAIChat, func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		seenLength = r.ContentLength
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":1}}`))
	}))
	body, grep := contextCompressionTestBody(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if strings.Contains(string(seen), "routes.go:1:") || !strings.Contains(string(seen), `internal/server/routes.go\n  1:`) {
		t.Fatalf("handler saw an uncompressed body: %s", seen)
	}
	if seenLength != int64(len(seen)) {
		t.Fatalf("ContentLength = %d, body is %d bytes", seenLength, len(seen))
	}
	header := recorder.Header().Get(contextCompressionHeader)
	if !strings.HasPrefix(header, "mode=safe; blocks=1; compressed=1; bytes=") {
		t.Fatalf("%s = %q", contextCompressionHeader, header)
	}

	s.observabilityMu.RLock()
	page, err := s.observability.ListRequests(req.Context(), observability.RequestFilter{})
	s.observabilityMu.RUnlock()
	if err != nil || page.Total != 1 {
		t.Fatalf("captured %d requests: %v", page.Total, err)
	}
	record := page.Items[0]
	if record.ContextCompressionMode != "safe" || record.ContextBlocks != 1 || record.ContextCompressedBlocks != 1 ||
		record.ContextBytesBefore != int64(len(grep)) || record.ContextBytesAfter >= record.ContextBytesBefore ||
		record.ContextTokensSaved <= 0 {
		t.Fatalf("compression not recorded: %+v", record)
	}
	if page.Summary.ContextTokensSaved != record.ContextTokensSaved {
		t.Fatalf("summary saved %d tokens, record %d", page.Summary.ContextTokensSaved, record.ContextTokensSaved)
	}
	s.observabilityMu.RLock()
	detail, err := s.observability.GetRequest(req.Context(), record.ID)
	s.observabilityMu.RUnlock()
	if err != nil || !strings.Contains(detail.RequestBody, "routes.go:1:") {
		t.Fatalf("stored request body is not the one the client sent: %v", err)
	}
	if resp := observabilityRequestResponse(detail); resp.ContextCompression == nil || resp.ContextCompression.Mode != "safe" {
		t.Fatalf("API response context_compression = %+v", resp.ContextCompression)
	}
}

func TestWithContextCompressionOffPassesBodyThrough(t *testing.T) {
	s := newTestServer(t)
	body, _ := contextCompressionTestBody(t)
	for _, mode := range []string{"", config.ContextCompressionOff, "unknown"} {
		s.cfg.Inference.ContextCompression = mode
		var seen []byte
		handler := s.withContextCompression(ctxcompress.ProtocolOpenAIChat, func(w http.ResponseWriter, r *http.Request) {
			seen, _ = io.ReadAll(r.Body)
		})
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
		if string(seen) != body || recorder.Header().Get(contextCompressionHeader) != "" {
			t.Fatalf("mode %q changed the request", mode)
		}
	}

	// A body the compressor cannot parse reaches the handler untouched.
	s.cfg.Inference.ContextCompression = config.ContextCompressionAggressive
	var seen []byte
	handler := s.withContextCompression(ctxcompress.ProtocolAnthropic, func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
	})
	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{broken")))
	if string(seen) != "{broken" {
		t.Fatalf("broken body = %q", seen)
	}
}

func TestHandleSettingsUpdatesContextCompression(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.Reset()
	s := newTestServer(t)

	update := func(mode string) *httptest.ResponseRecorder {
		body, err := json.Marshal(api.SettingsUpdateRequest{ContextCompression: &mode})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		s.handleSettingsUpdate(w, httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body)))
		return w
	}

	w := update("Aggressive")
	if w.Code != http.StatusOK || s.cfg.Inference.ContextCompression != config.ContextCompressionAggressive {
		t.Fatalf("status %d, stored %q", w.Code, s.cfg.Inference.ContextCompression)
	}
	var resp api.SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil || resp.ContextCompression != "aggressive" {
		t.Fatalf("response context_compression = %q (%v)", resp.ContextCompression, err)
	}

	if w := update("everything"); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode status = %d", w.Code)
	}
	if s.cfg.Inference.ContextCompression != config.ContextCompressionAggressive {
		t.Fatal("invalid mode changed the setting")
	}

	if w := update("off"); w.Code != http.StatusOK || s.cfg.Inference.ContextCompression != "" {
		t.Fatalf("off stored as %q", s.cfg.Inference.ContextCompression)
	}
	if got := s.settingsResponse().ContextCompression; got != "off" {
		t.Fatalf("settings report %q, want off", got)
	}
}

// TestContextCompressionEndToEnd sends agent requests in all three protocols
// through the full router to a fake provider and checks what the provider
// receives.
func TestContextCompressionEndToEnd(t *testing.T) {
	s := newTestServer(t)
	var received []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		received = append(received, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"up-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	if err := config.SaveProviders([]config.ThirdPartyProvider{
		{ID: "up", Name: "Up", BaseURL: upstream.URL + "/v1", APIKey: "key", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	_, grep := contextCompressionTestBody(t)
	tool := map[string]any{"type": "function", "function": map[string]any{"name": "grep", "parameters": map[string]any{"type": "object"}}}
	requests := map[string]map[string]any{
		"/v1/chat/completions": {
			"model": "up-model", "source": "provider:up", "tools": []any{tool},
			"messages": []any{
				map[string]any{"role": "user", "content": "find handlers"},
				map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "grep", "arguments": "{}"}}}},
				map[string]any{"role": "tool", "tool_call_id": "call_1", "content": grep},
			},
		},
		"/v1/messages": {
			"model": "up-model", "source": "provider:up", "max_tokens": 100,
			"tools": []any{map[string]any{"name": "Grep", "input_schema": map[string]any{"type": "object"}}},
			"messages": []any{
				map[string]any{"role": "user", "content": "find handlers"},
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Grep", "input": map[string]any{}}}},
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": grep}}},
			},
		},
		"/v1/responses": {
			"model": "up-model", "source": "provider:up",
			"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
			"input": []any{
				map[string]any{"type": "message", "role": "user", "content": "find handlers"},
				map[string]any{"type": "function_call", "call_id": "call_9", "name": "shell", "arguments": "{}"},
				map[string]any{"type": "function_call_output", "call_id": "call_9", "output": grep},
			},
		},
	}

	for _, mode := range []string{config.ContextCompressionOff, config.ContextCompressionSafe} {
		s.cfg.Inference.ContextCompression = mode
		for path, payload := range requests {
			received = nil
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, req)
			if w.Code != http.StatusOK || len(received) != 1 {
				t.Fatalf("%s %s: status %d, upstream calls %d, body %s", mode, path, w.Code, len(received), w.Body.String())
			}
			sent, _ := json.Marshal(received[0]["messages"])
			grouped := strings.Contains(string(sent), `internal/server/routes.go\n  1:`)
			original := strings.Contains(string(sent), `routes.go:1:\tmux`)
			header := w.Header().Get(contextCompressionHeader)
			switch mode {
			case config.ContextCompressionSafe:
				if !grouped || original || !strings.HasPrefix(header, "mode=safe; blocks=1; compressed=1") {
					t.Fatalf("%s: provider got uncompressed tool output (header %q): %s", path, header, sent)
				}
			default:
				if grouped || !original || header != "" {
					t.Fatalf("%s: compression ran while off (header %q): %s", path, header, sent)
				}
			}
		}
	}
}
