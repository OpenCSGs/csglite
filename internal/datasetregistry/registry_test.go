package datasetregistry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
)

func TestNewUsesEnvironmentCredentials(t *testing.T) {
	t.Setenv(config.EnvHuggingFaceEndpoint, "https://hf-env.example.test")
	t.Setenv(config.EnvHuggingFaceToken, "env-token")
	registry, err := New(&config.Config{
		HuggingFaceEndpoint: "https://hf-config.example.test",
		HuggingFaceToken:    "config-token",
	}, SourceHuggingFace)
	if err != nil {
		t.Fatal(err)
	}
	hf := registry.(*huggingFaceRegistry)
	if hf.http.BaseURL() != "https://hf-env.example.test" || hf.http.Token() != "env-token" {
		t.Fatalf("effective registry config = %q, %q", hf.http.BaseURL(), hf.http.Token())
	}
}

func TestOpenCSGAdapterPreservesDefaultRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/datasets":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":  []map[string]any{{"name": "demo", "path": "acme/demo", "default_branch": "main"}},
				"total": 1,
			})
		case "/api/v1/datasets/acme/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"name": "demo", "path": "acme/demo", "default_branch": "main"},
			})
		case "/csg/api/datasets/acme/demo/revision/main":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha": "commit", "siblings": []map[string]any{{"rfilename": "data.jsonl"}},
			})
		case "/csg/datasets/acme/demo/resolve/main/data.jsonl":
			_, _ = w.Write([]byte("row\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := NewOpenCSG(server.URL, "")
	datasets, total, err := registry.ListDatasets(context.Background(), ListOptions{})
	if err != nil || total != 1 || len(datasets) != 1 || datasets[0].ArtifactSource != string(SourceOpenCSG) {
		t.Fatalf("ListDatasets() = %#v, %d, %v", datasets, total, err)
	}
	dataset, err := registry.GetDataset(context.Background(), "acme/demo", "")
	if err != nil || dataset.Revision != "main" {
		t.Fatalf("GetDataset() = %#v, %v", dataset, err)
	}
	if _, err := registry.GetDataset(context.Background(), "acme/demo", "dev"); err == nil {
		t.Fatal("expected custom revision rejection")
	}
	dest := t.TempDir()
	files, resolved, err := registry.DownloadSnapshot(context.Background(), "acme/demo", "", dest, nil)
	if err != nil || resolved != "" || len(files) != 1 {
		t.Fatalf("DownloadSnapshot() = %#v, %q, %v", files, resolved, err)
	}
	if body, err := os.ReadFile(filepath.Join(dest, "data.jsonl")); err != nil || string(body) != "row\n" {
		t.Fatalf("downloaded file = %q, %v", body, err)
	}
}

func TestHuggingFaceAdapterPaginatesLFSAndValidatesDownload(t *testing.T) {
	content := []byte("dataset")
	sum := fmt.Sprintf("%x", sha256.Sum256(content))
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/datasets":
			filters := r.URL.Query()["filter"]
			if r.URL.Query().Get("skip") != "16" || len(filters) != 3 ||
				filters[0] != "task_categories:text-classification" ||
				filters[1] != "language:en" || filters[2] != "license:apache-2.0" {
				t.Errorf("unexpected list query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": "acme/demo", "author": "acme", "downloads": 4,
				"tags":     []string{"license:apache-2.0", "language:en", "task_categories:text-classification"},
				"cardData": map[string]any{"pretty_name": "Demo", "language": []string{"en"}},
			}})
		case r.URL.Path == "/api/datasets/acme/demo/revision/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "acme/demo", "sha": "commit-sha"})
		case r.URL.Path == "/api/datasets/acme/demo/tree/main" && r.URL.Query().Get("cursor") == "":
			w.Header().Set("X-Repo-Commit", "commit-sha")
			w.Header().Set("Link", `<`+server.URL+`/api/datasets/acme/demo/tree/main?cursor=next>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"type": "directory", "path": "data", "size": 0, "oid": "tree",
			}})
		case r.URL.Path == "/api/datasets/acme/demo/tree/main" && r.URL.Query().Get("cursor") == "next":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"type": "file", "path": "data/train.parquet", "size": 128, "oid": "pointer",
				"lfs": map[string]any{"oid": "sha256:" + sum, "size": len(content), "pointerSize": 128},
			}})
		case r.URL.Path == "/datasets/acme/demo/resolve/main/data/train.parquet":
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := NewHuggingFace(server.URL, "secret")
	datasets, total, err := registry.ListDatasets(context.Background(), ListOptions{
		Page: 2, PerPage: 16, Task: "text-classification", Language: "en", License: "apache-2.0",
	})
	if err != nil || total != 17 || len(datasets) != 1 {
		t.Fatalf("ListDatasets() = %#v, %d, %v", datasets, total, err)
	}
	if datasets[0].License != "apache-2.0" || datasets[0].Provider.HuggingFace.Author != "acme" ||
		!hasNormalizedTag(datasets[0], "task", "text-classification") ||
		!hasNormalizedTag(datasets[0], "language", "en") {
		t.Fatalf("normalized dataset = %#v", datasets[0])
	}
	detail, err := registry.GetDataset(context.Background(), "acme/demo", "")
	if err != nil || detail.Revision != "commit-sha" {
		t.Fatalf("GetDataset() = %#v, %v", detail, err)
	}
	files, resolved, err := registry.ListFiles(context.Background(), "acme/demo", "")
	if err != nil || resolved != "commit-sha" || len(files) != 2 || !files[1].LFS ||
		files[1].Size != int64(len(content)) || files[1].LFSSHA256 != sum {
		t.Fatalf("ListFiles() = %#v, %q, %v", files, resolved, err)
	}
	dest := t.TempDir()
	files, resolved, err = registry.DownloadSnapshot(context.Background(), "acme/demo", "", dest, nil)
	if err != nil || resolved != "commit-sha" || len(files) != 1 {
		t.Fatalf("DownloadSnapshot() = %#v, %q, %v", files, resolved, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "data", "train.parquet.part")); !os.IsNotExist(err) {
		t.Fatalf("partial file remains: %v", err)
	}
}

func TestModelScopeAdapterUsesOpenAPIMetadataAndRestartsInvalidResume(t *testing.T) {
	content := []byte("complete")
	sum := fmt.Sprintf("%x", sha256.Sum256(content))
	var sawRange bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/openapi/v1/datasets":
			if r.URL.Query().Get("page_number") != "2" || r.URL.Query().Get("page_size") != "50" ||
				r.URL.Query().Get("filter.task") != "text-classification" ||
				r.URL.Query().Get("filter.language") != "zh" ||
				r.URL.Query().Get("filter.license") != "apache-2.0" {
				t.Errorf("unexpected list query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{"total_count": 1, "datasets": []map[string]any{{
					"id": "acme/demo", "display_name": "Demo", "downloads": 8,
					"license": "apache-2.0", "tasks": []string{"text-classification"},
					"languages": []string{"zh"},
				}}},
			})
		case "/openapi/v1/datasets/acme/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "data": map[string]any{"id": "acme/demo", "display_name": "Demo"},
			})
		case "/api/v1/datasets/acme/demo/repo/tree":
			if r.URL.Query().Get("Revision") != "v1" || r.URL.Query().Get("Recursive") != "true" {
				t.Errorf("unexpected tree query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 200, "Message": "success",
				"Data": map[string]any{"Files": []map[string]any{{
					"Name": "train.jsonl", "Path": "data/train.jsonl", "Type": "blob",
					"Size": len(content), "Sha256": sum, "Revision": "resolved-v1",
				}}},
			})
		case "/api/v1/datasets/acme/demo/repo":
			if r.Header.Get("Range") == "bytes=3-" {
				sawRange = true
			}
			// ModelScope mirrors may return 200 even when a Range was sent.
			// Such a response must replace, never append to, the partial file.
			w.Header().Set("Content-Range", "bytes 3-7/8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := NewModelScope(server.URL, "secret")
	datasets, total, err := registry.ListDatasets(context.Background(), ListOptions{
		Page: 2, PerPage: 100, Task: "text-classification", Language: "zh", License: "apache-2.0",
	})
	if err != nil || total != 1 || len(datasets) != 1 ||
		datasets[0].Provider.ModelScope.DisplayName != "Demo" ||
		!hasNormalizedTag(datasets[0], "task", "text-classification") ||
		!hasNormalizedTag(datasets[0], "language", "zh") {
		t.Fatalf("ListDatasets() = %#v, %d, %v", datasets, total, err)
	}
	detail, err := registry.GetDataset(context.Background(), "acme/demo", "v1")
	if err != nil || detail.Revision != "v1" {
		t.Fatalf("GetDataset() = %#v, %v", detail, err)
	}
	dest := t.TempDir()
	partial := filepath.Join(dest, "data", "train.jsonl.part")
	if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, resolved, err := registry.DownloadSnapshot(context.Background(), "acme/demo", "v1", dest, nil)
	if err != nil || resolved != "resolved-v1" || len(files) != 1 {
		t.Fatalf("DownloadSnapshot() = %#v, %q, %v", files, resolved, err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "data", "train.jsonl"))
	if err != nil || string(body) != string(content) || !sawRange {
		t.Fatalf("download = %q, saw range %v, err %v", body, sawRange, err)
	}
}

func TestSnapshotChecksumMismatchKeepsPartFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("wrong"))
	}))
	defer server.Close()

	// Reuse the HF adapter's real snapshot path to exercise adapter-to-shared
	// integrity wiring.
	registry := NewHuggingFace(server.URL, "")
	hf := registry.(*huggingFaceRegistry)
	dest := filepath.Join(t.TempDir(), "file")
	err := hf.http.DownloadOnce(context.Background(), server.URL, dest, 5, strings.Repeat("0", 64), nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("DownloadOnce() error = %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("final file should not exist: %v", err)
	}
	if _, err := os.Stat(dest + ".part"); err != nil {
		t.Fatalf("partial file should remain: %v", err)
	}
}

func hasNormalizedTag(dataset csghub.Dataset, category, name string) bool {
	for _, tag := range dataset.Tags {
		if tag.Category == category && tag.Name == name {
			return true
		}
	}
	return false
}
