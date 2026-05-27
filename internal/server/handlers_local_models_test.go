package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestHandleLocalModelSearch_QueryAndPagination(t *testing.T) {
	s := newTestServer(t)

	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "qwen3-0.6b-gguf",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(200, 0),
		Description:  "Fast coding assistant",
		License:      "apache-2.0",
		PipelineTag:  "text-generation",
	})
	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Vision",
		Name:         "qwen2-vl",
		Format:       model.FormatGGUF,
		Size:         2048,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(100, 0),
		Description:  "Multimodal model",
		License:      "apache-2.0",
		PipelineTag:  "image-text-to-text",
	})
	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Acme",
		Name:         "embed-sft",
		Format:       model.FormatSafeTensors,
		Size:         512,
		Files:        []string{"model.safetensors"},
		DownloadedAt: time.Unix(300, 0),
		Description:  "Embedding model",
		License:      "mit",
		PipelineTag:  "feature-extraction",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/models/search?q=qwen&limit=1", nil)
	w := httptest.NewRecorder()
	s.handleLocalModelSearch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}

	var resp api.LocalModelSearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Query != "qwen" {
		t.Fatalf("query = %q, want qwen", resp.Query)
	}
	if resp.Total != 2 {
		t.Fatalf("total = %d, want 2", resp.Total)
	}
	if resp.Limit != 1 {
		t.Fatalf("limit = %d, want 1", resp.Limit)
	}
	if !resp.HasMore {
		t.Fatal("has_more = false, want true")
	}
	if len(resp.Models) != 1 {
		t.Fatalf("models len = %d, want 1", len(resp.Models))
	}
	if resp.Models[0].Model != "qwen3-0.6b-gguf" {
		t.Fatalf("first model = %q, want qwen3-0.6b-gguf", resp.Models[0].Model)
	}
	if resp.Models[0].Description != "Fast coding assistant" {
		t.Fatalf("description = %q, want Fast coding assistant", resp.Models[0].Description)
	}
	if resp.Models[0].License != "apache-2.0" {
		t.Fatalf("license = %q, want apache-2.0", resp.Models[0].License)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/models/search?q=qwen&limit=1&offset=1", nil)
	w = httptest.NewRecorder()
	s.handleLocalModelSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("offset status = %d, want %d", w.Code, http.StatusOK)
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode offset response: %v", err)
	}
	if resp.Offset != 1 {
		t.Fatalf("offset = %d, want 1", resp.Offset)
	}
	if resp.HasMore {
		t.Fatal("has_more = true, want false")
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "qwen2-vl" {
		t.Fatalf("offset models = %#v, want qwen2-vl", resp.Models)
	}
}

func TestHandleLocalModelSearch_Filters(t *testing.T) {
	s := newTestServer(t)

	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Vision",
		Name:         "qwen2-vl",
		Format:       model.FormatGGUF,
		Size:         2048,
		Files:        []string{"model.gguf", "mmproj.gguf"},
		DownloadedAt: time.Unix(100, 0),
		Description:  "Multimodal model",
		License:      "apache-2.0",
		PipelineTag:  "image-text-to-text",
	})
	if err := os.WriteFile(filepath.Join(s.cfg.ModelDir, "Vision", "qwen2-vl", "mmproj.gguf"), []byte("mmproj"), 0o644); err != nil {
		t.Fatalf("write mmproj: %v", err)
	}
	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Acme",
		Name:         "embed-sft",
		Format:       model.FormatSafeTensors,
		Size:         512,
		Files:        []string{"model.safetensors"},
		DownloadedAt: time.Unix(300, 0),
		Description:  "Embedding model",
		License:      "mit",
		PipelineTag:  "feature-extraction",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/models/search?q=embedding&format=safetensors", nil)
	w := httptest.NewRecorder()
	s.handleLocalModelSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.LocalModelSearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 1 || len(resp.Models) != 1 {
		t.Fatalf("models = %#v, want 1 safetensors match", resp.Models)
	}
	if resp.Models[0].Model != "embed-sft" {
		t.Fatalf("model = %q, want embed-sft", resp.Models[0].Model)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/models/search?pipeline_tag=image-text-to-text", nil)
	w = httptest.NewRecorder()
	s.handleLocalModelSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pipeline status = %d, want %d", w.Code, http.StatusOK)
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode pipeline response: %v", err)
	}
	if resp.Total != 1 || len(resp.Models) != 1 {
		t.Fatalf("models = %#v, want 1 vision match", resp.Models)
	}
	if resp.Models[0].Model != "qwen2-vl" {
		t.Fatalf("vision model = %q, want qwen2-vl", resp.Models[0].Model)
	}
	if !resp.Models[0].HasMMProj {
		t.Fatal("has_mmproj = false, want true")
	}
}

func TestLocalModelInfoPrefersDetectedDiffusersPipelineTag(t *testing.T) {
	s := newTestServer(t)

	mustSaveLocalModel(t, s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen-Image-2512",
		Format:       model.FormatSafeTensors,
		Files:        []string{"model_index.json", "transformer/diffusion_pytorch_model.safetensors"},
		DownloadedAt: time.Unix(100, 0),
		PipelineTag:  "text-generation",
	})
	modelDir := filepath.Join(s.cfg.ModelDir, "Qwen", "Qwen-Image-2512")
	if err := os.WriteFile(filepath.Join(modelDir, "model_index.json"), []byte(`{"_class_name":"QwenImagePipeline"}`), 0o644); err != nil {
		t.Fatalf("write model_index.json: %v", err)
	}

	info := s.localModelInfo(&model.LocalModel{
		Namespace:   "Qwen",
		Name:        "Qwen-Image-2512",
		Format:      model.FormatSafeTensors,
		PipelineTag: "text-generation",
	})
	if info.PipelineTag != "text-to-image" {
		t.Fatalf("PipelineTag = %q, want text-to-image", info.PipelineTag)
	}
	if !s.modelUsesImageGenerationEngine("Qwen/Qwen-Image-2512") {
		t.Fatal("modelUsesImageGenerationEngine = false, want true")
	}
}

func TestHandleLocalModelSearch_InvalidPaginationParams(t *testing.T) {
	s := newTestServer(t)

	tests := []string{
		"/api/models/search?limit=bad",
		"/api/models/search?limit=0",
		"/api/models/search?offset=-1",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			s.handleLocalModelSearch(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}

			var resp apiErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if resp.ErrorCode != http.StatusBadRequest {
				t.Fatalf("errorCode = %d, want %d", resp.ErrorCode, http.StatusBadRequest)
			}
			if strings.TrimSpace(resp.Error) == "" {
				t.Fatal("error message is empty")
			}
		})
	}
}

func mustSaveLocalModel(t *testing.T, baseDir string, m *model.LocalModel) {
	t.Helper()
	if err := model.SaveManifest(baseDir, m); err != nil {
		t.Fatalf("save manifest for %s/%s: %v", m.Namespace, m.Name, err)
	}
}
