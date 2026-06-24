package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/dataset"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestHandleDatasetManifest_BackfillsLegacyManifest(t *testing.T) {
	s := newTestServer(t)
	datasetDir := filepath.Join(s.cfg.DatasetDir, "Acme", "demo")
	if err := os.MkdirAll(filepath.Join(datasetDir, "train"), 0o755); err != nil {
		t.Fatalf("mkdir train: %v", err)
	}
	if err := os.WriteFile(filepath.Join(datasetDir, "train", "data.jsonl"), []byte("demo"), 0o644); err != nil {
		t.Fatalf("write data file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(datasetDir, "README.md"), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	mustSaveLocalDataset(t, s.cfg.DatasetDir, &dataset.LocalDataset{
		Namespace:    "Acme",
		Name:         "demo",
		Size:         int64(len("demo") + len("# demo")),
		Files:        []string{"data.jsonl", "README.md"},
		DownloadedAt: time.Unix(100, 0),
		Origin:       dataset.LocalDatasetOriginMarketplace,
		Description:  "Demo dataset",
		License:      "cc-by-4.0",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/datasets/Acme/demo/manifest", nil)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}

	var resp api.DatasetManifestResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Details.Dataset != "Acme/demo" {
		t.Fatalf("details.dataset = %q, want Acme/demo", resp.Details.Dataset)
	}
	if resp.Details.Origin != string(dataset.LocalDatasetOriginMarketplace) {
		t.Fatalf("details.origin = %q, want marketplace", resp.Details.Origin)
	}
	if len(resp.Files) != 2 {
		t.Fatalf("files len = %d, want 2", len(resp.Files))
	}
	if resp.Files[0].Path != "README.md" || resp.Files[1].Path != "train/data.jsonl" {
		t.Fatalf("files = %#v, want sorted relative paths", resp.Files)
	}
	if resp.Files[1].DownloadURL != "/api/datasets/Acme/demo/files/train/data.jsonl" {
		t.Fatalf("download_url = %q, want /api/datasets/Acme/demo/files/train/data.jsonl", resp.Files[1].DownloadURL)
	}
	for _, file := range resp.Files {
		if file.Size <= 0 || file.SHA256 == "" {
			t.Fatalf("invalid file entry: %#v", file)
		}
	}

	reloaded, err := dataset.LoadManifest(s.cfg.DatasetDir, "Acme", "demo")
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if len(reloaded.FileEntries) != 2 {
		t.Fatalf("persisted file_entries len = %d, want 2", len(reloaded.FileEntries))
	}
}

func TestHandleDatasetFile_SupportsHeadAndRange(t *testing.T) {
	s := newTestServer(t)
	payload := []byte("0123456789")
	sum := sha256.Sum256(payload)
	datasetDir := filepath.Join(s.cfg.DatasetDir, "Acme", "demo")
	if err := os.MkdirAll(filepath.Join(datasetDir, "train"), 0o755); err != nil {
		t.Fatalf("mkdir train: %v", err)
	}
	if err := os.WriteFile(filepath.Join(datasetDir, "train", "data.jsonl"), payload, 0o644); err != nil {
		t.Fatalf("write data file: %v", err)
	}

	mustSaveLocalDataset(t, s.cfg.DatasetDir, &dataset.LocalDataset{
		Namespace:    "Acme",
		Name:         "demo",
		Size:         int64(len(payload)),
		Files:        []string{"train/data.jsonl"},
		FileEntries:  []dataset.LocalDatasetFile{{Path: "train/data.jsonl", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:]), LFS: true}},
		DownloadedAt: time.Unix(100, 0),
	})

	mux := s.routes()

	headReq := httptest.NewRequest(http.MethodHead, "/api/datasets/Acme/demo/files/train/data.jsonl", nil)
	headW := httptest.NewRecorder()
	mux.ServeHTTP(headW, headReq)
	if headW.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", headW.Code, http.StatusOK)
	}
	if headW.Body.Len() != 0 {
		t.Fatalf("HEAD body len = %d, want 0", headW.Body.Len())
	}
	if got := headW.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("HEAD Accept-Ranges = %q, want bytes", got)
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "/api/datasets/Acme/demo/files/train/data.jsonl", nil)
	rangeReq.Header.Set("Range", "bytes=2-5")
	rangeW := httptest.NewRecorder()
	mux.ServeHTTP(rangeW, rangeReq)
	if rangeW.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want %d", rangeW.Code, http.StatusPartialContent)
	}
	if got := rangeW.Body.String(); got != "2345" {
		t.Fatalf("range body = %q, want 2345", got)
	}
	if got := rangeW.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q, want bytes 2-5/10", got)
	}
}

func TestHandleDatasetFile_RejectsInvalidPath(t *testing.T) {
	s := newTestServer(t)
	datasetDir := filepath.Join(s.cfg.DatasetDir, "Acme", "demo")
	if err := os.MkdirAll(datasetDir, 0o755); err != nil {
		t.Fatalf("mkdir dataset dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(datasetDir, "data.jsonl"), []byte("demo"), 0o644); err != nil {
		t.Fatalf("write data file: %v", err)
	}
	mustSaveLocalDataset(t, s.cfg.DatasetDir, &dataset.LocalDataset{
		Namespace:    "Acme",
		Name:         "demo",
		Files:        []string{"data.jsonl"},
		DownloadedAt: time.Unix(100, 0),
	})

	req := httptest.NewRequest(http.MethodGet, "/ignored", nil)
	req.SetPathValue("namespace", "Acme")
	req.SetPathValue("name", "demo")
	req.SetPathValue("path", "../secret.txt")
	w := httptest.NewRecorder()

	s.handleDatasetFile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func mustSaveLocalDataset(t *testing.T, baseDir string, d *dataset.LocalDataset) {
	t.Helper()
	if err := dataset.SaveManifest(baseDir, d); err != nil {
		t.Fatalf("save manifest for %s/%s: %v", d.Namespace, d.Name, err)
	}
}
