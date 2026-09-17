package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

func seedModelForConfig(t *testing.T, s *Server, maxPositionEmbeddings string) string {
	t.Helper()
	lm := &model.LocalModel{
		Namespace:   "Acme",
		Name:        "ctx-model",
		Format:      model.FormatSafeTensors,
		PipelineTag: "text-generation",
	}
	modelDir := model.ModelDir(s.cfg.ModelDir, lm.Namespace, lm.Name)
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if maxPositionEmbeddings != "" {
		body := `{"max_position_embeddings":` + maxPositionEmbeddings + `}`
		if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := model.SaveManifestInDir(modelDir, lm); err != nil {
		t.Fatal(err)
	}
	return lm.Namespace + "/" + lm.Name
}

func modelConfigRequest(t *testing.T, s *Server, method, modelID, body string) (*httptest.ResponseRecorder, api.ModelConfigResponse) {
	t.Helper()
	namespace, name, _ := strings.Cut(modelID, "/")
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/models/"+namespace+"/"+name+"/config", reader)
	req.SetPathValue("namespace", namespace)
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	if method == http.MethodGet {
		s.handleModelConfig(rec, req)
	} else {
		s.handleModelConfigUpdate(rec, req)
	}
	var out api.ModelConfigResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v (body %q)", err, rec.Body.String())
		}
	}
	return rec, out
}

func TestModelConfigDefaultsToGlobalSetting(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "16384")

	rec, got := modelConfigRequest(t, s, http.MethodGet, modelID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.NumCtx != 0 {
		t.Fatalf("NumCtx = %d, want 0 (unset)", got.NumCtx)
	}
	if got.ModelMaxNumCtx != 40960 {
		t.Fatalf("ModelMaxNumCtx = %d, want 40960", got.ModelMaxNumCtx)
	}
	if got.EffectiveNumCtx != 16384 {
		t.Fatalf("EffectiveNumCtx = %d, want the global 16384", got.EffectiveNumCtx)
	}
}

func TestModelConfigSettingOverridesGlobal(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "16384")

	rec, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":32768}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.NumCtx != 32768 || got.EffectiveNumCtx != 32768 {
		t.Fatalf("NumCtx = %d, EffectiveNumCtx = %d, want 32768 for both", got.NumCtx, got.EffectiveNumCtx)
	}
	if s.modelNumCtxSetting(modelID) != 32768 {
		t.Fatalf("modelNumCtxSetting = %d, want 32768", s.modelNumCtxSetting(modelID))
	}

	// A stored setting must survive a re-read.
	if _, reread := modelConfigRequest(t, s, http.MethodGet, modelID, ""); reread.NumCtx != 32768 {
		t.Fatalf("re-read NumCtx = %d, want 32768", reread.NumCtx)
	}
}

func TestModelConfigZeroClearsSetting(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_CTX", "16384")

	if rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":32768}`); rec.Code != http.StatusOK {
		t.Fatalf("seed status = %d", rec.Code)
	}
	rec, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.NumCtx != 0 {
		t.Fatalf("NumCtx = %d, want 0 after clearing", got.NumCtx)
	}
	if got.EffectiveNumCtx != 16384 {
		t.Fatalf("EffectiveNumCtx = %d, want the global 16384 after clearing", got.EffectiveNumCtx)
	}
}

func TestModelConfigRejectsTooSmallNumCtx(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")

	rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":512}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Every field is optional so that a client which shows only some of them
// cannot wipe the rest: the run dialog omits the llama.cpp options entirely for
// a model served by a Python runtime and still has to save its keep-alive.
func TestModelConfigLeavesOmittedFieldsAlone(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")

	if rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":32768,"num_parallel":2}`); rec.Code != http.StatusOK {
		t.Fatalf("seeding status = %d, want 200", rec.Code)
	}

	rec, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"keep_alive":"-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.NumCtx != 32768 {
		t.Fatalf("NumCtx = %d, want the saved 32768 to survive a keep-alive-only update", got.NumCtx)
	}
	if got.NumParallel != 2 {
		t.Fatalf("NumParallel = %d, want the saved 2 to survive a keep-alive-only update", got.NumParallel)
	}
	if got.KeepAlive != "-1" {
		t.Fatalf("KeepAlive = %q, want %q", got.KeepAlive, "-1")
	}

	// An empty body changes nothing rather than clearing the model's settings.
	rec, got = modelConfigRequest(t, s, http.MethodPut, modelID, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.NumCtx != 32768 || got.NumParallel != 2 || got.KeepAlive != "-1" {
		t.Fatalf("empty update changed the settings: %+v", got)
	}
}

