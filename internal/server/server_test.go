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

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

type fakeEngine struct{}

func (f *fakeEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (f *fakeEngine) Chat(context.Context, []inference.Message, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (f *fakeEngine) Close() error { return nil }

func (f *fakeEngine) ModelName() string { return "fake" }

func TestMain(m *testing.M) {
	_ = os.Setenv(config.DisableFileLoggingEnv, "1")
	if home, err := os.MkdirTemp("", "csghub-lite-server-test-home-*"); err == nil {
		_ = os.Setenv("HOME", home)
		_ = os.Setenv("USERPROFILE", home)
	}
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	os.Exit(m.Run())
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)
	dir := t.TempDir()
	cfg := &config.Config{
		ServerURL:  "https://hub.opencsg.com",
		ListenAddr: ":0",
		ModelDir:   filepath.Join(dir, "models"),
		DatasetDir: filepath.Join(dir, "datasets"),
	}
	s := New(cfg, "test")
	s.cloud = cloud.NewService("")
	return s
}

func TestHandleHealth(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want %q", resp["status"], "ok")
	}
}

func TestGetChatEngineReusesLoadedEngineWhenOverridesOmitted(t *testing.T) {
	s := newTestServer(t)
	engine := &fakeChatCompletionEngine{}
	s.engines["test/model"] = &managedEngine{
		engine:      engine,
		numCtx:      160000,
		numParallel: 4,
		lastUsed:    time.Now(),
		keepAlive:   DefaultKeepAlive,
	}

	got, err := s.getChatEngine(context.Background(), "test/model", "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatalf("getChatEngine returned error: %v", err)
	}
	if got != engine {
		t.Fatalf("getChatEngine returned %#v, want existing engine %#v", got, engine)
	}
	if s.engines["test/model"].numCtx != 160000 {
		t.Fatalf("numCtx = %d, want 160000", s.engines["test/model"].numCtx)
	}
	if s.engines["test/model"].numParallel != 4 {
		t.Fatalf("numParallel = %d, want 4", s.engines["test/model"].numParallel)
	}
}

