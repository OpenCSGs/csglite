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

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
)

func TestNormalizeSourcePreservesOpenCSGDefault(t *testing.T) {
	for _, value := range []string{"", "opencsg", "OpenCSG"} {
		source, err := NormalizeSource(value)
		if err != nil || source != SourceOpenCSG {
			t.Fatalf("NormalizeSource(%q) = %q, %v", value, source, err)
		}
	}
	if _, err := NormalizeSource("unknown"); err == nil {
		t.Fatal("expected unsupported source error")
	}
}

func TestRegistryEnvironmentOverridesPersistedEndpoint(t *testing.T) {
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
	if hf.http.baseURL != "https://hf-env.example.test" || hf.http.token != "env-token" {
		t.Fatalf("effective registry config = %#v", hf.http)
	}
}

func TestOpenCSGRegistryRejectsCustomRevisionBeforeNetworkCall(t *testing.T) {
	registry := NewOpenCSG("", "")
	if _, err := registry.GetModel(context.Background(), "acme/demo", "main"); err == nil ||
		!strings.Contains(err.Error(), "custom revisions are not supported") {
		t.Fatalf("GetModel() error = %v", err)
	}
}

func TestRegistryHTTPClientSetsUserAgentAndExplainsAuthentication(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := newRegistryHTTPClient(server.URL, "")
	var response any
	_, err := client.getJSON(context.Background(), server.URL, &response)
	if err == nil || !strings.Contains(err.Error(), "configure an access token in Settings") {
		t.Fatalf("getJSON() error = %v", err)
	}
	if userAgent != registryUserAgent {
		t.Fatalf("User-Agent = %q, want %q", userAgent, registryUserAgent)
	}
}

func TestRegistryDownloadRejectsTruncatedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("short"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "model.bin")
	client := newRegistryHTTPClient(server.URL, "")
	err := client.downloadOnce(context.Background(), server.URL, dest, 10, nil)
	if err == nil || !strings.Contains(err.Error(), "incomplete download") {
		t.Fatalf("downloadOnce() error = %v", err)
	}
}

func TestModelScopeParameterBillionsSupportsBothUnits(t *testing.T) {
	if got := modelScopeParameterBillions(7); got != 7 {
		t.Fatalf("normalized billions = %v", got)
	}
	if got := modelScopeParameterBillions(7_000_000_000); got != 7 {
		t.Fatalf("raw parameter count = %v", got)
	}
}

