package modelregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCSGRegistryDefaultBranch(t *testing.T) {
	for _, branch := range []string{"main", "release"} {
		t.Run(branch, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/models/acme/demo":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"path": "acme/demo", "default_branch": branch}})
				case "/api/v1/models/acme/demo/tree":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"type": "file", "name": "model.gguf", "path": "model.gguf", "size": 4}}})
				case "/api/v1/models/acme/demo/raw/model.gguf":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": "GGUF"})
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			registry := NewOpenCSG(server.URL, "")
			ctx := context.Background()
			for _, revision := range []string{"", branch, " " + branch + " "} {
				info, err := registry.GetModel(ctx, "acme/demo", revision)
				if err != nil || info.Revision != branch || info.ArtifactSource != "opencsg" {
					t.Fatalf("GetModel(%q) = %+v, %v", revision, info, err)
				}
			}
			files, _, err := registry.ListFiles(ctx, "acme/demo", branch)
			if err != nil || len(files) != 1 {
				t.Fatalf("ListFiles: %+v, %v", files, err)
			}
			content, err := registry.ReadFile(ctx, "acme/demo", branch, "model.gguf")
			if err != nil || content != "GGUF" {
				t.Fatalf("ReadFile: %q, %v", content, err)
			}
			dest := t.TempDir()
			files, _, err = registry.DownloadSnapshot(ctx, "acme/demo", branch, dest, nil, nil)
			if err != nil || len(files) != 1 {
				t.Fatalf("DownloadSnapshot: %+v, %v", files, err)
			}
			data, err := os.ReadFile(filepath.Join(dest, "model.gguf"))
			if err != nil || string(data) != "GGUF" {
				t.Fatalf("downloaded file: %q, %v", data, err)
			}
		})
	}
}

func TestOpenCSGRegistryRejectsNonDefaultRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/acme/demo" {
			t.Errorf("unsupported revision reached a file endpoint: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"default_branch": "release"}})
	}))
	defer server.Close()
	registry := NewOpenCSG(server.URL, "")
	ctx := context.Background()
	for _, revision := range []string{"main", "v1.0", "commit-123"} {
		t.Run(revision, func(t *testing.T) {
			operations := map[string]func() error{
				"GetModel":  func() error { _, err := registry.GetModel(ctx, "acme/demo", revision); return err },
				"ListFiles": func() error { _, _, err := registry.ListFiles(ctx, "acme/demo", revision); return err },
				"ReadFile":  func() error { _, err := registry.ReadFile(ctx, "acme/demo", revision, "model.gguf"); return err },
				"DownloadSnapshot": func() error {
					_, _, err := registry.DownloadSnapshot(ctx, "acme/demo", revision, t.TempDir(), nil, nil)
					return err
				},
			}
			for name, run := range operations {
				t.Run(name, func(t *testing.T) {
					if err := run(); err == nil || !strings.Contains(err.Error(), "custom revisions are not supported") {
						t.Fatalf("error = %v, want unsupported custom revision", err)
					}
				})
			}
		})
	}
}
