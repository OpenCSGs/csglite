package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

type fakeChatCompletionEngine struct {
	resp    api.OpenAIChatResponse
	lastReq map[string]interface{}
}

type chunkedOpenAIStreamReader struct {
	chunks []string
}

func (r *chunkedOpenAIStreamReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}

type trackingFlushResponseWriter struct {
	*httptest.ResponseRecorder
	flushedBodies []string
}

func (w *trackingFlushResponseWriter) Flush() {
	w.flushedBodies = append(w.flushedBodies, w.Body.String())
	w.ResponseRecorder.Flush()
}

func (e *fakeChatCompletionEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (e *fakeChatCompletionEngine) Chat(context.Context, []inference.Message, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (e *fakeChatCompletionEngine) Close() error { return nil }

func (e *fakeChatCompletionEngine) ModelName() string { return "test/model" }

func (e *fakeChatCompletionEngine) ChatCompletion(_ context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	e.lastReq = reqBody
	if stream, _ := reqBody["stream"].(bool); stream {
		var body strings.Builder
		for _, choice := range e.resp.Choices {
			delta := choice.Message
			if delta == nil {
				delta = choice.Delta
			}
			chunk := api.OpenAIChatResponse{
				ID:      e.resp.ID,
				Object:  "chat.completion.chunk",
				Created: e.resp.Created,
				Model:   e.resp.Model,
				Choices: []api.OpenAIChoice{{
					Index: choice.Index,
					Delta: delta,
				}},
			}
			data, err := json.Marshal(chunk)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&body, "data: %s\n\n", data)

			finishReason := openAIChoiceFinishReason(choice)
			finishChunk := api.OpenAIChatResponse{
				ID:      e.resp.ID,
				Object:  "chat.completion.chunk",
				Created: e.resp.Created,
				Model:   e.resp.Model,
				Choices: []api.OpenAIChoice{{
					Index:        choice.Index,
					Delta:        &api.Message{Role: "assistant", Content: ""},
					FinishReason: &finishReason,
				}},
				Usage: e.resp.Usage,
			}
			data, err = json.Marshal(finishChunk)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&body, "data: %s\n\n", data)
		}
		body.WriteString("data: [DONE]\n\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body.String())),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}
	data, err := json.Marshal(e.resp)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     make(http.Header),
	}, nil
}

func TestOpenAIStreamWriterFlushesEachUpstreamChunk(t *testing.T) {
	reader := &chunkedOpenAIStreamReader{chunks: []string{
		"data: first\n\n",
		"data: second\n\n",
		"data: [DONE]\n\n",
	}}
	recorder := &trackingFlushResponseWriter{ResponseRecorder: httptest.NewRecorder()}

	if _, err := io.Copy(openAIStreamWriter{ResponseWriter: recorder}, reader); err != nil {
		t.Fatalf("copy stream: %v", err)
	}

	if len(recorder.flushedBodies) != 3 {
		t.Fatalf("flush count = %d, want 3", len(recorder.flushedBodies))
	}
	for i, want := range []string{"data: first\n\n", "data: second\n\n", "data: [DONE]\n\n"} {
		if !strings.HasSuffix(recorder.flushedBodies[i], want) {
			t.Fatalf("flush %d body = %q, want suffix %q", i, recorder.flushedBodies[i], want)
		}
	}
}

func TestOpenAIStreamEnabled(t *testing.T) {
	enabled := true
	disabled := false
	tests := []struct {
		name           string
		requested      *bool
		defaultEnabled bool
		want           bool
	}{
		{name: "omitted uses disabled default", requested: nil, defaultEnabled: false, want: false},
		{name: "omitted uses enabled default", requested: nil, defaultEnabled: true, want: true},
		{name: "explicit true overrides default", requested: &enabled, defaultEnabled: false, want: true},
		{name: "explicit false overrides default", requested: &disabled, defaultEnabled: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openAIStreamEnabled(tt.requested, tt.defaultEnabled); got != tt.want {
				t.Fatalf("openAIStreamEnabled() = %t, want %t", got, tt.want)
			}
		})
	}
}