func TestHuggingFaceRegistryNormalizesAndDownloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/models":
			if r.URL.Query().Get("expand") == "" {
				t.Error("expected expanded metadata fields")
			}
			if r.URL.Query().Get("filter") != "gguf" ||
				r.URL.Query().Get("pipeline_tag") != "text-generation" ||
				r.URL.Query().Get("sort") != "likes" {
				t.Errorf("unexpected Hugging Face query: %s", r.URL.RawQuery)
			}
			w.Header().Set("X-Total-Count", "1")
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": "acme/demo", "modelId": "acme/demo", "downloads": 12,
				"author":       "acme",
				"tags":         []string{"gguf", "license:apache-2.0", "text-generation"},
				"pipeline_tag": "text-generation", "library_name": "transformers",
				"cardData": map[string]any{
					"description": "A useful model.", "tags": []string{"chat"},
					"language": []string{"en"}, "base_model": "acme/base",
				},
				"safetensors": map[string]any{
					"total":      2_000_000_000,
					"parameters": map[string]int64{"BF16": 2_000_000_000},
				},
			}})
		case r.URL.Path == "/api/models/acme/demo/revision/main":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "acme/demo", "modelId": "acme/demo", "sha": "abc123",
				"tags": []string{"gguf"},
			})
		case r.URL.Path == "/api/models/acme/demo/tree/main":
			w.Header().Set("X-Repo-Commit", "abc123")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"type": "file", "path": "model.gguf", "size": 4, "oid": "blob"},
				{"type": "file", "path": "config.json", "size": 2, "oid": "config"},
			})
		case r.URL.Path == "/acme/demo/resolve/main/model.gguf":
			_, _ = w.Write([]byte("gguf"))
		case r.URL.Path == "/acme/demo/resolve/main/config.json":
			_, _ = w.Write([]byte("{}"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := NewHuggingFace(server.URL, "")
	models, total, err := registry.ListModels(context.Background(), ListOptions{
		Page: 1, PerPage: 16, Framework: "gguf", Task: "text-generation", Sort: "most_favorite",
	})
	if err != nil || total != 1 || len(models) != 1 {
		t.Fatalf("ListModels() = %#v, %d, %v", models, total, err)
	}
	if models[0].ArtifactSource != string(SourceHuggingFace) || models[0].Path != "acme/demo" {
		t.Fatalf("unexpected normalized model: %#v", models[0])
	}
	if models[0].License != "apache-2.0" || models[0].Description != "A useful model." ||
		models[0].Metadata.ModelParams != 2 || models[0].Metadata.TensorType != "BF16" {
		t.Fatalf("missing normalized Hugging Face metadata: %#v", models[0])
	}
	if countRegistryTags(models[0].Tags, "task", "text-generation") != 1 ||
		countRegistryTags(models[0].Tags, "license", "apache-2.0") != 1 {
		t.Fatalf("unexpected Hugging Face tags: %#v", models[0].Tags)
	}
	if models[0].Provider == nil || models[0].Provider.HuggingFace == nil ||
		models[0].Provider.HuggingFace.Author != "acme" ||
		len(models[0].Provider.HuggingFace.BaseModels) != 1 {
		t.Fatalf("missing Hugging Face provider metadata: %#v", models[0].Provider)
	}
	model, err := registry.GetModel(context.Background(), "acme/demo", "")
	if err != nil || model.Revision != "abc123" {
		t.Fatalf("GetModel() = %#v, %v", model, err)
	}
	dest := t.TempDir()
	files, resolved, err := registry.DownloadSnapshot(context.Background(), "acme/demo", "", dest, nil, nil)
	if err != nil || resolved != "abc123" || len(files) != 2 {
		t.Fatalf("DownloadSnapshot() = %#v, %q, %v", files, resolved, err)
	}
	if body, err := os.ReadFile(filepath.Join(dest, "model.gguf")); err != nil || string(body) != "gguf" {
		t.Fatalf("downloaded model = %q, %v", body, err)
	}
}

func TestModelScopeRegistryNormalizesAndDownloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/openapi/v1/models":
			if r.URL.Query().Get("filter.library") != "gguf" ||
				r.URL.Query().Get("filter.task") != "text-generation" ||
				r.URL.Query().Get("sort") != "likes" {
				t.Errorf("unexpected ModelScope query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"total_count": 1,
					"models": []map[string]any{{
						"id": "acme/demo", "downloads": 7, "file_size": 4,
						"license": "apache-2.0", "params": 2_000_000_000,
						"tags":  []string{"library:gguf", "license:apache-2.0", "task:text-generation"},
						"tasks": []string{"text-generation"},
					}},
				},
			})
		case r.URL.Path == "/openapi/v1/models/acme/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"id": "acme/demo", "file_size": 4, "tags": []string{"library:gguf"},
				},
			})
		case r.URL.Path == "/api/v1/models/acme/demo/repo/files":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Success": true,
				"Data": map[string]any{
					"Files": []map[string]any{{
						"Name": "model.gguf", "Path": "model.gguf", "Type": "blob", "Size": 4,
					}},
				},
			})
		case r.URL.Path == "/api/v1/models/acme/demo/repo":
			if r.URL.Query().Get("Revision") != "v1" {
				t.Errorf("Revision = %q, want v1", r.URL.Query().Get("Revision"))
			}
			_, _ = w.Write([]byte("gguf"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := NewModelScope(server.URL, "")
	models, total, err := registry.ListModels(context.Background(), ListOptions{
		Page: 1, PerPage: 16, Framework: "gguf", Task: "text-generation", Sort: "most_favorite",
	})
	if err != nil || total != 1 || len(models) != 1 {
		t.Fatalf("ListModels() = %#v, %d, %v", models, total, err)
	}
	if models[0].ArtifactSource != string(SourceModelScope) {
		t.Fatalf("artifact source = %q", models[0].ArtifactSource)
	}
	if models[0].Metadata.ModelParams != 2 || models[0].License != "apache-2.0" ||
		countRegistryTags(models[0].Tags, "task", "text-generation") != 1 ||
		countRegistryTags(models[0].Tags, "license", "apache-2.0") != 1 {
		t.Fatalf("missing normalized ModelScope metadata: %#v", models[0])
	}
	if models[0].Provider == nil || models[0].Provider.ModelScope == nil ||
		len(models[0].Provider.ModelScope.Tasks) != 1 ||
		len(models[0].Provider.ModelScope.Libraries) != 1 {
		t.Fatalf("missing ModelScope provider metadata: %#v", models[0].Provider)
	}
	dest := t.TempDir()
	files, resolved, err := registry.DownloadSnapshot(context.Background(), "acme/demo", "v1", dest, nil, nil)
	if err != nil || resolved != "v1" || len(files) != 1 {
		t.Fatalf("DownloadSnapshot() = %#v, %q, %v", files, resolved, err)
	}
}

func TestSummaryFromReadmeSkipsHeadingsAndBadges(t *testing.T) {
	readme := "# Demo\n\n[![build](badge.svg)](build)\n\n## Overview\n\nA **useful** [model](https://example.com) for local inference."
	if got := summaryFromReadme(readme); got != "A useful model for local inference." {
		t.Fatalf("summary = %q", got)
	}
}

func countRegistryTags(tags []csghub.Tag, category, name string) int {
	count := 0
	for _, tag := range tags {
		if tag.Category == category && tag.Name == name {
			count++
		}
	}
	return count
}

func TestDownloadRegistrySnapshotRejectsTraversal(t *testing.T) {
	client := newRegistryHTTPClient("https://example.invalid", "")
	_, err := downloadRegistrySnapshot(
		context.Background(),
		client,
		[]csghub.RepoFile{{Type: "file", Path: "../escape.gguf"}},
		t.TempDir(),
		nil,
		func(csghub.RepoFile) string { return "https://example.invalid/file" },
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "unsafe repository file path") {
		t.Fatalf("error = %v", err)
	}
}
