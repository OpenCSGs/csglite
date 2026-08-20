package dataset

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestManagerListsSameRepositoryAcrossSources(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(&config.Config{DatasetDir: dir})
	for _, source := range []string{"opencsg", "huggingface", "modelscope"} {
		datasetDir := RegistryDatasetDir(dir, source, "acme", "demo")
		if err := SaveManifestInDir(datasetDir, &LocalDataset{
			Namespace: "acme", Name: "demo", Repository: "acme/demo", ArtifactSource: source,
		}); err != nil {
			t.Fatal(err)
		}
	}

	datasets, err := mgr.List()
	if err != nil || len(datasets) != 3 {
		t.Fatalf("List() = %#v, %v", datasets, err)
	}
	for _, datasetID := range []string{"acme/demo", "huggingface/acme/demo", "modelscope/acme/demo"} {
		got, err := mgr.Get(datasetID)
		if err != nil {
			t.Fatalf("Get(%q): %v", datasetID, err)
		}
		if got.FullName() != datasetID {
			t.Fatalf("Get(%q).FullName() = %q", datasetID, got.FullName())
		}
	}
}

func TestManagerPullFromHuggingFaceStoresSourceIdentity(t *testing.T) {
	const content = "hello dataset"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datasets/acme/demo/revision/main":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "acme/demo", "sha": "resolved-from-info",
				"cardData": map[string]any{"description": "demo", "license": "mit"},
			})
		case "/api/datasets/acme/demo/tree/main":
			w.Header().Set("X-Repo-Commit", "resolved-from-tree")
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"type": "file", "path": "data/train.txt", "size": len(content), "oid": "git-object-id",
			}})
		case "/datasets/acme/demo/resolve/main/data/train.txt":
			_, _ = w.Write([]byte(content))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	mgr := NewManager(&config.Config{DatasetDir: dir, HuggingFaceEndpoint: server.URL})
	got, err := mgr.PullFrom(context.Background(), "acme/demo", "huggingface", "main", nil)
	if err != nil {
		t.Fatalf("PullFrom() error: %v", err)
	}
	if got.ArtifactSource != "huggingface" || got.Repository != "acme/demo" ||
		got.RequestedRevision != "main" || got.ResolvedRevision != "resolved-from-info" {
		t.Fatalf("source identity = %#v", got)
	}
	datasetDir := RegistryDatasetDir(dir, "huggingface", "acme", "demo")
	body, err := os.ReadFile(filepath.Join(datasetDir, "data", "train.txt"))
	if err != nil || string(body) != content {
		t.Fatalf("downloaded file = %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(datasetDir, datasetPullStateFile)); !os.IsNotExist(err) {
		t.Fatalf("pull state remains after install: %v", err)
	}
}

func TestManagerPullUsesRegistryDownloaderForOpenCSG(t *testing.T) {
	const content = "legacy dataset"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/datasets/acme/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"name": "demo", "path": "acme/demo", "default_branch": "main"},
			})
		case "/csg/api/datasets/acme/demo/revision/main":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha": "commit", "siblings": []map[string]any{{"rfilename": "data.txt"}},
			})
		case "/csg/datasets/acme/demo/resolve/main/data.txt":
			_, _ = w.Write([]byte(content))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	mgr := NewManager(&config.Config{DatasetDir: t.TempDir(), ServerURL: server.URL})
	got, err := mgr.Pull(context.Background(), "acme/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != int64(len(content)) || len(got.FileEntries) != 1 ||
		got.FileEntries[0].Size != int64(len(content)) {
		t.Fatalf("downloaded size metadata = %#v", got)
	}
}

func TestManagerPullFromDoesNotMergeExternalSnapshots(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(&config.Config{DatasetDir: dir})
	datasetDir := RegistryDatasetDir(dir, "huggingface", "acme", "demo")
	if err := SaveManifestInDir(datasetDir, &LocalDataset{
		Namespace: "acme", Name: "demo", Repository: "acme/demo", ArtifactSource: "huggingface",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.PullFrom(context.Background(), "acme/demo", "huggingface", "main", nil); err == nil ||
		!strings.Contains(err.Error(), "already installed") {
		t.Fatalf("PullFrom() error = %v", err)
	}
}

func TestManagerRemovePartialIsSourceScoped(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(&config.Config{DatasetDir: dir})
	hfDir := RegistryDatasetDir(dir, "huggingface", "acme", "demo")
	if err := os.MkdirAll(hfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveDatasetPullState(hfDir, datasetPullState{ArtifactSource: "huggingface"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hfDir, "partial.bin"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	openCSGDir := RegistryDatasetDir(dir, "opencsg", "acme", "demo")
	if err := SaveManifestInDir(openCSGDir, &LocalDataset{Namespace: "acme", Name: "demo"}); err != nil {
		t.Fatal(err)
	}

	removed, err := mgr.RemovePartial("acme/demo", "huggingface")
	if err != nil || removed != hfDir {
		t.Fatalf("RemovePartial() = %q, %v", removed, err)
	}
	if _, err := os.Stat(hfDir); !os.IsNotExist(err) {
		t.Fatalf("Hugging Face partial directory still exists: %v", err)
	}
	if _, err := os.Stat(openCSGDir); err != nil {
		t.Fatalf("OpenCSG dataset was removed: %v", err)
	}
}

func TestManagerRejectsUnsafeDatasetIDsAndPaths(t *testing.T) {
	mgr := NewManager(&config.Config{DatasetDir: t.TempDir()})
	if _, err := mgr.RemovePartial("../escape", "huggingface"); err == nil {
		t.Fatal("RemovePartial accepted path traversal")
	}
	if _, err := mgr.Get("huggingface/../escape"); err == nil {
		t.Fatal("Get accepted path traversal")
	}
	if _, err := mgr.ListFiles("acme/demo", "../escape"); err == nil {
		t.Fatal("ListFiles accepted path traversal")
	}
}

func TestManagerSourceAwareFileOperations(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(&config.Config{DatasetDir: dir})
	datasetDir := RegistryDatasetDir(dir, "modelscope", "acme", "demo")
	if err := SaveManifestInDir(datasetDir, &LocalDataset{
		Namespace: "acme", Name: "demo", Repository: "acme/demo", ArtifactSource: "modelscope",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(datasetDir, "train"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(datasetDir, "train", "data.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mgr.Exists("modelscope/acme/demo") || mgr.Exists("acme/demo") {
		t.Fatal("Exists did not distinguish registry sources")
	}
	gotPath, err := mgr.DatasetPath("modelscope/acme/demo")
	if err != nil || gotPath != datasetDir {
		t.Fatalf("DatasetPath() = %q, %v", gotPath, err)
	}
	entries, err := mgr.ListFiles("modelscope/acme/demo", "train")
	if err != nil || len(entries) != 1 || entries[0].Name != "data.jsonl" {
		t.Fatalf("ListFiles() = %#v, %v", entries, err)
	}
	if err := mgr.Remove("modelscope/acme/demo"); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if mgr.Exists("modelscope/acme/demo") {
		t.Fatal("source-qualified dataset remains after Remove")
	}
}

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
