package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
)

// The realtime and speech endpoints are not implemented yet. They must still be
// routed explicitly: an unrouted GET matches the "GET /" static fallback and
// returns the embedded web UI with status 200, which makes a client probing for
// realtime support believe the feature exists.
func TestRealtimeEndpointsReturnNotImplementedInsteadOfWebUI(t *testing.T) {
	s := newTestServerWithConfig(t, &config.Config{
		ModelDir:   config.ModelDirForStorage(t.TempDir()),
		DatasetDir: config.DatasetDirForStorage(t.TempDir()),
	})
	handler := s.routes()

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/audio/speech"},
		{http.MethodPost, "/v1/realtime/calls"},
		{http.MethodGet, "/v1/realtime"},
		{http.MethodGet, "/v1/realtime/transcription"},
		{http.MethodGet, "/v1/audio/transcriptions/realtime"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, http.StatusNotImplemented, truncateLogString(w.Body.String(), 200))
			}
			if contentType := w.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
				t.Fatalf("content type = %q, want application/json", contentType)
			}
			var body struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body %q: %v", w.Body.String(), err)
			}
			if body.Error.Type != "unsupported_error" {
				t.Fatalf("error type = %q, want unsupported_error", body.Error.Type)
			}
			if !strings.Contains(body.Error.Message, "issues/147") {
				t.Fatalf("error message = %q, want a pointer to the tracking issue", body.Error.Message)
			}
		})
	}
}

func TestIsTTSPipelineTag(t *testing.T) {
	for _, tag := range []string{"text-to-speech", "Text-To-Speech", "  text-to-speech  "} {
		if !isTTSPipelineTag(tag) {
			t.Errorf("isTTSPipelineTag(%q) = false, want true", tag)
		}
	}
	for _, tag := range []string{"", "text-generation", "automatic-speech-recognition", "text-to-image"} {
		if isTTSPipelineTag(tag) {
			t.Errorf("isTTSPipelineTag(%q) = true, want false", tag)
		}
	}
}

// A text-to-speech model must never reach the text-generation runtime: its
// language-model half looks like a plain causal LM, so the llama.cpp path would
// convert it to GGUF, drop the vocoder and emit text instead of audio. Every
// chat, generate and load path funnels through getOrLoadEngineFullMode, so the
// refusal is asserted there.
func TestTextGenerationRuntimeRefusesTextToSpeechModel(t *testing.T) {
	s := newTestServerWithConfig(t, &config.Config{
		ModelDir:   config.ModelDirForStorage(t.TempDir()),
		DatasetDir: config.DatasetDirForStorage(t.TempDir()),
	})

	lm := &model.LocalModel{
		Namespace:   "FunAudioLLM",
		Name:        "CosyVoice2-0.5B",
		Format:      model.FormatSafeTensors,
		PipelineTag: "text-to-speech",
	}
	modelDir := model.ModelDir(s.cfg.ModelDir, lm.Namespace, lm.Name)
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"architectures":["Qwen3ForCausalLM"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := model.SaveManifestInDir(modelDir, lm); err != nil {
		t.Fatal(err)
	}
	modelID := lm.Namespace + "/" + lm.Name

	if !s.modelUsesTTSEngine(modelID) {
		t.Fatalf("modelUsesTTSEngine(%q) = false, want true", modelID)
	}

	_, err := s.getOrLoadEngineFullMode(modelID, nil, 0, 0, -1, "", "", "", engineModeChat, inference.SpeculativeConfig{}, false)
	if err == nil {
		t.Fatal("getOrLoadEngineFullMode succeeded, want a text-to-speech refusal")
	}
	if !strings.Contains(err.Error(), "text-to-speech") {
		t.Fatalf("error = %v, want it to name text-to-speech", err)
	}
}
