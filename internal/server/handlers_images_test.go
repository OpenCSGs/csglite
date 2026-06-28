package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
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

func testPNGBase64(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	img.Set(0, 1, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
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

func TestHandleOpenAIImagesGenerationsRejectsInputImage(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir()}
	s := New(cfg, "test")
	body := `{"model":"Qwen/Qwen-Image-Edit-2511","prompt":"edit","image":"aW5wdXQ="}`
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleOpenAIImagesGenerations(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/v1/images/edits") {
		t.Fatalf("body = %s, want edits redirect message", w.Body.String())
	}
}

func TestHandleOpenAIImagesEditsForwardsInputImage(t *testing.T) {
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
		Name:         "Qwen-Image-Edit-2511",
		Format:       model.FormatSafeTensors,
		Size:         1,
		Files:        []string{"model_index.json"},
		DownloadedAt: time.Now(),
		PipelineTag:  "image-to-image",
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "Qwen", "Qwen-Image-Edit-2511")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}

	s := New(cfg, "test")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", "Qwen/Qwen-Image-Edit-2511")
	_ = writer.WriteField("prompt", "make the sky orange")
	part, err := writer.CreateFormFile("image", "input.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("fake-png")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleOpenAIImagesEdits(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", w.Code, w.Body.String())
	}
	if fake.lastReq.Image == "" {
		t.Fatalf("input image was not forwarded: %#v", fake.lastReq)
	}
}