type fakeEmbeddingsEngine struct {
	resp    api.OpenAIEmbeddingsResponse
	lastReq map[string]interface{}
}

func (e *fakeEmbeddingsEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (e *fakeEmbeddingsEngine) Chat(context.Context, []inference.Message, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (e *fakeEmbeddingsEngine) Close() error { return nil }

func (e *fakeEmbeddingsEngine) ModelName() string { return "BAAI/bge-m3" }

func (e *fakeEmbeddingsEngine) Embeddings(_ context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	e.lastReq = reqBody
	data, err := json.Marshal(e.resp)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     make(http.Header),
	}, nil
}

func newCloudOpenAIAPIServer(t *testing.T, expectedToken string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{
						"id":           "cloud/model",
						"object":       "model",
						"created":      456,
						"owned_by":     "opencsg",
						"task":         "text-generation",
						"display_name": "Cloud Model",
						"public":       true,
					},
				},
			})
		case "/v1/chat/completions":
			if expectedToken != "" {
				if got := r.Header.Get("Authorization"); got != "Bearer "+expectedToken {
					t.Fatalf("Authorization = %q, want %q", got, "Bearer "+expectedToken)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.OpenAIChatResponse{
				ID:      "chatcmpl-cloud",
				Object:  "chat.completion",
				Created: 789,
				Model:   "cloud/model",
				Choices: []api.OpenAIChoice{{
					Index: 0,
					Message: &api.Message{
						Role:    "assistant",
						Content: "cloud reply",
					},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestNormalizeOpenAIToolResponseFromJSONText(t *testing.T) {
	tools := []api.Tool{{
		Type: "function",
		Function: api.ToolFunction{
			Name:       "get_weather",
			Parameters: map[string]interface{}{"type": "object", "required": []interface{}{"city"}},
		},
	}}
	resp := api.OpenAIChatResponse{
		Choices: []api.OpenAIChoice{{
			Message: &api.Message{
				Role:    "assistant",
				Content: "{\"name\":\"get_weather\",\"arguments\":{\"city\":\"Beijing\"}}",
			},
		}},
	}

	got := normalizeOpenAIToolResponse(resp, tools)
	if got.Choices[0].Message == nil || len(got.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected synthesized tool call, got %#v", got.Choices[0].Message)
	}
	call := got.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "get_weather" {
		t.Fatalf("unexpected tool name: %#v", call.Function.Name)
	}
	if call.Function.Arguments != "{\"city\":\"Beijing\"}" {
		t.Fatalf("unexpected arguments payload: %#v", call.Function.Arguments)
	}
	if got.Choices[0].Message.Content != nil {
		t.Fatalf("expected response content to be cleared, got %#v", got.Choices[0].Message.Content)
	}
	if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("unexpected finish reason: %#v", got.Choices[0].FinishReason)
	}
}

func TestNormalizeOpenAIToolResponseFromBareToolName(t *testing.T) {
	tools := []api.Tool{{
		Type: "function",
		Function: api.ToolFunction{
			Name:       "get_time",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
	}}
	resp := api.OpenAIChatResponse{
		Choices: []api.OpenAIChoice{{
			Message: &api.Message{
				Role:    "assistant",
				Content: "get_time",
			},
		}},
	}

	got := normalizeOpenAIToolResponse(resp, tools)
	if got.Choices[0].Message == nil || len(got.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected synthesized tool call, got %#v", got.Choices[0].Message)
	}
	call := got.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "get_time" {
		t.Fatalf("unexpected tool name: %#v", call.Function.Name)
	}
	if call.Function.Arguments != "{}" {
		t.Fatalf("unexpected arguments payload: %#v", call.Function.Arguments)
	}
}

func TestHandleOpenAIEmbeddingsProxiesLocalEmbeddingEngine(t *testing.T) {
	engine := &fakeEmbeddingsEngine{
		resp: api.OpenAIEmbeddingsResponse{
			Object: "list",
			Model:  "BAAI/bge-m3",
			Data: []api.OpenAIEmbeddingObject{{
				Object:    "embedding",
				Embedding: []float64{0.1, 0.2, 0.3},
				Index:     0,
			}},
			Usage: api.OpenAIUsage{PromptTokens: 3, TotalTokens: 3},
		},
	}
	cfg := &config.Config{ModelDir: t.TempDir()}
	s := New(cfg, "test")
	s.engines[engineCacheKey("BAAI/bge-m3", engineModeEmbed)] = &managedEngine{
		engine:    engine,
		lastUsed:  time.Now(),
		keepAlive: DefaultKeepAlive,
	}

	body := `{"model":"BAAI/bge-m3","input":["hello","world"],"encoding_format":"float","source":"local"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIEmbeddings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	var resp api.OpenAIEmbeddingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("unexpected embeddings response: %#v", resp)
	}
	embedding, ok := resp.Data[0].Embedding.([]interface{})
	if !ok || len(embedding) != 3 {
		t.Fatalf("unexpected embeddings response: %#v", resp)
	}
	if engine.lastReq["model"] != "BAAI/bge-m3" {
		t.Fatalf("model was not forwarded: %#v", engine.lastReq["model"])
	}
	if engine.lastReq["encoding_format"] != "float" {
		t.Fatalf("encoding_format was not preserved: %#v", engine.lastReq["encoding_format"])
	}
	if _, ok := engine.lastReq["source"]; ok {
		t.Fatalf("source should not be forwarded upstream: %#v", engine.lastReq)
	}
}

func TestHandleOpenAIEmbeddingsRoutesUnsupportedHFEmbeddingToPythonRuntime(t *testing.T) {
	oldNewPythonEmbeddingEngine := newPythonEmbeddingEngine
	oldEnsureEmbeddingRuntimeReady := ensureEmbeddingRuntimeReady
	defer func() {
		newPythonEmbeddingEngine = oldNewPythonEmbeddingEngine
		ensureEmbeddingRuntimeReady = oldEnsureEmbeddingRuntimeReady
	}()

	engine := &fakeEmbeddingsEngine{
		resp: api.OpenAIEmbeddingsResponse{
			Object: "list",
			Model:  "jinaai/jina-embeddings-v5-omni-nano",
			Data: []api.OpenAIEmbeddingObject{{
				Object:    "embedding",
				Embedding: []float64{0.1, 0.2, 0.3},
				Index:     0,
			}},
			Usage: api.OpenAIUsage{PromptTokens: 1, TotalTokens: 1},
		},
	}
	called := false
	ensureEmbeddingRuntimeReady = func(context.Context, *imagegen.RuntimeManager, imagegen.ProgressFunc, bool) error {
		return nil
	}
	newPythonEmbeddingEngine = func(_ context.Context, modelName, modelDir string, _ *imagegen.RuntimeManager) (inference.Engine, error) {
		called = true
		if modelName != "jinaai/jina-embeddings-v5-omni-nano" {
			t.Fatalf("modelName = %q", modelName)
		}
		if !strings.HasSuffix(modelDir, filepath.Join("jinaai", "jina-embeddings-v5-omni-nano")) {
			t.Fatalf("modelDir = %q", modelDir)
		}
		return engine, nil
	}

	cfg := &config.Config{ModelDir: t.TempDir()}
	modelID := "jinaai/jina-embeddings-v5-omni-nano"
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:   "jinaai",
		Name:        "jina-embeddings-v5-omni-nano",
		Format:      model.FormatSafeTensors,
		Files:       []string{"model.safetensors", "config.json"},
		PipelineTag: "feature-extraction",
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "jinaai", "jina-embeddings-v5-omni-nano")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write weights: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"architectures":["JinaEmbeddingsV5OmniModel"]}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	s := New(cfg, "test")
	body := `{"model":"` + modelID + `","input":{"text":"hello"},"source":"local"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIEmbeddings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatal("python embedding loader was not called")
	}
}

func TestHandleLoadStreamRoutesShortUnsupportedHFEmbeddingToPythonRuntime(t *testing.T) {
	oldNewPythonEmbeddingEngine := newPythonEmbeddingEngine
	oldEnsureEmbeddingRuntimeReady := ensureEmbeddingRuntimeReady
	defer func() {
		newPythonEmbeddingEngine = oldNewPythonEmbeddingEngine
		ensureEmbeddingRuntimeReady = oldEnsureEmbeddingRuntimeReady
	}()

	called := false
	ensureEmbeddingRuntimeReady = func(context.Context, *imagegen.RuntimeManager, imagegen.ProgressFunc, bool) error {
		return nil
	}
	newPythonEmbeddingEngine = func(_ context.Context, modelName, modelDir string, _ *imagegen.RuntimeManager) (inference.Engine, error) {
		called = true
		if modelName != "jinaai/jina-embeddings-v5-omni-nano" {
			t.Fatalf("modelName = %q", modelName)
		}
		if !strings.HasSuffix(modelDir, filepath.Join("jinaai", "jina-embeddings-v5-omni-nano")) {
			t.Fatalf("modelDir = %q", modelDir)
		}
		return &fakeEmbeddingsEngine{}, nil
	}

	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:   "jinaai",
		Name:        "jina-embeddings-v5-omni-nano",
		Format:      model.FormatSafeTensors,
		Files:       []string{"model.safetensors", "config.json"},
		PipelineTag: "feature-extraction",
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "jinaai", "jina-embeddings-v5-omni-nano")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write weights: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"architectures":["JinaEmbeddingsV5OmniModel"]}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	s := New(cfg, "test")
	body := `{"model":"jina-embeddings-v5-omni-nano","stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatal("python embedding loader was not called")
	}
	if !strings.Contains(w.Body.String(), `"status":"ready"`) {
		t.Fatalf("body = %q, want ready SSE", w.Body.String())
	}
}

func TestUnsupportedHFEmbeddingDoesNotUsePythonRuntimeWithoutCompatibleArchitecture(t *testing.T) {
	oldNewPythonEmbeddingEngine := newPythonEmbeddingEngine
	defer func() {
		newPythonEmbeddingEngine = oldNewPythonEmbeddingEngine
	}()

	called := false
	newPythonEmbeddingEngine = func(_ context.Context, modelName, modelDir string, _ *imagegen.RuntimeManager) (inference.Engine, error) {
		called = true
		return &fakeEmbeddingsEngine{}, nil
	}

	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:   "example",
		Name:        "unsupported-embedding",
		Format:      model.FormatSafeTensors,
		Files:       []string{"model.safetensors", "config.json"},
		PipelineTag: "feature-extraction",
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "example", "unsupported-embedding")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write weights: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"architectures":["SomeUnsupportedEmbeddingModel"]}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	s := New(cfg, "test")
	if s.shouldUsePythonEmbeddingRuntime("unsupported-embedding") {
		t.Fatal("unknown embedding architecture should not use Python embedding runtime without an explicit compatible worker path")
	}
	if called {
		t.Fatal("python embedding loader should not be called")
	}
}

func TestHandleChatWithEmbeddingModelReturnsEmbeddingJSON(t *testing.T) {
	engine := &fakeEmbeddingsEngine{
		resp: api.OpenAIEmbeddingsResponse{
			Object: "list",
			Model:  "BAAI/bge-m3",
			Data: []api.OpenAIEmbeddingObject{{
				Object:    "embedding",
				Embedding: []float64{0.1, 0.2},
				Index:     0,
			}},
			Usage: api.OpenAIUsage{PromptTokens: 2, TotalTokens: 2},
		},
	}
	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:   "BAAI",
		Name:        "bge-m3",
		Format:      model.FormatPyTorch,
		Files:       []string{"pytorch_model.bin"},
		PipelineTag: "feature-extraction",
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	s := New(cfg, "test")
	s.engines[engineCacheKey("BAAI/bge-m3", engineModeEmbed)] = &managedEngine{
		engine:    engine,
		lastUsed:  time.Now(),
		keepAlive: DefaultKeepAlive,
	}

	body := `{"model":"BAAI/bge-m3","source":"local","messages":[{"role":"user","content":"hello world"}],"stream":true,"web_search":{"enabled":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-CSGHUB-Stream", "sse")
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	respBody := w.Body.String()
	if strings.Contains(respBody, "searching") || strings.Contains(respBody, "search_results") || strings.Contains(respBody, "search_route") {
		t.Fatalf("embedding chat should not emit search events: %s", respBody)
	}
	if !strings.Contains(respBody, "embedding") || !strings.Contains(respBody, "0.1") {
		t.Fatalf("embedding chat response missing vector JSON: %s", respBody)
	}
	if engine.lastReq["input"] != "hello world" {
		t.Fatalf("embedding input = %#v, want hello world", engine.lastReq["input"])
	}
}

func TestHandleOpenAIChatCompletionsWithToolsSynthesizesToolCalls(t *testing.T) {
	engine := &fakeChatCompletionEngine{
		resp: api.OpenAIChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 123,
			Model:   "test/model",
			Choices: []api.OpenAIChoice{{
				Index: 0,
				Message: &api.Message{
					Role:    "assistant",
					Content: "get_time",
				},
			}},
		},
	}
	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "test",
		Name:         "model",
		Format:       model.FormatGGUF,
		Size:         1,
		Files:        []string{"model.gguf", "config.json"},
		DownloadedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"max_position_embeddings":40960}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	s := New(cfg, "test")
	s.engines["test/model"] = &managedEngine{engine: engine, numCtx: 16384, numParallel: 4}

	body := `{
	  "model": "test/model",
	  "messages": [{"role":"user","content":"Call get_time if a tool is available."}],
	  "tools": [{
	    "type":"function",
	    "function":{
	      "name":"get_time",
	      "description":"Get current time",
	      "parameters":{"type":"object","properties":{}}
	    }
	  }],
	  "tool_choice":"auto",
	  "parallel_tool_calls": false,
	  "stream": false
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}

	var resp api.OpenAIChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message == nil {
		t.Fatalf("unexpected choices payload: %#v", resp.Choices)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", resp.Choices[0].Message.ToolCalls)
	}
	if resp.Choices[0].Message.ToolCalls[0].Function.Name != "get_time" {
		t.Fatalf("unexpected tool call: %#v", resp.Choices[0].Message.ToolCalls[0])
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("unexpected finish reason: %#v", resp.Choices[0].FinishReason)
	}

	if engine.lastReq["tool_choice"] != "auto" {
		t.Fatalf("tool_choice was not forwarded: %#v", engine.lastReq["tool_choice"])
	}
	if engine.lastReq["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls was not forwarded: %#v", engine.lastReq["parallel_tool_calls"])
	}
	if engine.lastReq["stream"] != false {
		t.Fatalf("expected upstream tool request to disable streaming, got %#v", engine.lastReq["stream"])
	}
	tools, ok := engine.lastReq["tools"].([]api.Tool)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected forwarded tools, got %#v", engine.lastReq["tools"])
	}
}

func TestHandleOpenAIModelsUsesCsghubOwner(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3.5-2B",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	s := &Server{
		cfg:     cfg,
		manager: model.NewManager(cfg),
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()

	s.handleOpenAIModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}

	var resp api.OpenAIModelList
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected one model, got %#v", resp.Data)
	}
	if resp.Data[0].ID != "Qwen3.5-2B" {
		t.Fatalf("unexpected model id: %#v", resp.Data[0].ID)
	}
	if resp.Data[0].OwnedBy != "csghub" {
		t.Fatalf("unexpected owner: %#v", resp.Data[0].OwnedBy)
	}
}

func TestHandleOpenAIModelsIncludesCloudModels(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir(), Token: "test-token"}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3.5-2B",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	apiServer := newCloudOpenAIAPIServer(t, "test-token")
	defer apiServer.Close()

	s := &Server{
		cfg:     cfg,
		manager: model.NewManager(cfg),
		cloud:   cloud.NewService(apiServer.URL),
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()

	s.handleOpenAIModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}

	var resp api.OpenAIModelList
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected two models, got %#v", resp.Data)
	}
	if resp.Data[0].ID != "Qwen3.5-2B" {
		t.Fatalf("first model id = %q, want local model", resp.Data[0].ID)
	}
	if resp.Data[1].ID != "cloud/model" {
		t.Fatalf("second model id = %q, want cloud/model", resp.Data[1].ID)
	}
}

func TestHandleModelsAnthropicFormatIncludesCloudModels(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir(), Token: "test-token"}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3.5-2B",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "Qwen", "Qwen3.5-2B")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"max_position_embeddings":65536}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	apiServer := newCloudOpenAIAPIServer(t, "test-token")
	defer apiServer.Close()

	s := &Server{
		cfg:     cfg,
		manager: model.NewManager(cfg),
		cloud:   cloud.NewService(apiServer.URL),
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()

	s.handleModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}

	var resp api.AnthropicModelListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected two models, got %#v", resp.Data)
	}
	if resp.Data[0].ID != "Qwen3.5-2B" {
		t.Fatalf("first model id = %q, want local model", resp.Data[0].ID)
	}
	if resp.Data[1].ID != "cloud/model" {
		t.Fatalf("second model id = %q, want cloud/model", resp.Data[1].ID)
	}
	if resp.Data[1].Type != "model" {
		t.Fatalf("cloud model type = %q, want model", resp.Data[1].Type)
	}
	if resp.Data[0].MaxInputTokens != defaultAnthropicMaxInputTokens {
		t.Fatalf("local max_input_tokens = %d, want %d", resp.Data[0].MaxInputTokens, defaultAnthropicMaxInputTokens)
	}
	if resp.FirstID != "Qwen3.5-2B" || resp.LastID != "cloud/model" {
		t.Fatalf("unexpected pagination metadata: %#v", resp)
	}
}

func TestHandleModelsAnthropicFormatUsesCloudTokenLimits(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{
						"id":           "provider/model",
						"task":         "text-generation",
						"display_name": "Provider Model",
						"metadata": map[string]any{
							"contextWindow":   262144,
							"maxOutputTokens": 12288,
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	cfg := &config.Config{ModelDir: t.TempDir()}
	s := New(cfg, "test")
	s.cloud = cloud.NewService(apiServer.URL)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()

	s.handleModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}

	var resp api.AnthropicModelListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected one model, got %#v", resp.Data)
	}
	if resp.Data[0].ID != "provider/model" {
		t.Fatalf("model id = %q, want provider/model", resp.Data[0].ID)
	}
	if resp.Data[0].MaxInputTokens != 262144 {
		t.Fatalf("max_input_tokens = %d, want 262144", resp.Data[0].MaxInputTokens)
	}
	if resp.Data[0].MaxTokens != 12288 {
		t.Fatalf("max_tokens = %d, want 12288", resp.Data[0].MaxTokens)
	}
}

func TestHandleOpenAIChatCompletionsForwardsChatTemplateKwargs(t *testing.T) {
	engine := &fakeChatCompletionEngine{
		resp: api.OpenAIChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 123,
			Model:   "Qwen3.5-2B",
			Choices: []api.OpenAIChoice{{
				Index:   0,
				Message: &api.Message{Role: "assistant", Content: "ok"},
			}},
		},
	}
	cfg := &config.Config{ModelDir: t.TempDir()}
	s := New(cfg, "test")
	s.engines["Qwen3.5-2B"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1}

	body := `{
	  "model": "Qwen3.5-2B",
	  "messages": [{"role":"user","content":"hi"}],
	  "stream": false,
	  "chat_template_kwargs": {"enable_thinking": false, "custom": "value"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	kwargs, ok := engine.lastReq["chat_template_kwargs"].(map[string]interface{})
	if !ok {
		t.Fatalf("chat_template_kwargs missing or wrong type: %#v", engine.lastReq)
	}
	if got, ok := kwargs["enable_thinking"].(bool); !ok || got {
		t.Fatalf("enable_thinking = %#v, want false", kwargs["enable_thinking"])
	}
	if got := kwargs["custom"]; got != "value" {
		t.Fatalf("custom = %#v, want value", got)
	}
}

func TestHandleOpenAIChatCompletionsDisableThinkingHeaderSetsTemplateKwargs(t *testing.T) {
	engine := &fakeChatCompletionEngine{
		resp: api.OpenAIChatResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 123,
			Model:   "Qwen3.5-2B",
			Choices: []api.OpenAIChoice{{
				Index:   0,
				Message: &api.Message{Role: "assistant", Content: "ok"},
			}},
		},
	}
	cfg := &config.Config{ModelDir: t.TempDir()}
	s := New(cfg, "test")
	s.engines["Qwen3.5-2B"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1}

	body := `{"model":"Qwen3.5-2B","messages":[{"role":"user","content":"hi"}],"chat_template_kwargs":{"enable_thinking":true}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-CSGHUB-Disable-Thinking", "true")
	w := httptest.NewRecorder()

	s.handleOpenAIChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	kwargs, ok := engine.lastReq["chat_template_kwargs"].(map[string]interface{})
	if !ok {
		t.Fatalf("chat_template_kwargs missing or wrong type: %#v", engine.lastReq)
	}
	if got, ok := kwargs["enable_thinking"].(bool); !ok || got {
		t.Fatalf("enable_thinking = %#v, want false", kwargs["enable_thinking"])
	}
}

func TestHandleModelsAnthropicFormatUsesLoadedContextWhenLarger(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "MiniMaxAI",
		Name:         "MiniMax-M2.5",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf", "config.json"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "MiniMaxAI", "MiniMax-M2.5")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"max_position_embeddings":196608}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	s := New(cfg, "test")
	s.engines["MiniMaxAI/MiniMax-M2.5"] = &managedEngine{
		engine:      &fakeChatCompletionEngine{},
		numCtx:      160000,
		numParallel: 4,
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()

	s.handleModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}

	var resp api.AnthropicModelListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var target *api.AnthropicModelInfo
	for i := range resp.Data {
		if resp.Data[i].ID == "MiniMax-M2.5" {
			target = &resp.Data[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("MiniMax model not found in %#v", resp.Data)
	}
	if target.MaxInputTokens != 160000 {
		t.Fatalf("local max_input_tokens = %d, want 160000", target.MaxInputTokens)
	}
}

func TestHandleAnthropicMessagesSupportsCloudModels(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir(), OpenCSGAPIKey: "test-token"}
	apiServer := newCloudOpenAIAPIServer(t, "test-token")
	defer apiServer.Close()

	s := New(cfg, "test")
	s.cloud = cloud.NewService(apiServer.URL)

	body := `{"model":"cloud/model","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Anthropic-Version", "2023-06-01")
	w := httptest.NewRecorder()

	s.handleAnthropicMessages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp api.AnthropicMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model != "cloud/model" {
		t.Fatalf("model = %q, want cloud/model", resp.Model)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "cloud reply" {
		t.Fatalf("unexpected content: %#v", resp.Content)
	}
}

func TestHandleOpenAIChatCompletionsCloudStreamPreservesReasoningContent(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir(), OpenCSGAPIKey: "test-token"}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-v4-pro","object":"model","created":456,"owned_by":"opencsg"}]}`))
		case "/v1/chat/completions":
			if got := r.Header.Get("Accept"); got != "text/event-stream" {
				t.Fatalf("Accept = %q, want text/event-stream", got)
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode upstream request: %v", err)
			}
			if body["stream"] != true {
				t.Fatalf("stream = %#v, want true", body["stream"])
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"think\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	s := New(cfg, "test")
	s.cloud = cloud.NewService(apiServer.URL)

	body := `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `"reasoning_content":"think"`) {
		t.Fatalf("stream body missing reasoning_content: %s", respBody)
	}
	if strings.Contains(respBody, "<think>") {
		t.Fatalf("stream body converted reasoning to think tags: %s", respBody)
	}
}

func TestHandleOpenAIChatCompletionsCloudStreamWithToolsProxiesSSE(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir(), OpenCSGAPIKey: "test-token"}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"hy3","object":"model","created":456,"owned_by":"opencsg"}]}`))
		case "/v1/chat/completions":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode upstream request: %v", err)
			}
			if body["stream"] != true {
				t.Fatalf("stream = %#v, want true", body["stream"])
			}
			if _, ok := body["tools"]; !ok {
				t.Fatalf("upstream request missing tools: %#v", body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"partial\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_time\",\"arguments\":\"{}\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	s := New(cfg, "test")
	s.cloud = cloud.NewService(apiServer.URL)

	body := `{
	  "model": "hy3",
	  "messages": [{"role":"user","content":"what time is it"}],
	  "tools": [{
	    "type":"function",
	    "function":{"name":"get_time","description":"Get current time","parameters":{"type":"object","properties":{}}}
	  }],
	  "stream": true
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `"content":"partial"`) {
		t.Fatalf("stream body missing upstream content chunk: %s", respBody)
	}
	if !strings.Contains(respBody, `"name":"get_time"`) {
		t.Fatalf("stream body missing upstream tool_calls delta: %s", respBody)
	}
	if !strings.Contains(respBody, "data: [DONE]") {
		t.Fatalf("stream body missing [DONE]: %s", respBody)
	}
}

func TestHandleOpenAIChatCompletionsCloudWithoutTokenReturnsUnauthorized(t *testing.T) {
	s := newTestServer(t)
	apiServer := newCloudOpenAIAPIServer(t, "")
	defer apiServer.Close()
	s.cloud = cloud.NewService(apiServer.URL)

	body := `{"model":"cloud/model","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIChatCompletions(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}

	var resp struct {
		ErrorCode int `json:"errorCode"`
		Error     struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Type != "authentication_error" {
		t.Fatalf("error type = %q, want authentication_error", resp.Error.Type)
	}
	if resp.ErrorCode != http.StatusUnauthorized {
		t.Fatalf("errorCode = %d, want %d", resp.ErrorCode, http.StatusUnauthorized)
	}
	if !strings.Contains(resp.Error.Message, "Cloud login required") {
		t.Fatalf("error message = %q, want Cloud login required", resp.Error.Message)
	}
}

func TestHandleOpenAIResponsesCloudWithoutTokenReturnsUnauthorized(t *testing.T) {
	s := newTestServer(t)
	apiServer := newCloudOpenAIAPIServer(t, "")
	defer apiServer.Close()
	s.cloud = cloud.NewService(apiServer.URL)

	body := `{"model":"cloud/model","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIResponses(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}

	var resp struct {
		ErrorCode int `json:"errorCode"`
		Error     struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Type != "authentication_error" {
		t.Fatalf("error type = %q, want authentication_error", resp.Error.Type)
	}
	if resp.ErrorCode != http.StatusUnauthorized {
		t.Fatalf("errorCode = %d, want %d", resp.ErrorCode, http.StatusUnauthorized)
	}
	if !strings.Contains(resp.Error.Message, "Cloud login required") {
		t.Fatalf("error message = %q, want Cloud login required", resp.Error.Message)
	}
}
