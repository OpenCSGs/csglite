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

func TestHandleImageGenerationJobLifecycle(t *testing.T) {
	oldNewDiffusersEngine := newDiffusersEngine
	oldEnsureImageRuntimeReady := ensureImageRuntimeReady
	defer func() { newDiffusersEngine = oldNewDiffusersEngine }()
	defer func() { ensureImageRuntimeReady = oldEnsureImageRuntimeReady }()

	ensureImageRuntimeReady = func(context.Context, *imagegen.RuntimeManager, imagegen.ProgressFunc, bool) error {
		return nil
	}
	newDiffusersEngine = func(context.Context, string, string, *imagegen.RuntimeManager) (imagegen.Engine, error) {
		return &fakeImageEngine{}, nil
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
	body := `{"model":"Qwen/Qwen-Image","prompt":"a cat","size":"1024x1024","response_format":"b64_json"}`
	req := httptest.NewRequest(http.MethodPost, "/api/images/jobs", strings.NewReader(body))
	req.SetPathValue("jobID", "")
	w := httptest.NewRecorder()
	s.handleImageGenerationJobCreate(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	var job api.ImageGenerationJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if job.ID == "" {
		t.Fatal("job id is empty")
	}

	var got api.ImageGenerationJobResponse
	for i := 0; i < 20; i++ {
		req = httptest.NewRequest(http.MethodGet, "/api/images/jobs/"+job.ID, nil)
		req.SetPathValue("jobID", job.ID)
		w = httptest.NewRecorder()
		s.handleImageGenerationJobGet(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get status = %d body=%s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode get response: %v", err)
		}
		if got.Status == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.Status != "succeeded" || got.Result == nil || len(got.Result.Data) != 1 {
		t.Fatalf("job = %#v, want succeeded result", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images/jobs/"+job.ID+"/result", nil)
	req.SetPathValue("jobID", job.ID)
	w = httptest.NewRecorder()
	s.handleImageGenerationJobResult(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("result status = %d body=%s", w.Code, w.Body.String())
	}
	var result api.OpenAIImagesGenerationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result response: %v", err)
	}
	if len(result.Data) != 1 || result.Data[0].B64JSON == "" {
		t.Fatalf("result = %#v, want image data", result)
	}
}
