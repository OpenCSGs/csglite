package dataset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestManager_GetWithFileEntries_BackfillsAndPersists(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DatasetDir: dir}
	mgr := NewManager(cfg)

	datasetDir := DatasetDir(dir, "test", "demo")
	if err := os.MkdirAll(filepath.Join(datasetDir, "train"), 0o755); err != nil {
		t.Fatalf("mkdir train: %v", err)
	}
	if err := os.WriteFile(filepath.Join(datasetDir, "train", "data.jsonl"), []byte("demo"), 0o644); err != nil {
		t.Fatalf("write data file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(datasetDir, "README.md"), []byte("readme"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	ld := &LocalDataset{
		Namespace: "test",
		Name:      "demo",
		Files:     []string{"data.jsonl", "README.md"},
	}
	if err := SaveManifest(dir, ld); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	got, err := mgr.GetWithFileEntries("test/demo")
	if err != nil {
		t.Fatalf("GetWithFileEntries error: %v", err)
	}
	if len(got.FileEntries) != 2 {
		t.Fatalf("file_entries len = %d, want 2", len(got.FileEntries))
	}

	reloaded, err := LoadManifest(dir, "test", "demo")
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if len(reloaded.FileEntries) != 2 {
		t.Fatalf("persisted file_entries len = %d, want 2", len(reloaded.FileEntries))
	}
}
