package model

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
)

func TestManager_List_Empty(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	models, err := mgr.List()
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("len = %d, want 0", len(models))
	}
}

func TestManager_List_WithModels(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	// Create two models
	for _, m := range []*LocalModel{
		{Namespace: "ns1", Name: "model1", Format: FormatGGUF, Size: 100, DownloadedAt: time.Now()},
		{Namespace: "ns2", Name: "model2", Format: FormatSafeTensors, Size: 200, DownloadedAt: time.Now()},
	} {
		if err := SaveManifest(dir, m); err != nil {
			t.Fatal(err)
		}
	}

	models, err := mgr.List()
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(models) != 2 {
		t.Errorf("len = %d, want 2", len(models))
	}
}

func TestManager_Get(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	lm := &LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    FormatGGUF,
		Size:      1024,
	}
	if err := SaveManifest(dir, lm); err != nil {
		t.Fatal(err)
	}

	got, err := mgr.Get("test/model")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got.FullName() != "test/model" {
		t.Errorf("FullName = %q, want %q", got.FullName(), "test/model")
	}
}

func TestManager_Get_InvalidID(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	_, err := mgr.Get("invalid")
	if err == nil {
		t.Error("expected error for invalid model ID")
	}
}

func TestManager_GetWithFileEntries_BackfillsAndPersists(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	modelDir := ModelDir(dir, "test", "model")
	if err := os.MkdirAll(filepath.Join(modelDir, "weights"), 0o755); err != nil {
		t.Fatalf("mkdir weights: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "weights", "model.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatalf("write model file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lm := &LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    FormatGGUF,
		Files:     []string{"model.gguf", "config.json"},
	}
	if err := SaveManifest(dir, lm); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	got, err := mgr.GetWithFileEntries("test/model")
	if err != nil {
		t.Fatalf("GetWithFileEntries error: %v", err)
	}
	if len(got.FileEntries) != 2 {
		t.Fatalf("file_entries len = %d, want 2", len(got.FileEntries))
	}

	reloaded, err := LoadManifest(dir, "test", "model")
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if len(reloaded.FileEntries) != 2 {
		t.Fatalf("persisted file_entries len = %d, want 2", len(reloaded.FileEntries))
	}
}

func TestManager_Remove(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	lm := &LocalModel{
		Namespace: "test",
		Name:      "removeme",
		Format:    FormatGGUF,
	}
	if err := SaveManifest(dir, lm); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Remove("test/removeme"); err != nil {
		t.Fatalf("Remove error: %v", err)
	}

	// Should not exist anymore
	_, err := mgr.Get("test/removeme")
	if err == nil {
		t.Error("model should be removed")
	}
}

func TestManager_Remove_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	err := mgr.Remove("nonexistent/model")
	if err == nil {
		t.Error("expected error for non-existent model")
	}
}

func TestManager_Exists(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	lm := &LocalModel{Namespace: "test", Name: "exists"}
	SaveManifest(dir, lm)

	if !mgr.Exists("test/exists") {
		t.Error("Exists() = false, want true")
	}
	if mgr.Exists("test/nope") {
		t.Error("Exists() = true, want false for non-existent model")
	}
	if mgr.Exists("invalid") {
		t.Error("Exists() = true, want false for invalid ID")
	}
}

func TestManager_ModelPath(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	lm := &LocalModel{Namespace: "test", Name: "pathtest"}
	SaveManifest(dir, lm)

	path, err := mgr.ModelPath("test/pathtest")
	if err != nil {
		t.Fatalf("ModelPath error: %v", err)
	}
	if path == "" {
		t.Error("ModelPath returned empty string")
	}
}

func TestManager_ModelPath_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ModelDir: dir}
	mgr := NewManager(cfg)

	_, err := mgr.ModelPath("nonexistent/model")
	if err == nil {
		t.Error("expected error for non-existent model")
	}
}
