package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

func newSGLangConfigTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := &config.Config{ModelDir: t.TempDir()}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace: "test", Name: "native", Format: model.FormatSafeTensors,
		PipelineTag: "text-generation", Size: 7, Files: []string{"model.safetensors", "config.json"},
		DownloadedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(cfg.ModelDir, "test", "native")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{"architectures":["LlamaForCausalLM"],"model_type":"llama","torch_dtype":"bfloat16","num_hidden_layers":2,"num_attention_heads":4,"num_key_value_heads":4,"hidden_size":256,"max_position_embeddings":8192}`
	for name, content := range map[string]string{"config.json": configJSON, "model.safetensors": "weights"} {
		if err := os.WriteFile(filepath.Join(modelDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := newTestServerWithConfig(t, cfg)
	return s, modelDir
}

func disableSGLangForTest(t *testing.T) {
	t.Helper()
	t.Setenv("CSGHUB_LITE_SGLANG_DISABLE", "1")
	inference.ResetSGLangRuntimeCache()
	t.Cleanup(inference.ResetSGLangRuntimeCache)
}

func modelConfigUpdate(t *testing.T, s *Server, modelID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/models/"+modelID+"/config", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleModelConfigUpdateForID(recorder, request, modelID)
	return recorder
}

func TestModelConfigReportsUnavailableSGLangWithoutOfferingIt(t *testing.T) {
	s, modelDir := newSGLangConfigTestServer(t)
	disableSGLangForTest(t)

	response := s.modelConfigResponse("test/native", modelDir)
	if response.Backend != "" || response.EffectiveBackend != api.InferenceBackendLlama {
		t.Fatalf("backend/effective = %q/%q, want auto/llama", response.Backend, response.EffectiveBackend)
	}
	if response.Runtime != api.ModelRuntimeLlama {
		t.Fatalf("runtime = %q, want llama", response.Runtime)
	}
	var sglang *api.InferenceRuntimeCapability
	for i := range response.Backends {
		if response.Backends[i].Backend == api.InferenceBackendSGLang {
			sglang = &response.Backends[i]
		}
	}
	if sglang == nil || sglang.Available || strings.TrimSpace(sglang.Reason) == "" {
		t.Fatalf("SGLang capability = %+v, want unavailable with a reason", sglang)
	}
	if response.SGLang != nil {
		t.Fatalf("SGLang plan advertised for an unavailable runtime: %+v", response.SGLang)
	}

	recorder := modelConfigUpdate(t, s, "test/native", `{"backend":"sglang"}`)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unavailable") {
		t.Fatalf("explicit SGLang save = %d %s, want 400 with the reason", recorder.Code, recorder.Body.String())
	}
}

func TestModelConfigBackendPersistsAndLegacyPayloadKeepsIt(t *testing.T) {
	s, modelDir := newSGLangConfigTestServer(t)
	disableSGLangForTest(t)

	recorder := modelConfigUpdate(t, s, "test/native", `{"backend":"llama"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("save backend = %d %s", recorder.Code, recorder.Body.String())
	}
	var saved api.ModelConfigResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Backend != api.InferenceBackendLlama || saved.EffectiveBackend != api.InferenceBackendLlama {
		t.Fatalf("saved backend/effective = %q/%q", saved.Backend, saved.EffectiveBackend)
	}

	// A client written before the backend field must not clear it.
	recorder = modelConfigUpdate(t, s, "test/native", `{"num_ctx":16384}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("legacy update = %d %s", recorder.Code, recorder.Body.String())
	}
	var updated api.ModelConfigResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Backend != api.InferenceBackendLlama || updated.NumCtx != 16384 {
		t.Fatalf("legacy update changed backend: %+v", updated)
	}
	if got := s.modelConfigResponse("test/native", modelDir).EffectiveBackend; got != api.InferenceBackendLlama {
		t.Fatalf("effective backend after legacy update = %q", got)
	}
}

func TestModelConfigRejectsSGLangWithGGUFQuantization(t *testing.T) {
	s, _ := newSGLangConfigTestServer(t)
	disableSGLangForTest(t)
	recorder := modelConfigUpdate(t, s, "test/native", `{"backend":"sglang","dtype":"q4_k_m"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
}

func TestModelConfigAdvertisesSGLangPlanWhenAvailable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native SGLang is Linux-only; the platform reason is covered elsewhere")
	}
	python := filepath.Join(t.TempDir(), "python3")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"installed\":true,\"version\":\"0.5.19\",\"cuda\":true,\"rocm\":false,\"devices\":[{\"index\":0,\"name\":\"NVIDIA A100-SXM4-80GB\",\"total\":85899345920}]}'\n"
	if err := os.WriteFile(python, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CSGHUB_LITE_SGLANG_DISABLE", "0")
	t.Setenv("CSGHUB_LITE_SGLANG_PYTHON", python)
	inference.ResetSGLangRuntimeCache()
	t.Cleanup(inference.ResetSGLangRuntimeCache)

	s, modelDir := newSGLangConfigTestServer(t)
	recorder := modelConfigUpdate(t, s, "test/native", `{"backend":"sglang","num_ctx":16384,"num_parallel":2}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("save SGLang = %d %s", recorder.Code, recorder.Body.String())
	}
	response := s.modelConfigResponse("test/native", modelDir)
	if response.Backend != api.InferenceBackendSGLang || response.EffectiveBackend != api.InferenceBackendSGLang {
		t.Fatalf("backend/effective = %q/%q", response.Backend, response.EffectiveBackend)
	}
	if response.Runtime != api.ModelRuntimeSGLang {
		t.Fatalf("runtime = %q, want sglang", response.Runtime)
	}
	if response.SGLang == nil {
		t.Fatal("SGLang plan missing")
	}
	if response.SGLang.ContextLength != 8192 {
		t.Fatalf("context = %d, want the model maximum", response.SGLang.ContextLength)
	}
	if response.SGLang.MaxRunningRequests != 2 || response.SGLang.MaxTotalTokens != 16384 {
		t.Fatalf("parallel/total = %d/%d", response.SGLang.MaxRunningRequests, response.SGLang.MaxTotalTokens)
	}
	if response.SGLang.ToolCallingSupported || response.SGLang.ToolCallingReason == "" {
		t.Fatalf("unknown tiny model advertised tool calling: %+v", response.SGLang)
	}
	if response.EffectiveNumCtx != 8192 || response.EffectiveNumParallel != 2 {
		t.Fatalf("effective ctx/parallel = %d/%d", response.EffectiveNumCtx, response.EffectiveNumParallel)
	}
}

func TestSGLangLoadUsesRequestContextOverSavedSetting(t *testing.T) {
	s, _ := newSGLangConfigTestServer(t)
	storageID := s.resolveLocalModelStorageID("test/native")
	s.cfg.Inference.SetModelSettings(storageID, config.ModelRuntimeSettings{NumCtx: 8192, NumParallel: 2, Backend: "sglang"})

	original := loadEngineWithBackendSpeculativeProgress
	gotNumCtx, gotNumParallel := -1, -1
	loadEngineWithBackendSpeculativeProgress = func(_ string, _ *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, numCtx, numParallel, _ int, _, _, _ string, _ inference.EngineBackendOptions, _ inference.SpeculativeConfig) (inference.Engine, error) {
		gotNumCtx, gotNumParallel = numCtx, numParallel
		return &fakeChatCompletionEngine{}, nil
	}
	t.Cleanup(func() { loadEngineWithBackendSpeculativeProgress = original })

	if _, err := s.getOrLoadEngineFullMode("test/native", nil, 1024, 1, -1, "", "", "", engineModeChat, inference.SpeculativeConfig{}, false); err != nil {
		t.Fatal(err)
	}
	if gotNumCtx != 1024 {
		t.Fatalf("loader received num_ctx=%d, want the explicit request 1024", gotNumCtx)
	}
	if gotNumParallel != 1 {
		t.Fatalf("loader received num_parallel=%d, want the explicit request 1", gotNumParallel)
	}
}

func TestSGLangKeepAliveDefaultAndOverrides(t *testing.T) {
	s, _ := newSGLangConfigTestServer(t)
	const modelID = "test/native"
	storageID := s.resolveLocalModelStorageID(modelID)
	s.cfg.Inference.SetModelSettings(storageID, config.ModelRuntimeSettings{Backend: inference.SGLangBackend})
	original := loadEngineWithBackendSpeculativeProgress
	loadEngineWithBackendSpeculativeProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string, inference.EngineBackendOptions, inference.SpeculativeConfig) (inference.Engine, error) {
		return &fakeChatCompletionEngine{}, nil
	}
	t.Cleanup(func() { loadEngineWithBackendSpeculativeProgress = original })

	rec, response := modelConfigRequest(t, s, http.MethodGet, modelID, "")
	if rec.Code != http.StatusOK || response.GlobalKeepAlive != "-1" || response.EffectiveKeepAlive != "-1" {
		t.Fatalf("default model config = %d %s, want keep-alive -1", rec.Code, rec.Body)
	}

	cacheKey := engineCacheKey(storageID, engineModeChat)
	for _, test := range []struct {
		name      string
		saved     string
		requested string
		want      time.Duration
	}{
		{name: "default", want: api.KeepAliveForever},
		{name: "saved override", saved: "30s", want: 30 * time.Second},
		{name: "request override", saved: "30s", requested: "2m", want: 2 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			s.closeEngineKey(cacheKey)
			s.cfg.Inference.SetModelSettings(storageID, config.ModelRuntimeSettings{
				Backend:   inference.SGLangBackend,
				KeepAlive: test.saved,
			})
			body := `{"model":"test/native","keep_alive":"` + test.requested + `"}`
			request := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
			recorder := httptest.NewRecorder()
			s.handleLoad(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("load = %d %s", recorder.Code, recorder.Body)
			}
			if got := s.engines[cacheKey].keepAlive; got != test.want {
				t.Fatalf("loaded keep-alive = %s, want %s", got, test.want)
			}
		})
	}

	rec, response = modelConfigRequest(t, s, http.MethodGet, modelID, "")
	if rec.Code != http.StatusOK || response.EffectiveKeepAlive != "30s" {
		t.Fatalf("saved model config = %d %s, want keep-alive 30s", rec.Code, rec.Body)
	}
}