func TestHandleOpenAIImagesEditsAvoidsSystemTempDir(t *testing.T) {
	missingTempDir := filepath.Join(t.TempDir(), "missing-temp")
	t.Setenv("TMPDIR", missingTempDir)
	t.Setenv("TMP", missingTempDir)
	t.Setenv("TEMP", missingTempDir)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", "Qwen/Qwen-Image-Edit-2511")
	_ = writer.WriteField("prompt", "edit")
	part, err := writer.CreateFormFile("image", "large-input.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.CopyN(part, zeroReader{}, maxImageUploadMemory+1024); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	_ = writer.Close()

	storageDir := t.TempDir()
	cfg := &config.Config{
		ModelDir:   config.ModelDirForStorage(storageDir),
		DatasetDir: config.DatasetDirForStorage(storageDir),
	}
	s := New(cfg, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleOpenAIImagesEdits(w, req)

	if strings.Contains(w.Body.String(), "no such file or directory") {
		t.Fatalf("expected image edits upload to avoid system temp dir, got status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(missingTempDir); !os.IsNotExist(err) {
		t.Fatalf("expected system temp dir to remain unused, stat err=%v", err)
	}
	if _, err := os.Stat(cfg.TempDir()); err != nil {
		t.Fatalf("expected image edits cache to use lite temp dir: %v", err)
	}
	entries, err := os.ReadDir(cfg.TempDir())
	if err != nil {
		t.Fatalf("read lite temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected image edit temp files to be cleaned up, found %d entries", len(entries))
	}
}

func TestHandleOpenAIImagesGenerationsSupportsCloudModels(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want bearer test-key", got)
		}
		var raw map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if raw["model"] != "Qwen/Qwen-Image-2512:s-test" || raw["prompt"] != "a cat" || raw["size"] != "1024x1024" || raw["response_format"] != "b64_json" {
			t.Fatalf("cloud request = %#v, want common image fields", raw)
		}
		for _, key := range []string{"source", "cfg_scale", "steps", "seed", "negative_prompt"} {
			if _, ok := raw[key]; ok {
				t.Fatalf("cloud request includes non-portable field %q: %#v", key, raw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.OpenAIImagesGenerationResponse{
			Created: 456,
			Data: []api.OpenAIImage{{
				B64JSON: "Y2xvdWQtcG5n",
			}},
		})
	}))
	defer apiServer.Close()

	cfg := &config.Config{ModelDir: t.TempDir(), AIGatewayURL: apiServer.URL, OpenCSGAPIKey: "test-key"}
	s := New(cfg, "test")
	body := `{"model":"Qwen/Qwen-Image-2512:s-test","source":"cloud","prompt":"a cat","negative_prompt":"bad","size":"1024x1024","response_format":"b64_json","steps":8,"seed":123,"cfg_scale":7.5}`
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
	if len(resp.Data) != 1 || resp.Data[0].B64JSON != "Y2xvdWQtcG5n" {
		t.Fatalf("response = %#v, want cloud image data", resp)
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

func TestHandleImageGenerationJobSupportsCloudModels(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.OpenAIImagesGenerationResponse{
			Created: 789,
			Data: []api.OpenAIImage{{
				B64JSON: "Y2xvdWQtam9i",
			}},
		})
	}))
	defer apiServer.Close()

	cfg := &config.Config{ModelDir: t.TempDir(), AIGatewayURL: apiServer.URL, OpenCSGAPIKey: "test-key"}
	s := New(cfg, "test")
	body := `{"model":"Qwen/Qwen-Image-2512:s-test","source":"cloud","prompt":"a cat","size":"1024x1024","response_format":"b64_json"}`
	req := httptest.NewRequest(http.MethodPost, "/api/images/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleImageGenerationJobCreate(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	var job api.ImageGenerationJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	for i := 0; i < 20; i++ {
		req = httptest.NewRequest(http.MethodGet, "/api/images/jobs/"+job.ID, nil)
		req.SetPathValue("jobID", job.ID)
		w = httptest.NewRecorder()
		s.handleImageGenerationJobGet(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get status = %d body=%s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatalf("decode get response: %v", err)
		}
		if job.Status == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != "succeeded" || job.Result == nil || len(job.Result.Data) != 1 || job.Result.Data[0].B64JSON != "Y2xvdWQtam9i" {
		t.Fatalf("job = %#v, want succeeded cloud image result", job)
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

func TestHandleImageGenerationJobListPersistsHistory(t *testing.T) {
	imageData := testPNGBase64(t)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.OpenAIImagesGenerationResponse{
			Created: 999,
			Data: []api.OpenAIImage{{
				B64JSON: imageData,
			}},
		})
	}))
	defer apiServer.Close()

	storageDir := t.TempDir()
	cfg := &config.Config{
		ModelDir:      config.ModelDirForStorage(storageDir),
		DatasetDir:    config.DatasetDirForStorage(storageDir),
		AIGatewayURL:  apiServer.URL,
		OpenCSGAPIKey: "test-key",
	}
	s := New(cfg, "test")
	body := `{"model":"Qwen/Qwen-Image-2512:s-test","source":"cloud","prompt":"persist this","size":"1024x1024","response_format":"b64_json"}`
	req := httptest.NewRequest(http.MethodPost, "/api/images/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleImageGenerationJobCreate(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	var job api.ImageGenerationJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	for i := 0; i < 20; i++ {
		req = httptest.NewRequest(http.MethodGet, "/api/images/jobs/"+job.ID, nil)
		req.SetPathValue("jobID", job.ID)
		w = httptest.NewRecorder()
		s.handleImageGenerationJobGet(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get status = %d body=%s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatalf("decode get response: %v", err)
		}
		if job.Status == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != "succeeded" {
		t.Fatalf("job status = %q, want succeeded", job.Status)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images/jobs", nil)
	w = httptest.NewRecorder()
	s.handleImageGenerationJobList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", w.Code, w.Body.String())
	}
	var list api.ImageGenerationJobListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(list.Jobs) != 1 || list.Jobs[0].ID != job.ID || list.Jobs[0].Result == nil {
		t.Fatalf("list = %#v, want persisted succeeded job", list)
	}

	reloaded := New(cfg, "test")
	req = httptest.NewRequest(http.MethodGet, "/api/images/jobs", nil)
	w = httptest.NewRecorder()
	reloaded.handleImageGenerationJobList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reloaded list status = %d body=%s", w.Code, w.Body.String())
	}
	list = api.ImageGenerationJobListResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode reloaded list response: %v", err)
	}
	if len(list.Jobs) != 1 || list.Jobs[0].ID != job.ID || list.Jobs[0].Result == nil || list.Jobs[0].Result.Data[0].B64JSON == "" || list.Jobs[0].Result.Data[0].B64JSON == imageData {
		t.Fatalf("reloaded list = %#v, want thumbnail result", list)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images/jobs/"+job.ID+"/result", nil)
	req.SetPathValue("jobID", job.ID)
	w = httptest.NewRecorder()
	reloaded.handleImageGenerationJobResult(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reloaded result status = %d body=%s", w.Code, w.Body.String())
	}
	var result api.OpenAIImagesGenerationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode reloaded result response: %v", err)
	}
	if len(result.Data) != 1 || result.Data[0].B64JSON != imageData {
		t.Fatalf("reloaded result = %#v, want full persisted result", result)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images/jobs/"+job.ID, nil)
	req.SetPathValue("jobID", job.ID)
	w = httptest.NewRecorder()
	reloaded.handleImageGenerationJobCancel(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", w.Code, w.Body.String())
	}

	afterDelete := New(cfg, "test")
	req = httptest.NewRequest(http.MethodGet, "/api/images/jobs", nil)
	w = httptest.NewRecorder()
	afterDelete.handleImageGenerationJobList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("after delete list status = %d body=%s", w.Code, w.Body.String())
	}
	list = api.ImageGenerationJobListResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode after delete list response: %v", err)
	}
	if len(list.Jobs) != 0 {
		t.Fatalf("after delete list = %#v, want empty history", list)
	}
}
