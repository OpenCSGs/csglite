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

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
)

type fakeDecisionEngine struct {
	fakeEngine
	adapter bool
	prompts []string
}

func (e *fakeDecisionEngine) HasDecisionAdapter() bool { return e.adapter }

func (e *fakeDecisionEngine) ScoreDecisionTokens(_ context.Context, prompt string, ids []int) (map[int]float64, int, error) {
	e.prompts = append(e.prompts, prompt)
	out := map[int]float64{}
	for i, id := range ids {
		out[id] = -float64(i + 1) // the first option is the most likely
	}
	return out, 12, nil
}

// newDecisionTestServer stores a typed-decision GGUF (org/JEV-9B-GGUF) whose
// System 1 block lives in its locally downloaded base model (org/JEV-9B).
func newDecisionTestServer(t *testing.T, withBase bool) *Server {
	t.Helper()
	useIsolatedStorageHome(t)
	cfg := &config.Config{ModelDir: t.TempDir()}
	s := newTestServerWithConfig(t, cfg)

	save := func(name string, files map[string]string) {
		if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
			Namespace: "org", Name: name, Format: model.FormatGGUF, Size: 1,
			Files: []string{"model.gguf"}, DownloadedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		for rel, content := range files {
			path := filepath.Join(cfg.ModelDir, "org", name, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	save("JEV-9B-GGUF", map[string]string{
		"model.gguf": "gguf",
		"README.md":  "---\nbase_model: org/JEV-9B\ntags:\n- jev\n- typed-decisions\n---\n",
	})
	save("plain", map[string]string{"model.gguf": "gguf", "README.md": "---\ntags:\n- text-generation\n---\n"})
	if withBase {
		ids, bias, verbalizers := []int{}, []float64{}, []string{"false", "true", "0", "1", "2", "3", "4", "5"}
		for i := 0; i < 24; i++ {
			ids = append(ids, 500+i)
			bias = append(bias, 0)
		}
		for i := 0; i < 16; i++ {
			verbalizers = append(verbalizers, string(rune('A'+i)))
		}
		head, _ := json.Marshal(map[string]any{
			"bias": bias, "verbalizer_ids": ids,
			"slots": map[string]any{
				"ranges":      map[string][2]int{"noul": {0, 2}, "score": {2, 8}, "choice": {8, 24}},
				"verbalizers": verbalizers, "template_version": "bare-v1",
			},
		})
		save("JEV-9B", map[string]string{
			"model.gguf":                      "gguf",
			"adapter_vllm/decision_head.json": string(head),
			"calibration.json":                `{"per_kind":{"noul":1,"choice":1,"score":1}}`,
		})
	}
	return s
}

func postDecide(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/decide", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleDecide(w, req)
	return w
}

func TestHandleDecideReturnsJEVStyleDecision(t *testing.T) {
	s := newDecisionTestServer(t, true)
	engine := &fakeDecisionEngine{adapter: true}
	s.engines["org/JEV-9B-GGUF"] = &managedEngine{engine: engine, numCtx: 4096, numParallel: 1}

	w := postDecide(t, s, `{"model":"org/JEV-9B-GGUF","kind":"choice","state":"low stock","question":"Next step?","options":["warn","wait","order"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kind", "effective_kind", "options", "probabilities", "choice_index", "choice", "adaptation", "protocol", "model", "usage", "elapsed_seconds", "num_model_requests"} {
		if _, ok := resp[key]; !ok {
			t.Fatalf("response missing %q: %s", key, w.Body.String())
		}
	}
	if resp["choice"] != "warn" || resp["model"] != "org/JEV-9B-GGUF" || resp["protocol"] != "jev-bare-v1" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	if len(engine.prompts) != 1 || !strings.HasPrefix(engine.prompts[0], "[kind] choice\n[state] low stock\n") {
		t.Fatalf("prompts = %q", engine.prompts)
	}
}

func TestHandleDecideReloadsEngineStartedWithoutAdapter(t *testing.T) {
	s := newDecisionTestServer(t, true)
	s.engines["org/JEV-9B-GGUF"] = &managedEngine{engine: &fakeDecisionEngine{adapter: false}, numCtx: 4096, numParallel: 1}

	origLoader := loadEngineWithProgress
	t.Cleanup(func() { loadEngineWithProgress = origLoader })
	loads := 0
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		loads++
		return &fakeDecisionEngine{adapter: true}, nil
	}

	w := postDecide(t, s, `{"model":"org/JEV-9B-GGUF","kind":"noul","question":"Refund?"}`)
	if w.Code != http.StatusOK || loads != 1 {
		t.Fatalf("status %d loads %d body %s", w.Code, loads, w.Body.String())
	}
}

func TestHandleDecideRejectsModelsWithoutSystem1(t *testing.T) {
	s := newDecisionTestServer(t, false)
	tests := map[string]string{
		`{"model":"org/plain","kind":"noul","question":"q"}`:       "not a typed-decision model",
		`{"model":"org/JEV-9B-GGUF","kind":"noul","question":"q"}`: "org/JEV-9B",
		`{"kind":"noul","question":"q"}`:                           "model is required",
	}
	for body, want := range tests {
		w := postDecide(t, s, body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%s: status %d body %s, want 400 containing %q", body, w.Code, w.Body.String(), want)
		}
	}
}

func TestHandleDecideRejectsInvalidQuestionsBeforeInference(t *testing.T) {
	s := newDecisionTestServer(t, true)
	origLoader := loadEngineWithProgress
	t.Cleanup(func() { loadEngineWithProgress = origLoader })
	loads := 0
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		loads++
		return &fakeDecisionEngine{adapter: true}, nil
	}

	w := postDecide(t, s, `{"model":"org/JEV-9B-GGUF","kind":"noul","question":"q","thinking":"on"}`)
	if w.Code != http.StatusBadRequest || loads != 0 {
		t.Fatalf("status %d model loads %d body %s", w.Code, loads, w.Body.String())
	}
}

func TestHandleDecideInfo(t *testing.T) {
	s := newDecisionTestServer(t, true)
	req := httptest.NewRequest(http.MethodGet, "/v1/decide/info?model=org/JEV-9B-GGUF", nil)
	w := httptest.NewRecorder()
	s.handleDecideInfo(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"native_choice_options":16`) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestLocalModelInfoFlagsTypedDecisionModels(t *testing.T) {
	s := newDecisionTestServer(t, true)
	infos, err := s.listLocalModelInfos()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, info := range infos {
		got[info.ID] = info.TypedDecisions
	}
	if !got["org/JEV-9B-GGUF"] || got["org/plain"] {
		t.Fatalf("typed_decisions flags = %v", got)
	}
}

func TestLocalModelInfoSkipsTaggedModelsWithoutSystem1(t *testing.T) {
	// Tagged, but neither the model nor a downloaded base_model ships the
	// System 1 block (e.g. another runtime's typed-decisions checkpoint).
	s := newDecisionTestServer(t, false)
	infos, err := s.listLocalModelInfos()
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		if info.TypedDecisions {
			t.Fatalf("%s flagged for typed decisions without a System 1 block", info.ID)
		}
	}
}

func TestHandleDecisionsServesOpenAIDecisionsAPI(t *testing.T) {
	s := newDecisionTestServer(t, true)
	engine := &fakeDecisionEngine{adapter: true}
	s.engines["org/JEV-9B-GGUF"] = &managedEngine{engine: engine, numCtx: 4096, numParallel: 1}

	body := `{"model":"org/JEV-9B-GGUF","input":"I was charged twice for my order.","questions":[
	  {"type":"predicate","name":"refund","instructions":"Is this a refund request?"},
	  {"type":"choice","name":"department","instructions":"Which department?","choices":[{"value":"billing"},{"value":"technical"}]}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleDecisions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Answers []map[string]any `json:"answers"`
		Model   string           `json:"model"`
		Usage   map[string]any   `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Answers) != 2 || resp.Answers[0]["name"] != "refund" || resp.Answers[1]["choice"] != "billing" {
		t.Fatalf("answers = %v", resp.Answers)
	}
	if resp.Model != "org/JEV-9B-GGUF" || resp.Usage["input_tokens"] != float64(24) || len(engine.prompts) != 2 {
		t.Fatalf("model %q usage %v prompts %d", resp.Model, resp.Usage, len(engine.prompts))
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(`{"model":"org/plain","input":"x","questions":[{"type":"predicate","instructions":"q"}]}`))
	w = httptest.NewRecorder()
	s.handleDecisions(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("untagged model: status %d body %s", w.Code, w.Body.String())
	}
}

type fakeSystemOneEngine struct {
	fakeEngine
	bodies []string
}

func (e *fakeSystemOneEngine) NativeDecisionType() string { return "laya" }

func (e *fakeSystemOneEngine) SystemOne(_ context.Context, body []byte) ([]byte, error) {
	e.bodies = append(e.bodies, string(body))
	return []byte(`{"answers":{"q000":{"type":"noul","noul":0.9},"q001":{"type":"choice","probabilities":{"billing":0.8,"other":0.2}}},"usage":{"input_tokens":40}}`), nil
}

// newNativeDecisionTestServer stores a Laya checkpoint (org/laya): a
// ModernBERT encoder with a decision head, recognized by its layout.
func newNativeDecisionTestServer(t *testing.T) *Server {
	t.Helper()
	useIsolatedStorageHome(t)
	cfg := &config.Config{ModelDir: t.TempDir()}
	s := newTestServerWithConfig(t, cfg)
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace: "org", Name: "laya", Format: model.FormatSafeTensors, Size: 1,
		Files: []string{"model.safetensors"}, DownloadedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"model.safetensors":    "weights",
		"config.json":          `{"model_type":"laya","architectures":["LayaTypedDecisions"]}`,
		"encoder/config.json":  `{"architectures":["ModernBertModel"]}`,
		"rl_agent_config.json": `{"head_layers":2}`,
		"README.md":            "---\ntags:\n- typed-decisions\n---\n",
	} {
		path := filepath.Join(cfg.ModelDir, "org", "laya", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestHandleDecisionsServesNativeDecisionModels(t *testing.T) {
	s := newNativeDecisionTestServer(t)
	engine := &fakeSystemOneEngine{}
	s.engines["org/laya"] = &managedEngine{engine: engine, numCtx: 8192, numParallel: 1}

	body := `{"model":"org/laya","input":"I was charged twice.","questions":[
	  {"type":"predicate","name":"refund","instructions":"Is this a refund request?"},
	  {"type":"choice","name":"department","instructions":"Which department?","choices":[{"value":"billing"},{"value":"other"}]}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleDecisions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Answers []map[string]any `json:"answers"`
		Usage   map[string]any   `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Answers) != 2 || resp.Answers[0]["probability"] != 0.9 || resp.Answers[1]["choice"] != "billing" || resp.Usage["input_tokens"] != float64(40) {
		t.Fatalf("response = %s", w.Body.String())
	}
	if len(engine.bodies) != 1 || !strings.Contains(engine.bodies[0], `"state":"I was charged twice."`) {
		t.Fatalf("System One bodies = %q", engine.bodies)
	}

	// The JEV-style endpoint points native models at /v1/decisions.
	w = postDecide(t, s, `{"model":"org/laya","kind":"noul","question":"q"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "/v1/decisions") {
		t.Fatalf("/v1/decide: status %d body %s", w.Code, w.Body.String())
	}
}

func TestLocalModelInfoFlagsNativeDecisionModels(t *testing.T) {
	s := newNativeDecisionTestServer(t)
	infos, err := s.listLocalModelInfos()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || !infos[0].TypedDecisions || !infos[0].DecisionOnly {
		t.Fatalf("infos = %+v", infos)
	}
	// The typed-decisions tag must not send the engine looking for a JEV adapter.
	if path, err := s.resolveDecisionLoRA("org/laya"); path != "" || err != nil {
		t.Fatalf("resolveDecisionLoRA = %q, %v", path, err)
	}
}
