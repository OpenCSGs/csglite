package modelmetadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
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
		PipelineTag:     "text-generation",
		HasMMProj:       true,
		ContextWindow:   8192,
		MaxModelLen:     32768,
		ModelMaxContext: 40960,
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

func TestOpenUpgradesCacheWithoutModelMaxContext(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, DirName, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE model_metadata (
	storage_id TEXT PRIMARY KEY,
	fingerprint TEXT NOT NULL,
	pipeline_tag TEXT NOT NULL DEFAULT '',
	has_mmproj INTEGER NOT NULL DEFAULT 0,
	context_window INTEGER NOT NULL DEFAULT 0,
	max_model_len INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open() on older cache: %v", err)
	}
	defer store.Close()
	want := Metadata{PipelineTag: "text-generation", ModelMaxContext: 32768}
	if err := store.Put(context.Background(), "acme/demo", "fingerprint", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(context.Background(), "acme/demo", "fingerprint")
	if err != nil || !ok || got != want {
		t.Fatalf("Get() = %#v, %v, %v, want %#v", got, ok, err, want)
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

// A change in derivation logic must invalidate cached entries even though the
// model files are untouched, otherwise an upgraded build keeps serving a
// pipeline tag computed by the previous one. The digest is therefore seeded
// with DerivationVersion rather than being a pure file-identity hash.
func TestFingerprintIsSeededWithDerivationVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	unseeded := sha256.New()
	fmt.Fprintf(unseeded, "%s\x00%d\x00%d\x00%d\n", "config.json", info.Size(), info.ModTime().UnixNano(), info.Mode())
	if got == hex.EncodeToString(unseeded.Sum(nil)) {
		t.Fatal("Fingerprint() is a pure file-identity hash; a derivation-logic change would not invalidate the cache")
	}

	seeded := sha256.New()
	fmt.Fprintf(seeded, "derivation-version\x00%d\n", DerivationVersion)
	fmt.Fprintf(seeded, "%s\x00%d\x00%d\x00%d\n", "config.json", info.Size(), info.ModTime().UnixNano(), info.Mode())
	if got != hex.EncodeToString(seeded.Sum(nil)) {
		t.Fatalf("Fingerprint() = %q, want the DerivationVersion-seeded digest", got)
	}
}
