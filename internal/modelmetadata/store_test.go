package modelmetadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistsMetadataAcrossReopen(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want := Metadata{
		PipelineTag:   "text-generation",
		HasMMProj:     true,
		ContextWindow: 8192,
		MaxModelLen:   32768,
	}
	if err := store.Put(context.Background(), "huggingface/acme/demo", "fingerprint", want); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, ok, err := store.Get(context.Background(), "huggingface/acme/demo", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != want {
		t.Fatalf("Get() = %#v, %v, want %#v, true", got, ok, want)
	}
}

func TestFingerprintChangesWithModelFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second-version"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("fingerprint did not change after model file update")
	}
}

func TestStoreMissesWhenFingerprintChanges(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Put(context.Background(), "acme/demo", "old", Metadata{MaxModelLen: 4096}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get(context.Background(), "acme/demo", "new"); err != nil || ok {
		t.Fatalf("Get() changed fingerprint = ok %v, err %v", ok, err)
	}
}