// The keep-alive has to outlive the engine it was set on: before it was saved
// per model it lived only on one managedEngine, so an idle eviction or a
// restart silently returned the model to the five-minute default.
func TestModelConfigKeepAliveSurvivesAndResolves(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")

	rec, got := modelConfigRequest(t, s, http.MethodGet, modelID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.KeepAlive != "" {
		t.Fatalf("KeepAlive = %q, want empty when unset", got.KeepAlive)
	}
	if want := api.FormatKeepAlive(DefaultKeepAlive); got.EffectiveKeepAlive != want {
		t.Fatalf("EffectiveKeepAlive = %q, want the default %q", got.EffectiveKeepAlive, want)
	}

	rec, got = modelConfigRequest(t, s, http.MethodPut, modelID, `{"keep_alive":"-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.KeepAlive != "-1" || got.EffectiveKeepAlive != "-1" {
		t.Fatalf("KeepAlive = %q, EffectiveKeepAlive = %q, want -1 for both", got.KeepAlive, got.EffectiveKeepAlive)
	}
	// This is what a load started by a plain chat request resolves, and it is
	// the value the evictor reads: -1 means the model is never evicted.
	if resolved := s.effectiveModelKeepAlive(modelID); resolved != api.KeepAliveForever {
		t.Fatalf("effectiveModelKeepAlive = %s, want %s", resolved, api.KeepAliveForever)
	}

	rec, got = modelConfigRequest(t, s, http.MethodPut, modelID, `{"keep_alive":"90s"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.KeepAlive != "1m30s" {
		t.Fatalf("KeepAlive = %q, want the normalized %q", got.KeepAlive, "1m30s")
	}
	if resolved := s.effectiveModelKeepAlive(modelID); resolved != 90*time.Second {
		t.Fatalf("effectiveModelKeepAlive = %s, want 1m30s", resolved)
	}

	rec, got = modelConfigRequest(t, s, http.MethodPut, modelID, `{"keep_alive":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.KeepAlive != "" {
		t.Fatalf("KeepAlive = %q, want empty after clearing", got.KeepAlive)
	}
	if resolved := s.effectiveModelKeepAlive(modelID); resolved != DefaultKeepAlive {
		t.Fatalf("effectiveModelKeepAlive = %s, want the default %s after clearing", resolved, DefaultKeepAlive)
	}
}

func TestModelConfigRejectsUnusableKeepAlive(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")

	for _, body := range []string{`{"keep_alive":"soon"}`, `{"keep_alive":"-5m"}`} {
		rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d for %s, want 400", rec.Code, body)
		}
	}
	if s.modelSettings(modelID).KeepAlive != "" {
		t.Fatalf("KeepAlive = %q, want nothing saved after a rejected update", s.modelSettings(modelID).KeepAlive)
	}
}

// The Run dialog decides which load options to show from the runtime, because
// the pipeline tag cannot tell it: an embedding model runs on llama.cpp when
// its weights convert to GGUF and in the Python runtime when they do not, and
// the two accept completely different options.
func TestModelConfigReportsTheServingRuntime(t *testing.T) {
	cases := []struct {
		name        string
		pipelineTag string
		format      model.Format
		config      string
		files       []string
		want        string
	}{
		{
			name:        "gguf-embedding",
			pipelineTag: "feature-extraction",
			format:      model.FormatGGUF,
			want:        api.ModelRuntimeLlama,
		},
		{
			name:        "python-embedding",
			pipelineTag: "feature-extraction",
			format:      model.FormatSafeTensors,
			// An architecture the GGUF converter does not handle: those that it
			// does still prefer llama.cpp.
			config: `{"architectures":["JinaEmbeddingsV5OmniModel"]}`,
			files:  []string{"model.safetensors"},
			want:   api.ModelRuntimePythonEmbedding,
		},
		{
			name:        "text-to-speech",
			pipelineTag: "text-to-speech",
			format:      model.FormatSafeTensors,
			config:      `{"architectures":["Qwen3ForCausalLM"]}`,
			want:        api.ModelRuntimePythonTTS,
		},
		{
			name:        "text-generation",
			pipelineTag: "text-generation",
			format:      model.FormatGGUF,
			want:        api.ModelRuntimeLlama,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t)
			lm := &model.LocalModel{
				Namespace:   "Acme",
				Name:        tc.name,
				Format:      tc.format,
				PipelineTag: tc.pipelineTag,
				Files:       tc.files,
			}
			modelDir := model.ModelDir(s.cfg.ModelDir, lm.Namespace, lm.Name)
			if err := os.MkdirAll(modelDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(tc.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range tc.files {
				if err := os.WriteFile(filepath.Join(modelDir, name), []byte("weights"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := model.SaveManifestInDir(modelDir, lm); err != nil {
				t.Fatal(err)
			}

			_, resp := modelConfigRequest(t, s, http.MethodGet, lm.Namespace+"/"+lm.Name, "")
			if resp.Runtime != tc.want {
				t.Fatalf("runtime = %q, want %q", resp.Runtime, tc.want)
			}
		})
	}
}

func TestModelConfigNumParallelBeatsGlobalSetting(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_PARALLEL", "")
	s.cfg.Inference.LlamaNumParallel = 8

	rec, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0,"num_parallel":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	if got.NumParallel != 1 || got.EffectiveNumParallel != 1 {
		t.Fatalf("NumParallel = %d, EffectiveNumParallel = %d, want 1 for both", got.NumParallel, got.EffectiveNumParallel)
	}
	if got.GlobalNumParallel != 8 {
		t.Fatalf("GlobalNumParallel = %d, want 8", got.GlobalNumParallel)
	}
	if s.modelNumParallelSetting(modelID) != 1 {
		t.Fatalf("modelNumParallelSetting = %d, want 1", s.modelNumParallelSetting(modelID))
	}
}

// A model with its own slot count keeps it even though the chat page used to
// send the global one with every request, which is what silently reloaded the
// engine at eight times the KV cache.
func TestModelConfigNumParallelOmittedKeepsSetting(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")
	t.Setenv("CSGHUB_LITE_LLAMA_NUM_PARALLEL", "")

	if _, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0,"num_parallel":2}`); got.NumParallel != 2 {
		t.Fatalf("NumParallel = %d, want 2", got.NumParallel)
	}
	if _, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0}`); got.NumParallel != 2 {
		t.Fatalf("NumParallel = %d after an update without num_parallel, want the stored 2", got.NumParallel)
	}
	if _, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0,"num_parallel":0}`); got.NumParallel != 0 {
		t.Fatalf("NumParallel = %d after clearing, want 0", got.NumParallel)
	}
}

// The reported bug: a repository holding several quantizations served Q8_0
// again as soon as anything reloaded the engine, because a chat request names
// no dtype and the loader then falls back to the repository default.
// seedGGUFQuant drops a file whose name carries a quantization label into the
// model directory, so the config endpoint can see that the model can serve it.
func seedGGUFQuant(t *testing.T, s *Server, quant string) {
	t.Helper()
	modelDir := model.ModelDir(s.cfg.ModelDir, "Acme", "ctx-model")
	path := filepath.Join(modelDir, "ctx-model-"+quant+".gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestModelConfigDTypeSurvivesARequestWithoutOne(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")
	seedGGUFQuant(t, s, "Q5_K")

	if _, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0,"dtype":"Q5_K"}`); got.DType != "q5_k" {
		t.Fatalf("DType = %q, want %q", got.DType, "q5_k")
	}
	if s.modelDTypeSetting(modelID) != "q5_k" {
		t.Fatalf("modelDTypeSetting = %q, want %q", s.modelDTypeSetting(modelID), "q5_k")
	}

	// An update that carries no dtype must not drop it.
	if _, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0}`); got.DType != "q5_k" {
		t.Fatalf("DType = %q after an update without dtype, want it kept", got.DType)
	}
	// An empty dtype clears it and returns the model to the repository default.
	if _, got := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0,"dtype":""}`); got.DType != "" {
		t.Fatalf("DType = %q after clearing, want empty", got.DType)
	}
}

// A dtype the model cannot serve must not be saved: it would otherwise be
// applied to every later load that names none.
func TestModelConfigRejectsDTypeWithNoBuild(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")
	seedGGUFQuant(t, s, "Q8_0")

	rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0,"dtype":"Q5_K"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %q", rec.Code, rec.Body.String())
	}
	if s.modelDTypeSetting(modelID) != "" {
		t.Fatalf("modelDTypeSetting = %q, want it not saved", s.modelDTypeSetting(modelID))
	}
}

func TestModelConfigRejectsUnknownDType(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")

	rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, `{"num_ctx":0,"dtype":"not-a-quant"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %q", rec.Code, rec.Body.String())
	}
}

