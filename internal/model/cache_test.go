package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
)

func writeCachedFixture(t *testing.T, dir, source, revision, content string) *LocalModel {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	lm := &LocalModel{Namespace: "acme", Name: "demo", Repository: "acme/demo", ArtifactSource: source, RequestedRevision: revision, ResolvedRevision: revision, Format: FormatGGUF, Size: int64(len(content)), Files: []string{"model.gguf"}}
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifestInDir(dir, lm); err != nil {
		t.Fatal(err)
	}
	return lm
}
func TestEnsureFromReusesOneConcurrentDownload(t *testing.T) {
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/acme/demo/revision/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "acme/demo", "sha": "commit-one", "cardData": map[string]string{"description": "test"}})
		case "/api/models/acme/demo/tree/main":
			w.Header().Set("X-Repo-Commit", "commit-one")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": "model.gguf", "size": 4, "oid": "blob"}})
		case "/acme/demo/resolve/main/model.gguf":
			downloads.Add(1)
			_, _ = w.Write([]byte("gguf"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := &config.Config{ModelDir: t.TempDir(), HuggingFaceEndpoint: server.URL}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := NewManager(cfg).EnsureFrom(context.Background(), "acme/demo", "huggingface", "main", nil, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if downloads.Load() != 1 {
		t.Fatalf("same artifact downloaded %d times", downloads.Load())
	}
	if _, err := NewManager(cfg).EnsureFrom(context.Background(), "acme/demo", "huggingface", "different", nil, nil); err == nil {
		t.Fatal("incompatible revision reused")
	}
	if downloads.Load() != 1 {
		t.Fatal("revision conflict overwrote existing snapshot")
	}
}
func TestAdoptCacheMovesPreservesMetadataAndReusesLibrary(t *testing.T) {
	base := t.TempDir()
	mgr := NewManager(&config.Config{ModelDir: filepath.Join(base, "library")})
	legacy := filepath.Join(base, "legacy")
	original := writeCachedFixture(t, legacy, "modelscope", "master", "gguf")
	original.RemovedSourceWeights = []string{"weights.safetensors"}
	if err := SaveManifestInDir(legacy, original); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AdoptCacheDirectory(context.Background(), legacy, "modelscope/acme/demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy data retained after successful move")
	}
	lm, err := mgr.EnsureFrom(context.Background(), "acme/demo", "modelscope", "master", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lm.FullName() != original.FullName() || len(lm.RemovedSourceWeights) != 1 {
		t.Fatal("migration lost manifest identity or conversion metadata")
	}
	listed, err := mgr.List()
	if err != nil || len(listed) != 1 {
		t.Fatal("migrated cache missing from model library", err)
	}
}
func TestAdoptCacheNeverOverwritesConflictingFiles(t *testing.T) {
	for _, conflict := range []string{"revision", "bytes"} {
		t.Run(conflict, func(t *testing.T) {
			base := t.TempDir()
			mgr := NewManager(&config.Config{ModelDir: filepath.Join(base, "library")})
			legacy := filepath.Join(base, "legacy")
			dest := RegistryModelDir(mgr.cfg.ModelDir, "opencsg", "acme", "demo")
			writeCachedFixture(t, legacy, "opencsg", "main", "old")
			revision, content := "main", "old"
			if conflict == "revision" {
				revision = "v2"
			} else {
				content = "new"
			}
			writeCachedFixture(t, dest, "opencsg", revision, content)
			if err := mgr.AdoptCacheDirectory(context.Background(), legacy, "acme/demo"); err == nil {
				t.Fatal("conflict accepted")
			}
			body, _ := os.ReadFile(filepath.Join(dest, "model.gguf"))
			if string(body) != content {
				t.Fatal("existing files overwritten")
			}
			if _, err := os.Stat(filepath.Join(legacy, "model.gguf")); err != nil {
				t.Fatal("conflicting legacy data lost")
			}
		})
	}
}
func TestAdoptCacheMergesMatchingSnapshotVariants(t *testing.T) {
	base := t.TempDir()
	mgr := NewManager(&config.Config{ModelDir: filepath.Join(base, "library")})
	legacy := filepath.Join(base, "legacy")
	dest := RegistryModelDir(mgr.cfg.ModelDir, "opencsg", "acme", "demo")
	writeCachedFixture(t, legacy, "opencsg", "main", "same")
	writeCachedFixture(t, dest, "opencsg", "main", "same")
	if err := os.WriteFile(filepath.Join(legacy, "model-Q4_K_M.gguf"), []byte("variant"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AdoptCacheDirectory(context.Background(), legacy, "acme/demo"); err != nil {
		t.Fatal(err)
	}
	lm, err := mgr.Get("acme/demo")
	if err != nil || len(lm.Files) != 2 || lm.Size != 11 {
		t.Fatalf("variants not merged: %+v %v", lm, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy directory retained")
	}
}
func TestAdoptPartialAndArtifactLockCancellation(t *testing.T) {
	base := t.TempDir()
	mgr := NewManager(&config.Config{ModelDir: filepath.Join(base, "library")})
	legacy := filepath.Join(base, "legacy")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveModelPullState(legacy, modelPullState{ArtifactSource: "huggingface", Revision: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "model.part"), []byte("resume"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AdoptCacheDirectory(context.Background(), legacy, "huggingface/acme/demo"); err != nil {
		t.Fatal(err)
	}
	dir, _ := mgr.ModelPath("huggingface/acme/demo")
	state, err := loadModelPullState(dir)
	if err != nil || state.Revision != "main" {
		t.Fatal("partial resume state lost", err)
	}
	unlock, err := mgr.LockArtifact(context.Background(), "huggingface/acme/demo")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := NewManager(mgr.cfg).LockArtifact(ctx, "huggingface/acme/demo"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting artifact operation ignored cancellation", err)
	}
	unlock()
	if err := mgr.RemoveCacheWhen(context.Background(), "huggingface/acme/demo", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("partial weights not deleted")
	}
}

func TestEnsureFromRejectsMissingCachedQuant(t *testing.T) {
	mgr := NewManager(&config.Config{ModelDir: t.TempDir()})
	dir := RegistryModelDir(mgr.cfg.ModelDir, "opencsg", "acme", "demo")
	lm := writeCachedFixture(t, dir, "opencsg", "main", "gguf")
	if err := os.Rename(filepath.Join(dir, "model.gguf"), filepath.Join(dir, "model-Q4_K_M.gguf")); err != nil {
		t.Fatal(err)
	}
	lm.Files = []string{"model-Q4_K_M.gguf"}
	if err := SaveManifestInDir(dir, lm); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.EnsureFrom(context.Background(), "acme/demo", "opencsg", "main", []string{"q8_0"}, nil); err == nil {
		t.Fatal("missing quant silently reused")
	}
	if _, err := mgr.EnsureFrom(context.Background(), "acme/demo", "opencsg", "main", []string{"q4_k_m"}, nil); err != nil {
		t.Fatal(err)
	}
}
