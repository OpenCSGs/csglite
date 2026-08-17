package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelDir(t *testing.T) {
	got := ModelDir("/base", "ns", "name")
	want := filepath.Join("/base", "ns", "name")
	if got != want {
		t.Errorf("ModelDir() = %q, want %q", got, want)
	}
}

func TestManifestPath(t *testing.T) {
	got := ManifestPath("/base", "ns", "name")
	want := filepath.Join("/base", "ns", "name", "manifest.json")
	if got != want {
		t.Errorf("ManifestPath() = %q, want %q", got, want)
	}
}

func TestEnsureModelDir(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureModelDir(dir, "test-ns", "test-model"); err != nil {
		t.Fatalf("EnsureModelDir error: %v", err)
	}

	modelDir := filepath.Join(dir, "test-ns", "test-model")
	info, err := os.Stat(modelDir)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if !info.IsDir() {
		t.Error("model dir should be a directory")
	}
}

func TestRemoveModelDir(t *testing.T) {
	dir := t.TempDir()

	// Create model directory with a file
	modelDir := filepath.Join(dir, "ns", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "test.txt"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveModelDir(dir, "ns", "model"); err != nil {
		t.Fatalf("RemoveModelDir error: %v", err)
	}

	if _, err := os.Stat(modelDir); !os.IsNotExist(err) {
		t.Error("model directory should be removed")
	}

	// Namespace dir should also be cleaned up since it's empty
	nsDir := filepath.Join(dir, "ns")
	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Error("empty namespace directory should be removed")
	}
}

func TestRemoveModelDir_KeepsOtherModels(t *testing.T) {
	dir := t.TempDir()

	// Create two models in the same namespace
	if err := os.MkdirAll(filepath.Join(dir, "ns", "model1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ns", "model2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ns", "model2", "file.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveModelDir(dir, "ns", "model1"); err != nil {
		t.Fatalf("RemoveModelDir error: %v", err)
	}

	// model1 removed
	if _, err := os.Stat(filepath.Join(dir, "ns", "model1")); !os.IsNotExist(err) {
		t.Error("model1 should be removed")
	}

	// namespace kept because model2 exists
	if _, err := os.Stat(filepath.Join(dir, "ns")); os.IsNotExist(err) {
		t.Error("namespace directory should be kept when other models exist")
	}
}

func TestListNamespaces(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ns1", "model"), 0o755); err != nil {
		t.Fatalf("mkdir ns1: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ns2", "model"), 0o755); err != nil {
		t.Fatalf("mkdir ns2: %v", err)
	}

	namespaces, err := ListNamespaces(dir)
	if err != nil {
		t.Fatalf("ListNamespaces error: %v", err)
	}
	if len(namespaces) != 2 {
		t.Errorf("len = %d, want 2", len(namespaces))
	}
}

func TestListNamespaces_Empty(t *testing.T) {
	dir := t.TempDir()
	namespaces, err := ListNamespaces(dir)
	if err != nil {
		t.Fatalf("ListNamespaces error: %v", err)
	}
	if len(namespaces) != 0 {
		t.Errorf("len = %d, want 0", len(namespaces))
	}
}

func TestListNamespaces_NonExistent(t *testing.T) {
	namespaces, err := ListNamespaces("/nonexistent/path")
	if err != nil {
		t.Fatalf("ListNamespaces error: %v", err)
	}
	if namespaces != nil {
		t.Error("should return nil for non-existent directory")
	}
}

func TestListModelsInNamespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ns", "model-a"), 0o755); err != nil {
		t.Fatalf("mkdir model-a: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ns", "model-b"), 0o755); err != nil {
		t.Fatalf("mkdir model-b: %v", err)
	}

	models, err := ListModelsInNamespace(dir, "ns")
	if err != nil {
		t.Fatalf("ListModelsInNamespace error: %v", err)
	}
	if len(models) != 2 {
		t.Errorf("len = %d, want 2", len(models))
	}
}
