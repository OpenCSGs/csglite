package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestModelConfigRequiresNumCtxField(t *testing.T) {
	s := newTestServer(t)
	modelID := seedModelForConfig(t, s, "40960")

	rec, _ := modelConfigRequest(t, s, http.MethodPut, modelID, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
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
			config:      `{"architectures":["JinaEmbeddingsV5OmniModel"]}`,
			files:       []string{"model.safetensors"},
			want:        api.ModelRuntimePythonEmbedding,
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