func TestLoadedEngineReloadsWhenRequestedDTypeGGUFMissing(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    model.FormatSafeTensors,
		Size:      123,
		Files:     []string{"model.safetensors"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), []byte("safetensors"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()

	newEngine := &fakeEngine{}
	loadEngineWithProgress = func(_ string, _ *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, _ int, _ int, _ int, _ string, _ string, dtype string) (inference.Engine, error) {
		if dtype != "q8_0" {
			t.Fatalf("dtype = %q, want q8_0", dtype)
		}
		return newEngine, nil
	}

	s.engines["test/model"] = &managedEngine{
		engine:      &fakeEngine{},
		numCtx:      inference.ResolveNumCtx(modelDir, 0),
		numParallel: inference.ResolveNumParallel(0),
		nGPULayers:  inference.ResolveNGPULayers(-1),
		lastUsed:    time.Now(),
		keepAlive:   api.KeepAliveForever,
	}

	got, err := s.getOrLoadEngineWithOpts("test/model", 0, 0, -1, "", "", "q8_0")
	if err != nil {
		t.Fatalf("getOrLoadEngineWithOpts returned error: %v", err)
	}
	if got != newEngine {
		t.Fatalf("engine = %#v, want reloaded engine %#v", got, newEngine)
	}
	if got := s.engines["test/model"].keepAlive; got != api.KeepAliveForever {
		t.Fatalf("keepAlive = %s, want forever", got)
	}
}

func TestLoadedEngineReusesRequestedDTypeWhenGGUFExists(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    model.FormatGGUF,
		Size:      123,
		Files:     []string{"model-q8_0.gguf"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model-q8_0.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		t.Fatal("loadEngineWithProgress should not be called")
		return nil, nil
	}

	engine := &fakeEngine{}
	s.engines["test/model"] = &managedEngine{
		engine:      engine,
		numCtx:      inference.ResolveNumCtx(modelDir, 0),
		numParallel: inference.ResolveNumParallel(0),
		nGPULayers:  inference.ResolveNGPULayers(-1),
		dtype:       "q8_0",
		lastUsed:    time.Now(),
		keepAlive:   api.KeepAliveForever,
	}

	got, err := s.getOrLoadEngineWithOpts("test/model", 0, 0, -1, "", "", "q8_0")
	if err != nil {
		t.Fatalf("getOrLoadEngineWithOpts returned error: %v", err)
	}
	if got != engine {
		t.Fatalf("engine = %#v, want existing engine %#v", got, engine)
	}
}

func TestReloadPreservesExistingKeepAlive(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    model.FormatGGUF,
		Size:      123,
		Files:     []string{"model.gguf"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()

	newEngine := &fakeEngine{}
	loadEngineWithProgress = func(_ string, _ *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, numCtx int, _ int, _ int, _ string, _ string, dtype string) (inference.Engine, error) {
		if numCtx != 131072 {
			t.Fatalf("numCtx = %d, want 131072", numCtx)
		}
		if dtype != "q8_0" {
			t.Fatalf("dtype = %q, want q8_0", dtype)
		}
		return newEngine, nil
	}

	s.engines["test/model"] = &managedEngine{
		engine:      &fakeEngine{},
		numCtx:      8192,
		numParallel: inference.ResolveNumParallel(0),
		nGPULayers:  inference.ResolveNGPULayers(-1),
		lastUsed:    time.Now().Add(-time.Hour),
		keepAlive:   api.KeepAliveForever,
	}

	got, err := s.getOrLoadEngineWithOpts("test/model", 131072, 0, -1, "", "", "q8_0")
	if err != nil {
		t.Fatalf("getOrLoadEngineWithOpts returned error: %v", err)
	}
	if got != newEngine {
		t.Fatalf("engine = %#v, want reloaded engine %#v", got, newEngine)
	}
	if got := s.engines["test/model"].keepAlive; got != api.KeepAliveForever {
		t.Fatalf("keepAlive = %s, want forever", got)
	}
}

func TestHandlePsRemainsResponsiveWhileModelLoads(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "slow",
		Format:    model.FormatSafeTensors,
		Size:      123,
		Files:     []string{"model.safetensors"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "slow")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()

	started := make(chan struct{})
	release := make(chan struct{})
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		close(started)
		<-release
		return &fakeEngine{}, nil
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := s.getOrLoadEngineWithOpts("test/slow", 0, 0, -1, "", "", "")
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for model load to start")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/ps", nil)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handlePs(w, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handlePs blocked while model load was in progress")
	}
	var psResp api.PsResponse
	if err := json.NewDecoder(w.Body).Decode(&psResp); err != nil {
		t.Fatalf("decode ps response: %v", err)
	}
	if len(psResp.Models) != 1 {
		t.Fatalf("running models = %d, want loading model; body=%s", len(psResp.Models), w.Body.String())
	}
	if got := psResp.Models[0].Status; got != "loading" {
		t.Fatalf("status = %q, want loading", got)
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("getOrLoadEngineWithOpts() error = %v", err)
	}
}

func TestConcurrentLoadsShareSingleInFlightLoad(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "shared",
		Format:    model.FormatSafeTensors,
		Size:      123,
		Files:     []string{"model.safetensors"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "shared")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()

	var (
		calls   int
		engine  = &fakeEngine{}
		started = make(chan struct{})
		release = make(chan struct{})
	)
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		calls++
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return engine, nil
	}

	type result struct {
		eng inference.Engine
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			eng, err := s.getOrLoadEngineWithOpts("test/shared", 0, 0, -1, "", "", "")
			results <- result{eng: eng, err: err}
		}()
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shared load to start")
	}
	close(release)

	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil {
			t.Fatalf("getOrLoadEngineWithOpts() error = %v", res.err)
		}
		if res.eng != engine {
			t.Fatalf("engine = %#v, want shared engine %#v", res.eng, engine)
		}
	}
	if calls != 1 {
		t.Fatalf("loadEngineWithProgress call count = %d, want 1", calls)
	}
}

