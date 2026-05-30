package csghub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("path = %q, want /api/v1/models", r.URL.Path)
		}

		q := r.URL.Query()
		if q.Get("search") != "qwen" {
			t.Errorf("search = %q, want %q", q.Get("search"), "qwen")
		}
		if q.Get("tag_category") != "framework" {
			t.Errorf("tag_category = %q, want %q", q.Get("tag_category"), "framework")
		}
		if q.Get("tag_name") != "gguf" {
			t.Errorf("tag_name = %q, want %q", q.Get("tag_name"), "gguf")
		}
		if q.Get("framework") != "" {
			t.Errorf("framework = %q, want empty", q.Get("framework"))
		}
		if q.Get("page") != "2" {
			t.Errorf("page = %q, want %q", q.Get("page"), "2")
		}
		if q.Get("model_params_min") != "6" {
			t.Errorf("model_params_min = %q, want %q", q.Get("model_params_min"), "6")
		}
		if q.Get("model_params_max") != "6.99999" {
			t.Errorf("model_params_max = %q, want %q", q.Get("model_params_max"), "6.99999")
		}

		resp := ListResponse[Model]{
			Msg:   "OK",
			Data:  []Model{{ID: 1, Name: "Qwen-7B", Path: "Qwen/Qwen-7B"}},
			Total: 50,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	models, total, err := c.ListModels(context.Background(), ModelListParams{
		Search:         "qwen",
		TagCategory:    "framework",
		TagName:        "gguf",
		Page:           2,
		ModelParamsMin: "6",
		ModelParamsMax: "6.99999",
	})
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if total != 50 {
		t.Errorf("total = %d, want 50", total)
	}
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1", len(models))
	}
	if models[0].Name != "Qwen-7B" {
		t.Errorf("Name = %q, want %q", models[0].Name, "Qwen-7B")
	}
}

func TestGetModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/OpenCSG/csg-wukong-1B" {
			t.Errorf("path = %q, want /api/v1/models/OpenCSG/csg-wukong-1B", r.URL.Path)
		}
		resp := APIResponse[Model]{
			Msg:  "OK",
			Data: Model{ID: 367, Name: "csg-wukong-1B", Path: "OpenCSG/csg-wukong-1B", DefaultBranch: "main"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	m, err := c.GetModel(context.Background(), "OpenCSG", "csg-wukong-1B")
	if err != nil {
		t.Fatalf("GetModel error: %v", err)
	}
	if m.Name != "csg-wukong-1B" {
		t.Errorf("Name = %q, want %q", m.Name, "csg-wukong-1B")
	}
	if m.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", m.DefaultBranch, "main")
	}
}

func TestGetModelTree(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/OpenCSG/csg-wukong-1B/tree" {
			t.Errorf("path = %q", r.URL.Path)
		}
		resp := APIResponse[[]RepoFile]{
			Msg: "OK",
			Data: []RepoFile{
				{Name: "config.json", Type: "file", Size: 555, LFS: false},
				{Name: "model.safetensors", Type: "file", Size: 2200119664, LFS: true, LFSSHA256: "abc123"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	files, err := c.GetModelTree(context.Background(), "OpenCSG", "csg-wukong-1B")
	if err != nil {
		t.Fatalf("GetModelTree error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files len = %d, want 2", len(files))
	}
	if files[0].Name != "config.json" {
		t.Errorf("files[0].Name = %q, want %q", files[0].Name, "config.json")
	}
	if !files[1].LFS {
		t.Error("files[1].LFS = false, want true")
	}
}

func TestSearchModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("search") != "llama" {
			t.Errorf("search = %q, want %q", q.Get("search"), "llama")
		}
		if q.Get("per") != "10" {
			t.Errorf("per = %q, want %q", q.Get("per"), "10")
		}

		resp := ListResponse[Model]{
			Msg:   "OK",
			Data:  []Model{{Name: "llama-7b"}},
			Total: 5,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	models, total, err := c.SearchModels(context.Background(), "llama", 1, 10)
	if err != nil {
		t.Fatalf("SearchModels error: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1", len(models))
	}
}
