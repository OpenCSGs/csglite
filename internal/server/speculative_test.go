package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDraftGGUFPathPrefersExistingGGUF(t *testing.T) {
	dir := t.TempDir()
	gguf := filepath.Join(dir, "draft-f16.gguf")
	if err := os.WriteFile(gguf, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("st"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDraftGGUFPath(dir, "ns/draft")
	if err != nil {
		t.Fatalf("resolveDraftGGUFPath() error = %v", err)
	}
	if got != gguf {
		t.Fatalf("resolveDraftGGUFPath() = %q, want %q", got, gguf)
	}
}

func TestResolveDraftGGUFPathRejectsNonConvertible(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveDraftGGUFPath(dir, "ns/empty")
	if err == nil {
		t.Fatal("expected error for non-convertible draft model")
	}
	if !strings.Contains(err.Error(), "must contain GGUF") {
		t.Fatalf("error = %q, want GGUF/convertible message", err)
	}
}
