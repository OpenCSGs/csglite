package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
)

type unsupportedToolEngine struct {
	*fakeChatCompletionEngine
}

func (e *unsupportedToolEngine) ToolCallingSupported() (bool, string) {
	return false, "SGLang tool-call parser is unknown for this model"
}

func TestCloudAndProviderSourcesNeverReachLocalLoader(t *testing.T) {
	s := newTestServerWithConfig(t, &config.Config{ModelDir: t.TempDir()})
	original := loadEngineWithProgress
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		t.Fatal("local loader reached for a non-local source")
		return nil, errors.New("unexpected local load")
	}
	t.Cleanup(func() { loadEngineWithProgress = original })

	for _, source := range []string{"cloud", "provider:test"} {
		_, _ = s.getChatEngine(context.Background(), "test/model", source, 0, 0, -1, "", "", "")
	}
}

func TestToolRequestRefusedThroughOpenAIAnthropicAndResponsesRoutes(t *testing.T) {
	engine := &unsupportedToolEngine{fakeChatCompletionEngine: &fakeChatCompletionEngine{}}
	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace: "test", Name: "model", Format: model.FormatGGUF, Size: 1,
		Files: []string{"model.gguf", "config.json"}, DownloadedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"max_position_embeddings":8192}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestServerWithConfig(t, cfg)
	s.engines["test/model"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1, nGPULayers: -1}

	openAIBody := `{"model":"test/model","messages":[{"role":"user","content":"time?"}],"tools":[{"type":"function","function":{"name":"get_time","parameters":{"type":"object"}}}],"stream":false}`
	recorder := httptest.NewRecorder()
	s.handleOpenAIChatCompletions(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(openAIBody)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "tool-call parser") {
		t.Fatalf("OpenAI route = %d %s", recorder.Code, recorder.Body.String())
	}

	anthropicBody := `{"model":"test/model","max_tokens":64,"messages":[{"role":"user","content":"time?"}],"tools":[{"name":"get_time","description":"Get time","input_schema":{"type":"object"}}]}`
	recorder = httptest.NewRecorder()
	s.handleAnthropicMessages(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(anthropicBody)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "tool-call parser") {
		t.Fatalf("Anthropic route = %d %s", recorder.Code, recorder.Body.String())
	}

	responsesBody := `{"model":"test/model","input":"time?","tools":[{"type":"function","name":"get_time","parameters":{"type":"object"}}],"stream":false}`
	recorder = httptest.NewRecorder()
	s.handleOpenAIResponses(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(responsesBody)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "tool-call parser") {
		t.Fatalf("Responses route = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLegacyFunctionsRefusedThroughRealRoute(t *testing.T) {
	engine := &unsupportedToolEngine{fakeChatCompletionEngine: &fakeChatCompletionEngine{}}
	s := newTestServerWithConfig(t, &config.Config{ModelDir: t.TempDir()})
	s.engines["test/model"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1, nGPULayers: -1}
	body := `{"model":"test/model","messages":[{"role":"user","content":"time?"}],"functions":[{"name":"get_time","parameters":{"type":"object"}}],"function_call":"auto","stream":false}`
	recorder := httptest.NewRecorder()
	s.handleOpenAIChatCompletions(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("legacy functions = %d %s", recorder.Code, recorder.Body.String())
	}
}