func TestHandleTags_Empty(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Errorf("models len = %d, want 0", len(resp.Models))
	}
}

func TestHandleTags_WithModels(t *testing.T) {
	s := newTestServer(t)

	// Create a model manifest
	lm := &model.LocalModel{
		Namespace:    "test",
		Name:         "model",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	model.SaveManifest(s.cfg.ModelDir, lm)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.TagsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Models) != 1 {
		t.Fatalf("models len = %d, want 1", len(resp.Models))
	}
	if resp.Models[0].Name != "test/model" {
		t.Errorf("model name = %q, want %q", resp.Models[0].Name, "test/model")
	}
}

func TestHandlePipelineTags(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pipeline-tags", nil)
	w := httptest.NewRecorder()

	s.handlePipelineTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.PipelineTagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.PipelineTags) != 5 {
		t.Fatalf("pipeline_tags len = %d, want 5", len(resp.PipelineTags))
	}
	if resp.PipelineTags[0].Category != "language_model" || resp.PipelineTags[0].Label != "语言模型" {
		t.Fatalf("first group = %#v, want language model group", resp.PipelineTags[0])
	}
	if got := strings.Join(resp.PipelineTags[0].Tags, ","); got != "text-generation,conversational,text2text-generation,fill-mask" {
		t.Fatalf("language model tags = %q", got)
	}
}

