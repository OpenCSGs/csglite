package server

import (
	"context"
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
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestRecommendXiaozhiModelSlotsPrefersAvailableSavedThenSourceOrder(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "local-chat", Source: "local", PipelineTag: "text-generation"},
		{Model: "cloud-chat", Source: "cloud", PipelineTag: "conversational"},
		{Model: "local-asr", Source: "local", PipelineTag: "automatic-speech-recognition"},
		{Model: "cloud-embedding", Source: "cloud", PipelineTag: "feature-extraction"},
		{Model: "provider-image", Source: "provider:images", PipelineTag: "text-to-image"},
	}
	saved := []api.AIAppModelBinding{
		{Task: "language_model", ModelID: "cloud-chat", Source: "cloud"},
		{Task: "speech_recognition", ModelID: "missing-asr", Source: "cloud"},
	}

	slots := recommendXiaozhiModelSlots(models, saved)
	if len(slots) != 4 {
		t.Fatalf("slots len = %d, want 4", len(slots))
	}
	if !slots[0].Required || slots[0].Binding == nil || slots[0].Binding.ModelID != "cloud-chat" {
		t.Fatalf("language slot = %#v, want required saved cloud binding", slots[0])
	}
	if slots[1].Required || slots[1].Binding == nil || slots[1].Binding.ModelID != "local-asr" {
		t.Fatalf("speech slot = %#v, want optional local recommendation", slots[1])
	}
	if slots[2].Binding == nil || slots[2].Binding.ModelID != "cloud-embedding" {
		t.Fatalf("embedding slot = %#v", slots[2])
	}
	if slots[3].Binding == nil || slots[3].Binding.ModelID != "provider-image" {
		t.Fatalf("image slot = %#v", slots[3])
	}
}

func TestXiaozhiRuntimeAndContainerBaseURL(t *testing.T) {
	if !aiAppSupportsRuntimeLifecycle(xiaozhiAppID) {
		t.Fatal("Xiaozhi runtime lifecycle is not registered")
	}
	got, err := xiaozhiLiteBaseURL(":12435")
	if err != nil {
		t.Fatalf("xiaozhiLiteBaseURL: %v", err)
	}
	if got != "http://host.docker.internal:12435/v1" {
		t.Fatalf("base URL = %q", got)
	}
	if _, err := xiaozhiLiteBaseURL("127.0.0.1:11435"); err == nil {
		t.Fatal("loopback-only listen address should be rejected")
	}
}

func TestValidateXiaozhiModelBindingsRejectsAmbiguousSourceAndWrongTask(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "shared", Source: "local", PipelineTag: "text-generation"},
		{Model: "shared", Source: "cloud", PipelineTag: "text-generation"},
		{Model: "asr", Source: "local", PipelineTag: "automatic-speech-recognition"},
	}
	_, err := validateXiaozhiModelBindings([]api.AIAppModelBinding{
		{Task: "language_model", ModelID: "shared"},
	}, models)
	if err == nil || !strings.Contains(err.Error(), "multiple sources") {
		t.Fatalf("ambiguous validation error = %v", err)
	}
	_, err = validateXiaozhiModelBindings([]api.AIAppModelBinding{
		{Task: "language_model", ModelID: "shared", Source: "cloud"},
	}, models)
	if err == nil || !strings.Contains(err.Error(), "cannot preserve source") {
		t.Fatalf("explicit ambiguous source validation error = %v", err)
	}

	_, err = validateXiaozhiModelBindings([]api.AIAppModelBinding{
		{Task: "language_model", ModelID: "asr", Source: "local"},
	}, models)
	if err == nil || !strings.Contains(err.Error(), `task "language_model"`) {
		t.Fatalf("wrong-category validation error = %v", err)
	}
}

