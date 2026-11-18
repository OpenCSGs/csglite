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
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
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

func TestOpenCSGFillFileSizesSumsTree(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/acme/image/tree" {
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if dir := r.URL.Query().Get("path"); dir != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"type": "file", "name": "weights.safetensors", "path": dir + "/weights.safetensors", "size": 300}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"type": "file", "name": "config.json", "path": "config.json", "size": 100},
				{"type": "dir", "name": "transformer", "path": "transformer", "size": 1},
			},
		})
	}))
	defer server.Close()

	registry := NewOpenCSG(server.URL, "")
	filler, ok := registry.(FileSizeFiller)
	if !ok {
		t.Fatal("OpenCSG registry does not implement FileSizeFiller")
	}
	models := []csghub.Model{
		{Path: "acme/image"},
		{Path: "acme/known", RepoSize: 42},
	}
	if err := filler.FillFileSizes(context.Background(), models, 0); err != nil {
		t.Fatalf("FillFileSizes: %v", err)
	}
	if models[0].RepoSize != 400 {
		t.Fatalf("RepoSize = %d, want 400 (files in every directory)", models[0].RepoSize)
	}
	if models[1].RepoSize != 42 {
		t.Fatalf("known RepoSize = %d, want 42 (must not be overwritten)", models[1].RepoSize)
	}
}

func TestOpenCSGFillFileSizesBoundsEachModel(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models/acme/slow/tree":
			// A repository with many directories keeps the handler busy until
			// the client gives up on this model alone.
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case "/api/v1/models/acme/fast/tree":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"type": "file", "name": "w.safetensors", "path": "w.safetensors", "size": 700}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	previous := fillFileSizesPerModelTimeout
	fillFileSizesPerModelTimeout = 100 * time.Millisecond
	defer func() { fillFileSizesPerModelTimeout = previous }()

	registry := NewOpenCSG(server.URL, "")
	filler, ok := registry.(FileSizeFiller)
	if !ok {
		t.Fatal("OpenCSG registry does not implement FileSizeFiller")
	}
	models := []csghub.Model{{Path: "acme/slow"}, {Path: "acme/fast"}}
	if err := filler.FillFileSizes(context.Background(), models, 0); err == nil {
		t.Fatal("FillFileSizes error = nil, want the slow tree to report its deadline")
	}
	if models[0].RepoSize != 0 {
		t.Fatalf("slow RepoSize = %d, want 0 (the walk was cancelled)", models[0].RepoSize)
	}
	if models[1].RepoSize != 700 {
		t.Fatalf("fast RepoSize = %d, want 700 (one slow tree must not starve the rest)", models[1].RepoSize)
	}
}

func TestOpenCSGFillRepoSizesFallsBackToParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/extra":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"repo_id": 42, "size": 0, "last_commit_size": 0}},
			})
		case r.URL.Path == "/api/v1/models/acme/huge":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"path":           "acme/huge",
					"default_branch": "main",
					"metadata":       map[string]any{"model_params": 684.53},
				},
			})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := NewOpenCSG(server.URL, "")
	filler, ok := registry.(RepoSizeFiller)
	if !ok {
		t.Fatal("OpenCSG registry does not implement RepoSizeFiller")
	}
	models := []csghub.Model{{Path: "acme/huge", RepositoryID: 42}}
	if err := filler.FillRepoSizes(context.Background(), models); err != nil {
		t.Fatalf("FillRepoSizes: %v", err)
	}
	if models[0].RepoSize != 0 {
		t.Fatalf("RepoSize = %d, want 0 (extras reports no size)", models[0].RepoSize)
	}
	if models[0].Metadata.ModelParams != 684.53 {
		t.Fatalf("ModelParams = %v, want the detail fallback 684.53", models[0].Metadata.ModelParams)
	}
}

func TestOpenCSGFillRepoSizesSkipsInvalidParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/extra":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"repo_id": 42, "size": 0, "last_commit_size": 0}},
			})
		case r.URL.Path == "/api/v1/models/acme/moe":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"path":           "acme/moe",
					"default_branch": "main",
					"metadata":       map[string]any{"model_params": 2446.18, "model_params_valid": false},
				},
			})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := NewOpenCSG(server.URL, "")
	filler, ok := registry.(RepoSizeFiller)
	if !ok {
		t.Fatal("OpenCSG registry does not implement RepoSizeFiller")
	}
	models := []csghub.Model{{Path: "acme/moe", RepositoryID: 42}}
	if err := filler.FillRepoSizes(context.Background(), models); err != nil {
		t.Fatalf("FillRepoSizes: %v", err)
	}
	if models[0].Metadata.ModelParams != 0 {
		t.Fatalf("ModelParams = %v, want 0 (detail marks the scan invalid)", models[0].Metadata.ModelParams)
	}
	if models[0].Metadata.ValidModelParams() != 0 {
		t.Fatalf("ValidModelParams = %v, want 0", models[0].Metadata.ValidModelParams())
	}
}
