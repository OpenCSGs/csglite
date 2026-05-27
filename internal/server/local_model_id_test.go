package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/internal/model"
)

func TestResolveLocalModelStorageIDAcceptsShortName(t *testing.T) {
	s := newTestServer(t)
	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3-0.6B-GGUF",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(100, 0),
	})

	got := s.resolveLocalModelStorageID("Qwen3-0.6B-GGUF")
	if got != "Qwen/Qwen3-0.6B-GGUF" {
		t.Fatalf("resolveLocalModelStorageID() = %q, want Qwen/Qwen3-0.6B-GGUF", got)
	}
}

func TestGetChatEngineAcceptsShortLocalModelName(t *testing.T) {
	s := newTestServer(t)
	engine := &fakeChatCompletionEngine{}
	s.mu.Lock()
	s.engines["Qwen/Qwen3-0.6B-GGUF"] = &managedEngine{engine: engine}
	s.mu.Unlock()

	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3-0.6B-GGUF",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(100, 0),
	})

	got, err := s.getChatEngine(context.Background(), "Qwen3-0.6B-GGUF", "local", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatalf("getChatEngine() error = %v", err)
	}
	if got != engine {
		t.Fatalf("getChatEngine() = %#v, want preloaded engine %#v", got, engine)
	}
}

func TestGetOrLoadEngineAcceptsShortLocalModelName(t *testing.T) {
	s := newTestServer(t)
	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3-0.6B-GGUF",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(100, 0),
	})
	modelDir := filepath.Join(s.cfg.ModelDir, "Qwen", "Qwen3-0.6B-GGUF")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	t.Cleanup(func() { loadEngineWithProgress = origLoader })

	var loadedFullName string
	loadEngineWithProgress = func(_ string, lm *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, _ int, _ int, _ int, _ string, _ string, _ string) (inference.Engine, error) {
		loadedFullName = lm.FullName()
		return &fakeEngine{}, nil
	}

	if _, err := s.getOrLoadEngineWithOpts("Qwen3-0.6B-GGUF", 0, 0, -1, "", "", ""); err != nil {
		t.Fatalf("getOrLoadEngineWithOpts() error = %v", err)
	}
	if loadedFullName != "Qwen/Qwen3-0.6B-GGUF" {
		t.Fatalf("loaded model = %q, want Qwen/Qwen3-0.6B-GGUF", loadedFullName)
	}
}
