package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/csghub"
	"github.com/opencsgs/csghub-lite/internal/model"
)

func TestHandleMarketplaceModelsMapsFrameworkToTagFilter(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("tag_category"); got != "framework" {
			t.Fatalf("tag_category = %q, want %q", got, "framework")
		}
		if got := r.URL.Query().Get("tag_name"); got != "gguf" {
			t.Fatalf("tag_name = %q, want %q", got, "gguf")
		}
		if got := r.URL.Query().Get("framework"); got != "" {
			t.Fatalf("framework = %q, want empty", got)
		}
		if got := r.URL.Query().Get("model_params_min"); got != "6" {
			t.Fatalf("model_params_min = %q, want %q", got, "6")
		}
		if got := r.URL.Query().Get("model_params_max"); got != "6.99999" {
			t.Fatalf("model_params_max = %q, want %q", got, "6.99999")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
			Msg:   "OK",
			Data:  []csghub.Model{},
			Total: 0,
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?framework=gguf&model_params_min=6&model_params_max=6.99999", nil)
	w := httptest.NewRecorder()

	s.handleMarketplaceModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleMarketplaceModelsMapsTaskToTagFilter(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("tag_category"); got != "task" {
			t.Fatalf("tag_category = %q, want %q", got, "task")
		}
		if got := r.URL.Query().Get("tag_name"); got != "text-generation" {
			t.Fatalf("tag_name = %q, want %q", got, "text-generation")
		}
		if got := r.URL.Query().Get("task"); got != "" {
			t.Fatalf("task = %q, want empty", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
			Msg:   "OK",
			Data:  []csghub.Model{},
			Total: 0,
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?task=text-generation", nil)
	w := httptest.NewRecorder()

	s.handleMarketplaceModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleMarketplaceModelsServesCachedListOnUpstreamRateLimit(t *testing.T) {
	marketplaceListCache.Lock()
	marketplaceListCache.entries = make(map[string]marketplaceListCacheEntry)
	marketplaceListCache.Unlock()

	requests := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests > 1 {
			http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
			Msg:   "OK",
			Data:  []csghub.Model{{ID: 1, Path: "ns/model"}},
			Total: 1,
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?page=9", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/marketplace/models?page=9", nil)
	w = httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second status = %d, want cached %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Header().Get("X-CSGHUB-Lite-Cache"); got != "fresh" {
		t.Fatalf("cache header = %q, want fresh", got)
	}

	var resp struct {
		Data  []csghub.Model `json:"data"`
		Total int            `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Path != "ns/model" {
		t.Fatalf("cached data = %#v, want ns/model", resp.Data)
	}
}

func TestHandleMarketplaceModelsFallbackFiltersFrameworkTags(t *testing.T) {
	allModels := []csghub.Model{
		{
			ID:   1,
			Path: "ns/safetensors-one",
			Tags: []csghub.Tag{{Name: "safetensors", Category: "framework", ShowName: "SafeTensors"}},
		},
		{
			ID:   2,
			Path: "ns/gguf-one",
			Tags: []csghub.Tag{{Name: "gguf", Category: "framework", ShowName: "GGUF"}},
		},
		{
			ID:   3,
			Path: "ns/pytorch-one",
			Tags: []csghub.Tag{{Name: "pytorch", Category: "framework", ShowName: "PyTorch"}},
		},
		{
			ID:   4,
			Path: "ns/gguf-two",
			Tags: []csghub.Tag{{Name: "gguf", Category: "framework", ShowName: "GGUF"}},
		},
	}

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("tag_category"); got != "framework" {
			t.Fatalf("tag_category = %q, want %q", got, "framework")
		}
		if got := r.URL.Query().Get("tag_name"); got != "gguf" {
			t.Fatalf("tag_name = %q, want %q", got, "gguf")
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("per"))
		if page <= 0 {
			page = 1
		}
		if per <= 0 {
			per = len(allModels)
		}

		start := (page - 1) * per
		if start > len(allModels) {
			start = len(allModels)
		}
		end := start + per
		if end > len(allModels) {
			end = len(allModels)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
			Msg:   "OK",
			Data:  allModels[start:end],
			Total: len(allModels),
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?framework=gguf&page=1&per=2", nil)
	w := httptest.NewRecorder()

	s.handleMarketplaceModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Data  []csghub.Model `json:"data"`
		Total int            `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("data len = %d, want 2", len(resp.Data))
	}
	for _, model := range resp.Data {
		if !marketplaceModelHasFramework(model.Tags, "gguf") {
			t.Fatalf("model %q should have been filtered out", model.Path)
		}
	}
	if resp.Data[0].Path != "ns/gguf-one" || resp.Data[1].Path != "ns/gguf-two" {
		t.Fatalf("unexpected order after filtering: %#v", resp.Data)
	}
	if resp.Total != 2 {
		t.Fatalf("total = %d, want 2", resp.Total)
	}
}

func TestHandleMarketplaceModelsFiltersFrameworkWithinTaskResults(t *testing.T) {
	allModels := []csghub.Model{
		{
			ID:   1,
			Path: "ns/safetensors-text",
			Tags: []csghub.Tag{
				{Name: "safetensors", Category: "framework", ShowName: "SafeTensors"},
				{Name: "text-generation", Category: "task", ShowName: "Text Generation"},
			},
		},
		{
			ID:   2,
			Path: "ns/gguf-text",
			Tags: []csghub.Tag{
				{Name: "gguf", Category: "framework", ShowName: "GGUF"},
				{Name: "text-generation", Category: "task", ShowName: "Text Generation"},
			},
		},
	}

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("tag_category"); got != "task" {
			t.Fatalf("tag_category = %q, want %q", got, "task")
		}
		if got := r.URL.Query().Get("tag_name"); got != "text-generation" {
			t.Fatalf("tag_name = %q, want %q", got, "text-generation")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
			Msg:   "OK",
			Data:  allModels,
			Total: len(allModels),
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?framework=gguf&task=text-generation&page=1&per=2", nil)
	w := httptest.NewRecorder()

	s.handleMarketplaceModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Data  []csghub.Model `json:"data"`
		Total int            `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("data len = %d, want 1", len(resp.Data))
	}
	if resp.Data[0].Path != "ns/gguf-text" {
		t.Fatalf("model path = %q, want ns/gguf-text", resp.Data[0].Path)
	}
	if resp.Total != 1 {
		t.Fatalf("total = %d, want 1", resp.Total)
	}
}

func TestHandleMarketplaceModelDetailReturnsQuantizations(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/Qwen/Qwen3-GGUF":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:          1,
					Name:        "Qwen3-GGUF",
					Path:        "Qwen/Qwen3-GGUF",
					Description: "GGUF model",
					Likes:       12,
					Downloads:   34,
					License:     "apache-2.0",
					Tags: []csghub.Tag{
						{Name: "gguf", Category: "framework", ShowName: "GGUF"},
						{Name: "text-generation", Category: "task", ShowName: "Text Generation"},
					},
					Metadata: csghub.ModelMetadata{
						ModelParams:  7.6,
						TensorType:   "F16",
						Architecture: "QwenForCausalLM",
					},
				},
			})
		case "/api/v1/models/Qwen/Qwen3-GGUF/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{
				Msg: "OK",
				Data: []csghub.RepoFile{
					{Path: "Q8_0/Qwen3-Q8_0-00001-of-00002.gguf", Name: "Qwen3-Q8_0-00001-of-00002.gguf"},
					{Path: "Q8_0/Qwen3-Q8_0-00002-of-00002.gguf", Name: "Qwen3-Q8_0-00002-of-00002.gguf"},
					{Path: "Q4_K_M/Qwen3-Q4_K_M-00001-of-00002.gguf", Name: "Qwen3-Q4_K_M-00001-of-00002.gguf"},
					{Path: "Q4_K_M/Qwen3-Q4_K_M-00002-of-00002.gguf", Name: "Qwen3-Q4_K_M-00002-of-00002.gguf"},
					{Path: "mmproj/model-mmproj.gguf", Name: "model-mmproj.gguf"},
				},
			})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/Qwen/Qwen3-GGUF", nil)
	req.SetPathValue("namespace", "Qwen")
	req.SetPathValue("name", "Qwen3-GGUF")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Details == nil {
		t.Fatal("details = nil")
	}
	if resp.Details.Metadata.ModelParams != 7.6 {
		t.Fatalf("model params = %v, want 7.6", resp.Details.Metadata.ModelParams)
	}
	if len(resp.Quantizations) != 2 {
		t.Fatalf("quantizations len = %d, want 2", len(resp.Quantizations))
	}
	if resp.Quantizations[0].Name != "Q8_0" || resp.Quantizations[0].FileCount != 2 {
		t.Fatalf("first quantization = %#v, want Q8_0 x2", resp.Quantizations[0])
	}
	if resp.Quantizations[1].Name != "Q4_K_M" || resp.Quantizations[1].FileCount != 2 {
		t.Fatalf("second quantization = %#v, want Q4_K_M x2", resp.Quantizations[1])
	}
	if !resp.LocalInference.Supported || resp.LocalInference.Runtime != "llama" || resp.LocalInference.Mode != "direct" || resp.LocalInference.RuntimeArchitecture != "qwen2" {
		t.Fatalf("local_inference = %#v, want llama direct qwen2", resp.LocalInference)
	}
}

func TestHandleMarketplaceModelDetailReturnsLocalModelStatus(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/openai/openai_whisper-large-v3":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:   1,
					Name: "openai_whisper-large-v3",
					Path: "openai/openai_whisper-large-v3",
					Tags: []csghub.Tag{
						{Name: "safetensors", Category: "framework", ShowName: "SafeTensors"},
						{Name: "automatic-speech-recognition", Category: "task", ShowName: "Automatic Speech Recognition"},
					},
					Metadata: csghub.ModelMetadata{
						Architecture: "WhisperForConditionalGeneration",
					},
				},
			})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL
	if err := model.SaveManifest(s.cfg.ModelDir, &model.LocalModel{
		Namespace: "openai",
		Name:      "openai_whisper-large-v3",
		Format:    model.FormatSafeTensors,
	}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/openai/openai_whisper-large-v3", nil)
	req.SetPathValue("namespace", "openai")
	req.SetPathValue("name", "openai_whisper-large-v3")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.LocalModel.Downloaded {
		t.Fatalf("local_model.downloaded = false, want true")
	}
	if resp.LocalModel.FullName != "openai/openai_whisper-large-v3" {
		t.Fatalf("local_model.full_name = %q, want openai/openai_whisper-large-v3", resp.LocalModel.FullName)
	}
	if resp.LocalModel.PublicID != "openai_whisper-large-v3" {
		t.Fatalf("local_model.public_id = %q, want openai_whisper-large-v3", resp.LocalModel.PublicID)
	}
}

func TestHandleMarketplaceModelDetailInfersLocalInferenceFromRepoFiles(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/Qwen/Qwen3-Embedding-4B":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:   1,
					Name: "Qwen3-Embedding-4B",
					Path: "Qwen/Qwen3-Embedding-4B",
				},
			})
		case "/api/v1/models/Qwen/Qwen3-Embedding-4B/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{
				Msg: "OK",
				Data: []csghub.RepoFile{
					{Path: "config.json", Name: "config.json"},
					{Path: "model-00001-of-00002.safetensors", Name: "model-00001-of-00002.safetensors", LFS: true},
					{Path: "model-00002-of-00002.safetensors", Name: "model-00002-of-00002.safetensors", LFS: true},
				},
			})
		case "/api/v1/models/Qwen/Qwen3-Embedding-4B/raw/config.json":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[string]{
				Msg:  "OK",
				Data: `{"architectures":["Qwen3ForCausalLM"],"model_type":"qwen3"}`,
			})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/Qwen/Qwen3-Embedding-4B", nil)
	req.SetPathValue("namespace", "Qwen")
	req.SetPathValue("name", "Qwen3-Embedding-4B")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.LocalInference.Supported || resp.LocalInference.Runtime != "llama" || resp.LocalInference.Mode != "convert" || resp.LocalInference.RuntimeArchitecture != "qwen3" {
		t.Fatalf("local_inference = %#v, want llama convert qwen3", resp.LocalInference)
	}
	if resp.Details.Metadata.Architecture != "Qwen3ForCausalLM" {
		t.Fatalf("architecture = %q, want Qwen3ForCausalLM", resp.Details.Metadata.Architecture)
	}
	if resp.Details.Metadata.ModelType != "qwen3" {
		t.Fatalf("model_type = %q, want qwen3", resp.Details.Metadata.ModelType)
	}
	if !hasMarketplaceTag(resp.Details.Tags, "safetensors", "framework") {
		t.Fatalf("tags = %#v, want safetensors framework tag", resp.Details.Tags)
	}
	if !hasMarketplaceTag(resp.Details.Tags, "feature-extraction", "task") {
		t.Fatalf("tags = %#v, want feature-extraction task tag", resp.Details.Tags)
	}
}

func TestHandleMarketplaceModelDetailIgnoresTreeError(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/Qwen/Qwen3-GGUF":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:   1,
					Name: "Qwen3-GGUF",
					Path: "Qwen/Qwen3-GGUF",
					Tags: []csghub.Tag{
						{Name: "gguf", Category: "framework", ShowName: "GGUF"},
					},
				},
			})
		case "/api/v1/models/Qwen/Qwen3-GGUF/tree":
			http.Error(w, "tree failed", http.StatusBadGateway)
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/Qwen/Qwen3-GGUF", nil)
	req.SetPathValue("namespace", "Qwen")
	req.SetPathValue("name", "Qwen3-GGUF")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Details == nil {
		t.Fatal("details = nil")
	}
	if len(resp.Quantizations) != 0 {
		t.Fatalf("quantizations = %#v, want empty when tree fails", resp.Quantizations)
	}
}

func hasMarketplaceTag(tags []csghub.Tag, name, category string) bool {
	for _, tag := range tags {
		if tag.Name == name && tag.Category == category {
			return true
		}
	}
	return false
}