func TestSyncXiaozhiConfigMergesScenariosAndPreservesUnknownFields(t *testing.T) {
	storage := t.TempDir()
	cfg := &config.Config{
		ModelDir:   filepath.Join(storage, config.ModelsDir),
		DatasetDir: filepath.Join(storage, config.DatasetsDir),
		Token:      "secret-token",
		AIAppModelBindings: map[string][]api.AIAppModelBinding{
			xiaozhiAppID: {
				{Task: "language_model", ModelID: "chat-model", Source: "local"},
				{Task: "speech_recognition", ModelID: "asr-model", Source: "local"},
			},
		},
	}
	path := filepath.Join(storage, "apps", xiaozhiAppID, "config", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `{
	  "unknown_root": {"keep": true},
	  "copilot": {
	    "unknown_copilot": 7,
	    "providers.openai": {"unknown_provider": "keep"},
	    "scenarios": {
	      "unknown_scenario_config": "keep",
	      "scenarios": {
	        "rerank": "keep-rerank",
	        "image": "remove-stale-image",
	        "unknown_scenario": "keep"
	      }
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{cfg: cfg}
	if err := s.syncXiaozhiConfig(); err != nil {
		t.Fatalf("syncXiaozhiConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	copilot := root["copilot"].(map[string]interface{})
	provider := copilot["providers.openai"].(map[string]interface{})
	if provider["baseURL"] != "http://host.docker.internal:11435/v1" || provider["apiKey"] != "secret-token" || provider["unknown_provider"] != "keep" {
		t.Fatalf("provider = %#v", provider)
	}
	scenarioConfig := copilot["scenarios"].(map[string]interface{})
	scenarios := scenarioConfig["scenarios"].(map[string]interface{})
	for _, key := range xiaozhiScenarioKeys["language_model"] {
		if scenarios[key] != "chat-model" {
			t.Fatalf("scenario %q = %#v, want chat-model", key, scenarios[key])
		}
	}
	if scenarios["audio_transcribing"] != "asr-model" {
		t.Fatalf("audio_transcribing = %#v", scenarios["audio_transcribing"])
	}
	if _, ok := scenarios["image"]; ok {
		t.Fatalf("stale managed image scenario was retained: %#v", scenarios)
	}
	if scenarios["rerank"] != "keep-rerank" || scenarios["unknown_scenario"] != "keep" ||
		copilot["unknown_copilot"] != float64(7) || root["unknown_root"] == nil {
		t.Fatalf("unknown fields or rerank were not preserved: %#v", root)
	}
	if runtime.GOOS != "windows" {
		if mode := fileModePerm(t, path); mode != 0o600 {
			t.Fatalf("config permissions = %#o, want 0600", mode)
		}
	}
}

func TestHandleAppModelSaveAcceptsXiaozhiBindingsAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.Reset()
	t.Cleanup(config.Reset)

	storage := t.TempDir()
	cfg := &config.Config{
		ModelDir:           filepath.Join(storage, config.ModelsDir),
		DatasetDir:         filepath.Join(storage, config.DatasetsDir),
		ListenAddr:         config.DefaultListenAddr,
		AIAppModelBindings: map[string][]api.AIAppModelBinding{},
	}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "chat",
		Format:       model.FormatGGUF,
		PipelineTag:  "text-generation",
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}
	s := newTestServerWithConfig(t, cfg)
	s.cloud = nil

	body := `{"app_id":"xiaozhi","model_bindings":[{"task":"language_model","model_id":"chat","source":"local"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/apps/model", strings.NewReader(body))
	s.handleAppModelSave(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if got := cfg.AIAppModelBindings[xiaozhiAppID]; len(got) != 1 || got[0].ModelID != "chat" {
		t.Fatalf("saved bindings = %#v", got)
	}
	configData, err := os.ReadFile(filepath.Join(home, config.AppDir, config.ConfigFile))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(configData), `"ai_app_model_bindings"`) {
		t.Fatalf("persisted config lacks bindings: %s", configData)
	}
}

func TestEnrichXiaozhiReturnsFourModelSlots(t *testing.T) {
	storage := t.TempDir()
	cfg := &config.Config{
		ModelDir:           filepath.Join(storage, config.ModelsDir),
		DatasetDir:         filepath.Join(storage, config.DatasetsDir),
		AIAppModelBindings: map[string][]api.AIAppModelBinding{},
	}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "chat",
		Format:       model.FormatGGUF,
		PipelineTag:  "text-generation",
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatal(err)
	}
	s := newTestServerWithConfig(t, cfg)
	s.cloud = nil
	info := api.AIAppInfo{ID: xiaozhiAppID}
	s.enrichXiaozhiModelSlots(context.Background(), &info)
	if len(info.ModelSlots) != 4 {
		t.Fatalf("model_slots len = %d, want 4", len(info.ModelSlots))
	}
	if info.ModelSlots[0].Task != "language_model" || !info.ModelSlots[0].Required || info.ModelSlots[0].Binding == nil {
		t.Fatalf("language slot = %#v", info.ModelSlots[0])
	}
	for _, slot := range info.ModelSlots[1:] {
		if slot.Required {
			t.Fatalf("optional slot marked required: %#v", slot)
		}
	}
}

func TestDesktopXiaozhiUsesDockerBridge(t *testing.T) {
	cfg := &config.Config{
		DesktopMode:        true,
		DesktopAPIAddr:     config.DefaultDesktopAPIAddr,
		DesktopAPIBindAddr: config.DefaultDesktopAPIBindAddr,
	}
	got, err := xiaozhiLiteBaseURL(cfg.RuntimeDockerAPIAddr())
	if err != nil {
		t.Fatalf("xiaozhiLiteBaseURL: %v", err)
	}
	if got != "http://host.docker.internal:11436/v1" {
		t.Fatalf("desktop Xiaozhi base URL = %q", got)
	}
}

func fileModePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