func TestHandleTagsProviderFilterLocal(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace:    "test",
		Name:         "model",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	model.SaveManifest(s.cfg.ModelDir, lm)

	req := httptest.NewRequest(http.MethodGet, "/api/tags?provider=local", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Provider != "local" || resp.Models[0].Source != "local" {
		t.Fatalf("models = %#v, want one local provider model", resp.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode filtered response: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Fatalf("models len = %d, want 0 for non-local provider", len(resp.Models))
	}
}

func TestHandleShow(t *testing.T) {
	s := newTestServer(t)

	// Create a model
	lm := &model.LocalModel{
		Namespace:    "ns",
		Name:         "mdl",
		Format:       model.FormatGGUF,
		Size:         2048,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	model.SaveManifest(s.cfg.ModelDir, lm)

	body := `{"model": "ns/mdl"}`
	req := httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleShow(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.ShowResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Details.Name != "ns/mdl" {
		t.Errorf("details.name = %q, want %q", resp.Details.Name, "ns/mdl")
	}
}

func TestHandleShow_NotFound(t *testing.T) {
	s := newTestServer(t)

	body := `{"model": "nonexistent/model"}`
	req := httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleShow(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDelete(t *testing.T) {
	s := newTestServer(t)

	// Create a model
	lm := &model.LocalModel{
		Namespace: "ns",
		Name:      "todelete",
		Format:    model.FormatGGUF,
		Size:      100,
		Files:     []string{"model.gguf"},
	}
	model.SaveManifest(s.cfg.ModelDir, lm)

	body := `{"model": "ns/todelete"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/delete", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleDelete(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleDelete_NotFound(t *testing.T) {
	s := newTestServer(t)

	body := `{"model": "nonexistent/model"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/delete", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGenerate_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()

	s.handleGenerate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleLoad_InvalidCacheType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","cache_type_k":"fp8"}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported cache type") {
		t.Fatalf("body = %q, want unsupported cache type", w.Body.String())
	}
}

func TestHandleLoad_InvalidDType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","dtype":"q4_k_m"}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported dtype") {
		t.Fatalf("body = %q, want unsupported dtype", w.Body.String())
	}
}

func TestHandleLoad_InvalidKeepAlive(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","keep_alive":"later"}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "keep_alive") {
		t.Fatalf("body = %q, want keep_alive validation error", w.Body.String())
	}
}

func TestHandleLoad_ForeverKeepAliveOnExistingEngine(t *testing.T) {
	s := newTestServer(t)
	s.engines["test/model"] = &managedEngine{
		engine:    &fakeChatCompletionEngine{},
		lastUsed:  time.Now().Add(-time.Hour),
		keepAlive: DefaultKeepAlive,
	}

	body := `{"model":"test/model","keep_alive":"-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := s.engines["test/model"].keepAlive; got != api.KeepAliveForever {
		t.Fatalf("keepAlive = %s, want forever", got)
	}
	if !s.engines["test/model"].expiresAt().IsZero() {
		t.Fatalf("expiresAt = %v, want zero time for forever keep-alive", s.engines["test/model"].expiresAt())
	}
}

func TestEvictExpiredSkipsForeverKeepAlive(t *testing.T) {
	s := newTestServer(t)
	s.engines["test/model"] = &managedEngine{
		engine:    &fakeChatCompletionEngine{},
		lastUsed:  time.Now().Add(-24 * time.Hour),
		keepAlive: api.KeepAliveForever,
	}

	s.evictExpired(time.Now())

	if _, ok := s.engines["test/model"]; !ok {
		t.Fatal("expected forever keep-alive engine to remain loaded")
	}
}

func TestHandleChat_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleChat_CloudWithoutTokenReturnsUnauthorized(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"Qwen/Qwen3.5-35B-A3B-FP8:s-qwen-qwen3-5-35b-a3b-fp8-6dp9","source":"cloud","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp["error"], "Cloud login required") {
		t.Fatalf("error = %q, want Cloud login required", resp["error"])
	}
}

func TestHandleAnthropicMessages_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	s.handleAnthropicMessages(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAnthropicCountTokens(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","system":"You are helpful","messages":[{"role":"user","content":"hello there"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleAnthropicCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.AnthropicCountTokensResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.InputTokens <= 0 {
		t.Fatalf("input_tokens = %d, want > 0", resp.InputTokens)
	}
}

func TestHandleOpenAIResponses_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	s.handleOpenAIResponses(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGenerate_ModelNotFound(t *testing.T) {
	s := newTestServer(t)

	body := `{"model": "nonexistent/model", "prompt": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleGenerate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGenerate_InvalidCacheType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","prompt":"hello","options":{"cache_type_v":"fp8"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleGenerate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported cache type") {
		t.Fatalf("body = %q, want unsupported cache type", w.Body.String())
	}
}

func TestHandleGenerate_InvalidDType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","prompt":"hello","options":{"dtype":"q4_k_m"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleGenerate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported dtype") {
		t.Fatalf("body = %q, want unsupported dtype", w.Body.String())
	}
}

func TestRoutes(t *testing.T) {
	s := newTestServer(t)
	mux := s.routes()

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/"},
		{"GET", "/api/tags"},
		{"GET", "/api/pipeline-tags"},
		{"GET", "/api/tags/manage"},
		{"POST", "/api/tags/manage"},
		{"PUT", "/api/tags/manage"},
		{"DELETE", "/api/tags/manage"},
		{"GET", "/api/models/search"},
		{"GET", "/api/models/test/model/manifest"},
		{"GET", "/api/models/test/model/files/model.gguf"},
		{"GET", "/api/datasets"},
		{"GET", "/api/datasets/search"},
		{"GET", "/api/datasets/test/data/manifest"},
		{"GET", "/api/datasets/test/data/files/file.txt"},
		{"POST", "/api/show"},
		{"POST", "/api/pull"},
		{"DELETE", "/api/delete"},
		{"POST", "/api/generate"},
		{"POST", "/api/chat"},
		{"GET", "/api/settings"},
		{"POST", "/api/settings"},
		{"POST", "/api/settings/directories"},
		{"GET", "/v1/responses"},
		{"POST", "/v1/responses"},
		{"POST", "/v1/messages"},
		{"POST", "/v1/messages/count_tokens"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			// Just verify no panic and some response
			if w.Code == 0 {
				t.Error("got status 0")
			}
		})
	}
}
