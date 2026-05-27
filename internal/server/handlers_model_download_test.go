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

	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestHandleModelManifest_BackfillsLegacyManifest(t *testing.T) {
	s := newTestServer(t)
	modelDir := filepath.Join(s.cfg.ModelDir, "Acme", "demo")
	if err := os.MkdirAll(filepath.Join(modelDir, "weights"), 0o755); err != nil {
		t.Fatalf("mkdir weights: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "weights", "model.gguf"), []byte("gguf-data"), 0o644); err != nil {
		t.Fatalf("write model.gguf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "config.json"), []byte(`{"arch":"demo"}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Acme",
		Name:         "demo",
		Format:       model.FormatGGUF,
		Size:         int64(len("gguf-data") + len(`{"arch":"demo"}`)),
		Files:        []string{"model.gguf", "config.json"},
		DownloadedAt: time.Unix(100, 0),
		Description:  "Demo model",
		License:      "apache-2.0",
		PipelineTag:  "text-generation",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/models/Acme/demo/manifest", nil)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}

	var resp api.ModelManifestResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Details.Model != "demo" {
		t.Fatalf("details.model = %q, want demo", resp.Details.Model)
	}
	if len(resp.Files) != 2 {
		t.Fatalf("files len = %d, want 2", len(resp.Files))
	}
	if resp.Files[0].Path != "config.json" || resp.Files[1].Path != "weights/model.gguf" {
		t.Fatalf("files = %#v, want sorted relative paths", resp.Files)
	}
	if resp.Files[1].DownloadURL != "/api/models/Acme/demo/files/weights/model.gguf" {
		t.Fatalf("download_url = %q, want /api/models/Acme/demo/files/weights/model.gguf", resp.Files[1].DownloadURL)
	}
	for _, file := range resp.Files {
		if file.Size <= 0 {
			t.Fatalf("file %q size = %d, want > 0", file.Path, file.Size)
		}
		if file.SHA256 == "" {
			t.Fatalf("file %q sha256 is empty", file.Path)
		}
	}

	reloaded, err := model.LoadManifest(s.cfg.ModelDir, "Acme", "demo")
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if len(reloaded.FileEntries) != 2 {
		t.Fatalf("persisted file_entries len = %d, want 2", len(reloaded.FileEntries))
	}
}

func TestHandleModelFile_SupportsHeadAndRange(t *testing.T) {
	s := newTestServer(t)
	payload := []byte("0123456789")
	sum := sha256.Sum256(payload)
	modelDir := filepath.Join(s.cfg.ModelDir, "Acme", "demo")
	if err := os.MkdirAll(filepath.Join(modelDir, "weights"), 0o755); err != nil {
		t.Fatalf("mkdir weights: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "weights", "model.gguf"), payload, 0o644); err != nil {
		t.Fatalf("write model.gguf: %v", err)
	}

	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Acme",
		Name:         "demo",
		Format:       model.FormatGGUF,
		Size:         int64(len(payload)),
		Files:        []string{"weights/model.gguf"},
		FileEntries:  []model.LocalModelFile{{Path: "weights/model.gguf", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:]), LFS: true}},
		DownloadedAt: time.Unix(100, 0),
	})

	mux := s.routes()

	headReq := httptest.NewRequest(http.MethodHead, "/api/models/Acme/demo/files/weights/model.gguf", nil)
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
	if got := headW.Header().Get("X-Checksum-SHA256"); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("HEAD checksum = %q, want %q", got, hex.EncodeToString(sum[:]))
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "/api/models/Acme/demo/files/weights/model.gguf", nil)
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

func TestHandleModelFile_RejectsInvalidPath(t *testing.T) {
	s := newTestServer(t)
	modelDir := filepath.Join(s.cfg.ModelDir, "Acme", "demo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte("demo"), 0o644); err != nil {
		t.Fatalf("write model file: %v", err)
	}
	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Acme",
		Name:         "demo",
		Format:       model.FormatGGUF,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(100, 0),
	})

	req := httptest.NewRequest(http.MethodGet, "/ignored", nil)
	req.SetPathValue("namespace", "Acme")
	req.SetPathValue("name", "demo")
	req.SetPathValue("path", "../secret.txt")
	w := httptest.NewRecorder()

	s.handleModelFile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
