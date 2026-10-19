package model

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEnsureLocalModelFiles_BackfillsMissingEntries(t *testing.T) {
	baseDir := t.TempDir()
	modelDir := ModelDir(baseDir, "Acme", "demo")
	if err := os.MkdirAll(filepath.Join(modelDir, "weights"), 0o755); err != nil {
		t.Fatalf("mkdir weights: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "weights", "model.gguf"), []byte("gguf-data"), 0o644); err != nil {
		t.Fatalf("write model.gguf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"arch":"demo"}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	lm := &LocalModel{
		Namespace: "Acme",
		Name:      "demo",
		Format:    FormatGGUF,
		Files:     []string{"model.gguf", "config.json"},
	}
	if err := SaveManifest(baseDir, lm); err != nil {
		t.Fatalf("save legacy manifest: %v", err)
	}

	loaded, err := LoadManifest(baseDir, "Acme", "demo")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	changed, err := EnsureLocalModelFiles(modelDir, loaded)
	if err != nil {
		t.Fatalf("EnsureLocalModelFiles: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for legacy manifest backfill")
	}
	if len(loaded.FileEntries) != 2 {
		t.Fatalf("file_entries len = %d, want 2", len(loaded.FileEntries))
	}
	if got := loaded.Files; len(got) != 2 || got[0] != "config.json" || got[1] != "weights/model.gguf" {
		t.Fatalf("files = %#v, want sorted relative paths", got)
	}
	for _, entry := range loaded.FileEntries {
		if entry.Path == "" {
			t.Fatal("entry path is empty")
		}
		if entry.Size <= 0 {
			t.Fatalf("entry %q size = %d, want > 0", entry.Path, entry.Size)
		}
		if entry.SHA256 == "" {
			t.Fatalf("entry %q sha256 is empty", entry.Path)
		}
	}

	changed, err = EnsureLocalModelFiles(modelDir, loaded)
	if err != nil {
		t.Fatalf("EnsureLocalModelFiles second pass: %v", err)
	}
	if changed {
		t.Fatal("changed = true on second pass, want false")
	}
}

func TestRefreshManifestFiles_ReplacesConvertedSourceWithGGUF(t *testing.T) {
	baseDir := t.TempDir()
	modelDir := ModelDir(baseDir, "Acme", "demo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "demo-f16.gguf"), []byte("gguf-data"), 0o644); err != nil {
		t.Fatalf("write gguf: %v", err)
	}
	lm := &LocalModel{
		Namespace: "Acme",
		Name:      "demo",
		Format:    FormatSafeTensors,
		Size:      100,
		Files:     []string{"config.json", "model.safetensors"},
		FileEntries: []LocalModelFile{
			{Path: "config.json", Size: 2},
			{Path: "model.safetensors", Size: 98},
		},
	}
	if err := SaveManifest(baseDir, lm); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	if err := RefreshManifestFiles(modelDir); err != nil {
		t.Fatalf("RefreshManifestFiles: %v", err)
	}

	got, err := LoadManifestInDir(modelDir)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if got.Format != FormatGGUF {
		t.Fatalf("Format = %q, want %q", got.Format, FormatGGUF)
	}
	if want := []string{"config.json", "demo-f16.gguf"}; !reflect.DeepEqual(got.Files, want) {
		t.Fatalf("Files = %v, want %v", got.Files, want)
	}
	if got.Size != int64(len("{}")+len("gguf-data")) {
		t.Fatalf("Size = %d, want %d", got.Size, len("{}")+len("gguf-data"))
	}
}