// Saving a keep-alive while the model is loaded has to reach the running
// engine, and -1 has to make the evictor skip it: an idle eviction is exactly
// what the setting exists to prevent.
func TestModelConfigKeepAliveReachesALoadedEngine(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")
	cacheKey := engineCacheKey(modelID, engineModeChat)

	s.engines[cacheKey] = &managedEngine{
		engine:    &poolRuntimeTestEngine{},
		lastUsed:  time.Now().Add(-time.Hour),
		keepAlive: DefaultKeepAlive,
	}

	if rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, `{"keep_alive":"-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := s.engines[cacheKey].keepAlive; got != api.KeepAliveForever {
		t.Fatalf("loaded engine keepAlive = %s, want %s without a reload", got, api.KeepAliveForever)
	}
	s.evictExpired(time.Now())
	if _, ok := s.engines[cacheKey]; !ok {
		t.Fatal("engine evicted although its keep-alive is -1")
	}

	// Clearing the setting hands the model back to the default, which an hour
	// of idling is past.
	if rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, `{"keep_alive":""}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := s.engines[cacheKey].keepAlive; got != DefaultKeepAlive {
		t.Fatalf("loaded engine keepAlive = %s, want the default %s after clearing", got, DefaultKeepAlive)
	}
	s.evictExpired(time.Now())
	if _, ok := s.engines[cacheKey]; ok {
		t.Fatal("engine not evicted although its keep-alive went back to the default")
	}
}
