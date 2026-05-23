package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/imagegen"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

type fakeImageEngine struct {
	lastReq api.OpenAIImagesGenerationRequest
}

func (e *fakeImageEngine) Generate(_ context.Context, req api.OpenAIImagesGenerationRequest) (*api.OpenAIImagesGenerationResponse, error) {
	e.lastReq = req
	return &api.OpenAIImagesGenerationResponse{
		Created: 123,
		Data: []api.OpenAIImage{{
			B64JSON: "ZmFrZS1wbmc=",
		}},
	}, nil
}

func (e *fakeImageEngine) Close() error { return nil }

func (e *fakeImageEngine) ModelName() string { return "Qwen/Qwen-Image" }

func TestHandleOpenAIImagesGenerations(t *testing.T) {
	oldNewDiffusersEngine := newDiffusersEngine
	oldEnsureImageRuntimeReady := ensureImageRuntimeReady
	defer func() { newDiffusersEngine = oldNewDiffusersEngine }()
	defer func() { ensureImageRuntimeReady = oldEnsureImageRuntimeReady }()

	fake := &fakeImageEngine{}
	ensureImageRuntimeReady = func(context.Context, *imagegen.RuntimeManager, imagegen.ProgressFunc, bool) error {
		return nil
	}
	newDiffusersEngine = func(context.Context, string, string, *imagegen.RuntimeManager) (imagegen.Engine, error) {
		return fake, nil
	}

	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen-Image",
		Format:       model.FormatSafeTensors,
		Size:         1,
		Files:        []string{"model_index.json"},
		DownloadedAt: time.Now(),
		PipelineTag:  "text-to-image",
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "Qwen", "Qwen-Image")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}

	s := New(cfg, "test")
	body := `{"model":"Qwen/Qwen-Image","prompt":"a cat","size":"1024x1024","response_format":"b64_json","steps":8}`
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIImagesGenerations(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	var resp api.OpenAIImagesGenerationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].B64JSON == "" {
		t.Fatalf("unexpected image response: %#v", resp)
	}
	if fake.lastReq.Prompt != "a cat" {
		t.Fatalf("prompt was not forwarded: %#v", fake.lastReq)
	}
}

func TestHandleOpenAIImagesGenerationsRejectsTextModel(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3",
		Format:       model.FormatGGUF,
		Size:         1,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
		PipelineTag:  "text-generation",
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "Qwen", "Qwen3")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}

	s := New(cfg, "test")
	body := `{"model":"Qwen/Qwen3","prompt":"a cat"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIImagesGenerations(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
}
