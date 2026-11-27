package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/internal/model"
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

func TestHandleMarketplaceModelsDispatchesHuggingFaceSource(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
			"id":      "acme/demo",
			"modelId": "acme/demo",
			"tags":    []string{"gguf"},
		}})
	}))
	defer apiServer.Close()
	t.Setenv("HF_ENDPOINT", apiServer.URL)

	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?artifact_source=huggingface", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Data       []csghub.Model `json:"data"`
		HasMore    bool           `json:"has_more"`
		TotalExact bool           `json:"total_exact"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].ArtifactSource != "huggingface" {
		t.Fatalf("models = %#v", response.Data)
	}
	if response.HasMore || response.TotalExact {
		t.Fatalf("paging = has_more=%v total_exact=%v, want incomplete estimated page", response.HasMore, response.TotalExact)
	}
}

func TestMarketplaceListPaging(t *testing.T) {
	hasMore, exact := marketplaceListPaging("huggingface", 1, 16, 16, 17)
	if !hasMore || exact {
		t.Fatalf("hf full page = %v, %v, want has_more and estimated total", hasMore, exact)
	}
	hasMore, exact = marketplaceListPaging("huggingface", 2, 16, 3, 35)
	if hasMore || exact {
		t.Fatalf("hf short page = %v, %v, want last estimated page", hasMore, exact)
	}
	hasMore, exact = marketplaceListPaging("opencsg", 1, 16, 16, 48)
	if !hasMore || !exact {
		t.Fatalf("opencsg first page = %v, %v, want exact remaining pages", hasMore, exact)
	}
	hasMore, exact = marketplaceListPaging("modelscope", 3, 16, 16, 48)
	if hasMore || !exact {
		t.Fatalf("modelscope last page = %v, %v, want exact end", hasMore, exact)
	}
}

func TestHandleMarketplaceModelsRejectsUnknownArtifactSource(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?artifact_source=unknown", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleMarketplaceDatasetsDispatchesProviderFilters(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datasets" {
			t.Fatalf("path = %q, want /api/datasets", r.URL.Path)
		}
		filters := r.URL.Query()["filter"]
		want := map[string]bool{
			"task_categories:text-classification": true,
			"language:en":                         true,
			"license:apache-2.0":                  true,
		}
		for _, filter := range filters {
			delete(want, filter)
		}
		if len(want) != 0 {
			t.Fatalf("filters = %#v, missing %#v", filters, want)
		}
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
			"id": "acme/demo", "tags": []string{"license:apache-2.0"},
		}})
	}))
	defer apiServer.Close()
	t.Setenv("HF_ENDPOINT", apiServer.URL)
	clearMarketplaceCache()

	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/marketplace/datasets?artifact_source=huggingface&task=text-classification&language=en&license=apache-2.0", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceDatasets(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Data []csghub.Dataset `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].ArtifactSource != "huggingface" {
		t.Fatalf("datasets = %#v", response.Data)
	}
}

func TestHandleMarketplaceDatasetsCachesResponse(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/datasets" {
			t.Fatalf("path = %q, want /api/datasets", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
			"id": "acme/demo", "tags": []string{"license:apache-2.0"},
		}})
	}))
	defer apiServer.Close()
	t.Setenv("HF_ENDPOINT", apiServer.URL)
	clearMarketplaceCache()

	s := newTestServer(t)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet,
			"/api/marketplace/datasets?artifact_source=huggingface&search=cache-check", nil)
		w := httptest.NewRecorder()
		s.handleMarketplaceDatasets(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 body=%s", i, w.Code, w.Body.String())
		}
		want := "miss"
		if i > 0 {
			want = "fresh"
		}
		if got := w.Header().Get("X-CSGHUB-Lite-Cache"); got != want {
			t.Fatalf("request %d cache header = %q, want %q", i, got, want)
		}
	}
}

func TestHandleMarketplaceDatasetsPreservesRegistryAuthStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(status), status)
			}))
			defer apiServer.Close()
			t.Setenv("HF_ENDPOINT", apiServer.URL)
			clearMarketplaceCache()

			s := newTestServer(t)
			req := httptest.NewRequest(http.MethodGet,
				"/api/marketplace/datasets?artifact_source=huggingface&search=auth-"+strconv.Itoa(status), nil)
			w := httptest.NewRecorder()
			s.handleMarketplaceDatasets(w, req)
			if w.Code != status {
				t.Fatalf("status = %d, want %d body=%s", w.Code, status, w.Body.String())
			}
		})
	}
}

func TestHandleMarketplaceDatasetDetailReportsSourceScopedLocalStatus(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datasets/acme/demo/revision/main":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "acme/demo", "sha": "abc123", "lastModified": "2026-08-20T00:00:00Z",
			})
		case "/api/datasets/acme/demo/tree/main":
			w.Header().Set("X-Repo-Commit", "abc123")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"type": "file", "path": "README.md", "size": 12},
				{"type": "file", "path": "data/train.jsonl", "size": 88},
				{"type": "directory", "path": "data", "size": 0},
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer apiServer.Close()
	t.Setenv("HF_ENDPOINT", apiServer.URL)

	s := newTestServer(t)
	local := &dataset.LocalDataset{
		Namespace: "acme", Name: "demo", ArtifactSource: "huggingface",
		Repository: "acme/demo", RequestedRevision: "main", ResolvedRevision: "abc123",
		DownloadedAt: time.Now(),
	}
	if err := dataset.SaveManifestInDir(
		dataset.RegistryDatasetDir(s.cfg.DatasetDir, "huggingface", "acme", "demo"), local,
	); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet,
		"/api/marketplace/datasets/acme/demo?artifact_source=huggingface&revision=main", nil)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("name", "demo")
	w := httptest.NewRecorder()
	s.handleMarketplaceDatasetDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
	var response marketplaceDatasetDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Details == nil || response.Details.ArtifactSource != "huggingface" ||
		response.Details.Path != "acme/demo" {
		t.Fatalf("details = %#v", response.Details)
	}
	if response.Details.RepoSize != 0 || response.Details.FileCount != 0 {
		t.Fatalf("primary detail unexpectedly blocked on repository metrics: size %d files %d", response.Details.RepoSize, response.Details.FileCount)
	}
	if !response.LocalDataset.Downloaded || response.LocalDataset.Dataset != "huggingface/acme/demo" {
		t.Fatalf("local_dataset = %#v", response.LocalDataset)
	}

	extrasReq := httptest.NewRequest(http.MethodGet,
		"/api/marketplace/datasets/acme/demo/extras?artifact_source=huggingface&revision=main", nil)
	extrasReq.SetPathValue("namespace", "acme")
	extrasReq.SetPathValue("name", "demo")
	extrasW := httptest.NewRecorder()
	s.handleMarketplaceDatasetExtras(extrasW, extrasReq)
	if extrasW.Code != http.StatusOK {
		t.Fatalf("extras status = %d, want 200 body=%s", extrasW.Code, extrasW.Body.String())
	}
	var extras marketplaceDatasetExtrasResponse
	if err := json.NewDecoder(extrasW.Body).Decode(&extras); err != nil {
		t.Fatal(err)
	}
	if !extras.Available || extras.RepoSize != 100 || extras.FileCount != 2 || extras.Revision != "abc123" {
		t.Fatalf("extras = %#v", extras)
	}
}

func TestHandleMarketplaceModelsDoesNotBlockOnRepositorySizes(t *testing.T) {
	extrasCalls := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
				Msg: "OK",
				Data: []csghub.Model{
					{ID: 1, Path: "ns/model-a", RepositoryID: 101},
					{ID: 2, Path: "ns/model-b", RepositoryID: 202},
				},
				Total: 2,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/extra":
			extrasCalls++
			t.Fatal("model list must not request repository extras")
		default:
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?page=71", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Data []csghub.Model `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("data len = %d, want 2", len(resp.Data))
	}
	if extrasCalls != 0 {
		t.Fatalf("extras calls = %d, want 0", extrasCalls)
	}
	if resp.Data[0].RepoSize != 0 || resp.Data[1].RepoSize != 0 {
		t.Fatalf("repo sizes = [%d %d], want [0 0]", resp.Data[0].RepoSize, resp.Data[1].RepoSize)
	}
}

func TestHandleMarketplaceModelExtrasFetchesRepositorySizes(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/extra" {
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			RepoIDs []int `json:"repo_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode extras request: %v", err)
		}
		if len(req.RepoIDs) != 2 || req.RepoIDs[0] != 101 || req.RepoIDs[1] != 202 {
			t.Fatalf("repo_ids = %#v, want [101 202]", req.RepoIDs)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoExtraItem]{
			Msg: "OK",
			Data: []csghub.RepoExtraItem{
				{RepoID: 101, Size: 7_516_192_768},
				{RepoID: 202, Size: 2_147_483_648},
			},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodPost, "/api/marketplace/models/extra", bytes.NewBufferString(`{"repo_ids":[101,202,101,-1]}`))
	w := httptest.NewRecorder()
	s.handleMarketplaceModelExtras(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp struct {
		Data []csghub.RepoExtraItem `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 2 || resp.Data[0].Size != 7_516_192_768 || resp.Data[1].Size != 2_147_483_648 {
		t.Fatalf("extras = %#v", resp.Data)
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

	// Age the entry past the fresh TTL so the second request actually reaches the
	// upstream and falls back to the stale copy when it is rate limited.
	cacheKey := marketplaceCacheKey("models", "page=9")
	marketplaceListCache.Lock()
	entry := marketplaceListCache.entries[cacheKey]
	entry.updatedAt = time.Now().Add(-marketplaceListCacheTTL - time.Minute)
	marketplaceListCache.entries[cacheKey] = entry
	marketplaceListCache.Unlock()

	req = httptest.NewRequest(http.MethodGet, "/api/marketplace/models?page=9", nil)
	w = httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second status = %d, want cached %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Header().Get("X-CSGHUB-Lite-Cache"); got != "stale" {
		t.Fatalf("cache header = %q, want stale", got)
	}
	if requests != 2 {
		t.Fatalf("upstream requests = %d, want 2 (the stale fallback must still call the upstream)", requests)
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
		if got := r.URL.Query()["tag_category"]; !reflect.DeepEqual(got, []string{"task", "framework"}) {
			t.Fatalf("tag_category = %q, want [task framework]", got)
		}
		if got := r.URL.Query()["tag_name"]; !reflect.DeepEqual(got, []string{"text-generation", "gguf"}) {
			t.Fatalf("tag_name = %q, want [text-generation gguf]", got)
		}

		// An upstream that ignores the framework pair still gets filtered locally.
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

// CSGHub filters by task and framework together when both pairs are sent, so
// the combined listing pages through the upstream total instead of the few
// pages a local scan of task results used to reach (issue: GGUF + text
// generation stopped after 3 pages of 1256 models).
func TestHandleMarketplaceModelsPagesTaskAndFrameworkUpstream(t *testing.T) {
	const upstreamTotal = 1256
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if !reflect.DeepEqual(q["tag_category"], []string{"task", "framework"}) || !reflect.DeepEqual(q["tag_name"], []string{"text-generation", "gguf"}) {
			t.Fatalf("tags = %q / %q, want task+framework pairs", q["tag_category"], q["tag_name"])
		}
		if got := q.Get("page"); got != "5" {
			t.Fatalf("upstream page = %q, want 5", got)
		}
		models := make([]csghub.Model, 16)
		for i := range models {
			models[i] = csghub.Model{
				ID:   i + 1,
				Path: fmt.Sprintf("ns/gguf-%d", i),
				Tags: []csghub.Tag{
					{Name: "gguf", Category: "framework"},
					{Name: "text-generation", Category: "task"},
				},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{Msg: "OK", Data: models, Total: upstreamTotal})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL
	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?framework=gguf&task=text-generation&page=5&per=16", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data       []csghub.Model `json:"data"`
		Total      int            `json:"total"`
		HasMore    bool           `json:"has_more"`
		TotalExact bool           `json:"total_exact"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 16 || resp.Total != upstreamTotal || !resp.HasMore || !resp.TotalExact {
		t.Fatalf("response = %d items, total %d, has_more %v, exact %v; want 16, %d, true, true", len(resp.Data), resp.Total, resp.HasMore, resp.TotalExact, upstreamTotal)
	}
}

// When the upstream ignores the framework filter, the local fallback scans a
// bounded number of upstream pages. Past what it scanned it must not report an
// exact total or a next page that will come back empty.
func TestHandleMarketplaceModelsFallbackPastScannedPagesIsNotExact(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		per, _ := strconv.Atoi(r.URL.Query().Get("per"))
		models := make([]csghub.Model, per)
		for i := range models {
			// One GGUF model per upstream page; the rest only match the task.
			framework := "safetensors"
			if i == 0 {
				framework = "gguf"
			}
			models[i] = csghub.Model{
				ID:   i + 1,
				Path: fmt.Sprintf("ns/m-%s-%d", r.URL.Query().Get("page"), i),
				Tags: []csghub.Tag{{Name: framework, Category: "framework"}, {Name: "text-generation", Category: "task"}},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{Msg: "OK", Data: models, Total: 100000})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL
	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?framework=gguf&task=text-generation&page=4&per=16", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data       []csghub.Model `json:"data"`
		HasMore    bool           `json:"has_more"`
		TotalExact bool           `json:"total_exact"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalExact || resp.HasMore || len(resp.Data) != 0 {
		t.Fatalf("response = %d items, has_more %v, exact %v; want 0, false, false", len(resp.Data), resp.HasMore, resp.TotalExact)
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
					{Path: "Q8_0/Qwen3-Q8_0-00001-of-00002.gguf", Name: "Qwen3-Q8_0-00001-of-00002.gguf", Size: 4 << 30},
					{Path: "Q8_0/Qwen3-Q8_0-00002-of-00002.gguf", Name: "Qwen3-Q8_0-00002-of-00002.gguf", Size: 4 << 30},
					{Path: "Q4_K_M/Qwen3-Q4_K_M-00001-of-00002.gguf", Name: "Qwen3-Q4_K_M-00001-of-00002.gguf", Size: 2 << 30},
					{Path: "Q4_K_M/Qwen3-Q4_K_M-00002-of-00002.gguf", Name: "Qwen3-Q4_K_M-00002-of-00002.gguf", Size: 2 << 30},
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
	if resp.Quantizations[0].Size != 8<<30 || resp.Quantizations[1].Size != 4<<30 {
		t.Fatalf("split GGUF sizes = %#v; want 8 GiB and 4 GiB", resp.Quantizations)
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
		case "/api/v1/models/openai/openai_whisper-large-v3/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{Msg: "OK"})
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

func TestHandleMarketplaceModelDetailRejectsQuantizedWeights(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/Qwen/Qwen2.5-72B-Instruct-AWQ":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:   1,
					Name: "Qwen2.5-72B-Instruct-AWQ",
					Path: "Qwen/Qwen2.5-72B-Instruct-AWQ",
					Tags: []csghub.Tag{
						{Name: "safetensors", Category: "framework", ShowName: "SafeTensors"},
						{Name: "text-generation", Category: "task", ShowName: "Text Generation"},
					},
					Metadata: csghub.ModelMetadata{Architecture: "Qwen2ForCausalLM"},
				},
			})
		case "/api/v1/models/Qwen/Qwen2.5-72B-Instruct-AWQ/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{Msg: "OK"})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/Qwen/Qwen2.5-72B-Instruct-AWQ", nil)
	req.SetPathValue("namespace", "Qwen")
	req.SetPathValue("name", "Qwen2.5-72B-Instruct-AWQ")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.LocalInference.Supported {
		t.Fatalf("local_inference = %#v, want unsupported for AWQ weights", resp.LocalInference)
	}
}

func TestHandleMarketplaceModelDetailKeepsNativeMLXImageSupport(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/mlx-community/Qwen-Image-2.1-mflux-q4":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:   1,
					Name: "Qwen-Image-2.1-mflux-q4",
					Path: "mlx-community/Qwen-Image-2.1-mflux-q4",
					Tags: []csghub.Tag{
						{Name: "image-to-image", Category: "task"},
						{Name: "mflux", Category: "task"},
						{Name: "mlx", Category: "task"},
						{Name: "qwen-image", Category: "task"},
						{Name: "text-to-image", Category: "task"},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/mlx-community/Qwen-Image-2.1-mflux-q4/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{Msg: "OK"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/extra":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoExtraItem]{Msg: "OK"})
		default:
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL
	s.localInferenceLimits = func() marketplaceLocalInferenceLimits {
		return marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 64 << 30}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/mlx-community/Qwen-Image-2.1-mflux-q4", nil)
	req.SetPathValue("namespace", "mlx-community")
	req.SetPathValue("name", "Qwen-Image-2.1-mflux-q4")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		if !resp.LocalInference.Supported || resp.LocalInference.Runtime != "mlx-image" || resp.LocalInference.Mode != "image" {
			t.Fatalf("local_inference = %#v, want mlx-image", resp.LocalInference)
		}
		return
	}
	if resp.LocalInference.Supported {
		t.Fatalf("local_inference = %#v, want unsupported outside darwin/arm64", resp.LocalInference)
	}
}

func TestHandleMarketplaceModelDetailUsesRoutedTaskTag(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/acme/sd-xl":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:   1,
					Name: "sd-xl",
					Path: "acme/sd-xl",
					Tags: []csghub.Tag{
						{Name: "safetensors", Category: "framework", ShowName: "SafeTensors"},
						{Name: "text-classification", Category: "task", ShowName: "Text Classification"},
						{Name: "text-to-image", Category: "task", ShowName: "Text to Image"},
					},
				},
			})
		case "/api/v1/models/acme/sd-xl/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{Msg: "OK"})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/acme/sd-xl", nil)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("name", "sd-xl")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.LocalInference.Supported || resp.LocalInference.Runtime != "diffusers" || resp.LocalInference.Mode != "image" {
		t.Fatalf("local_inference = %#v, want diffusers image (routed task must not use the first task tag)", resp.LocalInference)
	}
}

func TestHandleMarketplaceModelDetailPrefersGGUFOverFirstFrameworkTag(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/acme/mixed-weights":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:   1,
					Name: "mixed-weights",
					Path: "acme/mixed-weights",
					Tags: []csghub.Tag{
						{Name: "safetensors", Category: "framework", ShowName: "SafeTensors"},
						{Name: "gguf", Category: "framework", ShowName: "GGUF"},
						{Name: "image-text-to-text", Category: "task", ShowName: "Image-Text-to-Text"},
					},
				},
			})
		case "/api/v1/models/acme/mixed-weights/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{Msg: "OK"})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/acme/mixed-weights", nil)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("name", "mixed-weights")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.LocalInference.Supported || resp.LocalInference.Runtime != "llama" || resp.LocalInference.Mode != "direct" {
		t.Fatalf("local_inference = %#v, want llama direct (gguf tag must win over the first framework tag)", resp.LocalInference)
	}
}

func TestHandleMarketplaceModelDetailMixedTagsForModelScope(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openapi/v1/models/acme/mixed-weights":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"id":   "acme/mixed-weights",
					"tags": []string{"library:safetensors", "library:gguf", "task:image-text-to-text"},
				},
			})
		case "/api/v1/models/acme/mixed-weights/repo/files":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"Success": true,
				"Data":    map[string]interface{}{"Files": []interface{}{}},
			})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	t.Setenv(config.EnvModelScopeEndpoint, apiServer.URL)

	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/acme/mixed-weights?artifact_source=modelscope", nil)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("name", "mixed-weights")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.LocalInference.Supported || resp.LocalInference.Runtime != "llama" || resp.LocalInference.Mode != "direct" {
		t.Fatalf("local_inference = %#v, want llama direct (library:gguf must win over library:safetensors)", resp.LocalInference)
	}
}

func TestHandleMarketplaceModelDetailMixedTagsForHuggingFace(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/acme/mixed-weights/revision/main":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":           "acme/mixed-weights",
				"modelId":      "acme/mixed-weights",
				"tags":         []string{"safetensors", "gguf"},
				"pipeline_tag": "image-text-to-text",
			})
		case "/api/models/acme/mixed-weights/tree/main":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]interface{}{})
		case "/acme/mixed-weights/resolve/main/README.md":
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	t.Setenv(config.EnvHuggingFaceEndpoint, apiServer.URL)

	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/acme/mixed-weights?artifact_source=huggingface", nil)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("name", "mixed-weights")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.LocalInference.Supported || resp.LocalInference.Runtime != "llama" || resp.LocalInference.Mode != "direct" {
		t.Fatalf("local_inference = %#v, want llama direct (the bare gguf tag must win over safetensors)", resp.LocalInference)
	}
}

func TestHandleMarketplaceModelDetailFallsBackToFileInferredFormat(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/acme/onnx-weights":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:   1,
					Name: "onnx-weights",
					Path: "acme/onnx-weights",
					Tags: []csghub.Tag{
						{Name: "onnx", Category: "framework", ShowName: "ONNX"},
					},
				},
			})
		case "/api/v1/models/acme/onnx-weights/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{
				Msg: "OK",
				Data: []csghub.RepoFile{
					{Path: "config.json", Name: "config.json"},
					{Path: "model.safetensors", Name: "model.safetensors", LFS: true},
				},
			})
		case "/api/v1/models/acme/onnx-weights/raw/config.json":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[string]{
				Msg:  "OK",
				Data: `{"architectures":["LlamaForCausalLM"],"model_type":"llama"}`,
			})
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/acme/onnx-weights", nil)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("name", "onnx-weights")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Tags carry no usable framework, so the verdict falls back to the format
	// inferred from the repository files.
	if !resp.LocalInference.Supported || resp.LocalInference.Runtime != "llama" || resp.LocalInference.Mode != "convert" {
		t.Fatalf("local_inference = %#v, want llama convert from the file-inferred format", resp.LocalInference)
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

func TestHandleMarketplaceModelDetailRejectsOversizedConvertModel(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/Qwen/Qwen3.8-2.4T-A95B":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:           244357,
					Name:         "Qwen3.8-2.4T-A95B",
					Path:         "Qwen/Qwen3.8-2.4T-A95B",
					RepositoryID: 328040,
					Tags: []csghub.Tag{
						{Name: "safetensors", Category: "framework"},
						{Name: "text-generation", Category: "task"},
					},
					Metadata: csghub.ModelMetadata{Architecture: "Qwen3_5MoeForCausalLM"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/extra":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoExtraItem]{
				Msg:  "OK",
				Data: []csghub.RepoExtraItem{{RepoID: 328040, Size: 4_892_388_876_361}},
			})
		default:
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL
	s.localInferenceLimits = func() marketplaceLocalInferenceLimits {
		return marketplaceLocalInferenceLimits{maxDownloadBytes: 200 << 30, maxMemoryBytes: 64 << 30}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/Qwen/Qwen3.8-2.4T-A95B", nil)
	req.SetPathValue("namespace", "Qwen")
	req.SetPathValue("name", "Qwen3.8-2.4T-A95B")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Details == nil || resp.Details.RepoSize != 4_892_388_876_361 {
		t.Fatalf("details = %#v, want the enriched 4.4 TiB repo size", resp.Details)
	}
	if resp.LocalInference.Supported || resp.LocalInference.Mode != "none" {
		t.Fatalf("local_inference = %#v, want unsupported for an oversized convert-only repo", resp.LocalInference)
	}
	if resp.LocalInference.Architecture != "Qwen3_5MoeForCausalLM" {
		t.Fatalf("architecture = %q, want the probed architecture kept for display", resp.LocalInference.Architecture)
	}
}

func TestHandleMarketplaceModelDetailUsesTreeSizeWhenExtrasIsEmpty(t *testing.T) {
	const (
		gib   = int64(1) << 30
		total = 300 * gib
	)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/acme/big":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.Model]{
				Msg: "OK",
				Data: csghub.Model{
					ID:           1,
					Name:         "big",
					Path:         "acme/big",
					RepositoryID: 7,
					Tags: []csghub.Tag{
						{Name: "safetensors", Category: "framework"},
						{Name: "text-generation", Category: "task"},
					},
					Metadata: csghub.ModelMetadata{Architecture: "LlamaForCausalLM"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/extra":
			// The extras endpoint reports nothing for this mirror, so the detail
			// page must fall back to the file tree like the list does.
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoExtraItem]{Msg: "OK"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/acme/big/tree":
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[[]csghub.RepoFile]{
				Msg: "OK",
				Data: []csghub.RepoFile{
					{Path: "model-00001.safetensors", Name: "model-00001.safetensors", Type: "file", Size: total / 2},
					{Path: "model-00002.safetensors", Name: "model-00002.safetensors", Type: "file", Size: total / 2},
				},
			})
		default:
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL
	s.localInferenceLimits = func() marketplaceLocalInferenceLimits {
		return marketplaceLocalInferenceLimits{maxDownloadBytes: 100 * gib, maxMemoryBytes: 64 * gib}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/acme/big", nil)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("name", "big")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp marketplaceModelDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Details == nil || resp.Details.RepoSize != total {
		t.Fatalf("details = %#v, want the tree size %d", resp.Details, total)
	}
	if resp.LocalInference.Supported || resp.LocalInference.Mode != "none" {
		t.Fatalf("local_inference = %#v, want unsupported for a repository that does not fit", resp.LocalInference)
	}
}

func TestHandleMarketplaceModelDetailMapsUpstreamRateLimit(t *testing.T) {
	var calls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/api/models/acme/demo/revision/main" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"quota exceeded","retryAfter":22}`))
	}))
	defer apiServer.Close()
	t.Setenv("HF_ENDPOINT", apiServer.URL)

	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models/acme/demo?artifact_source=huggingface", nil)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("name", "demo")
	w := httptest.NewRecorder()

	s.handleMarketplaceModelDetail(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusTooManyRequests, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "22" {
		t.Fatalf("Retry-After = %q, want 22", got)
	}
	if body := w.Body.String(); !strings.Contains(body, "retry in about 22s") {
		t.Fatalf("body = %q, want the retry hint", body)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream requests = %d, want 1 (a 22s quota window must not block)", got)
	}
}

func TestHandleMarketplaceModelsRejectsLocalInferenceForProviderSources(t *testing.T) {
	var calls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/api/models" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
			"id":      "acme/demo",
			"modelId": "acme/demo",
			"tags":    []string{"gguf"},
		}})
	}))
	defer apiServer.Close()
	t.Setenv("HF_ENDPOINT", apiServer.URL)

	s := newTestServer(t)
	// local_inference is only supported for OpenCSG. Provider sources reject it
	// instead of falling back to the scan, which would hammer the upstream.
	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?artifact_source=huggingface&local_inference=true&per=100", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("upstream requests = %d, want 0", got)
	}
}

func TestHandleMarketplaceModelsUsesLocalModelFilterEndpoint(t *testing.T) {
	clearMarketplaceCache()
	var listCalls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/local_filter" {
			var request csghub.LocalModelFilterRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if request.Page != 1 || request.Per != 16 {
				t.Fatalf("page/per = %d/%d, want 1/16", request.Page, request.Per)
			}
			if len(request.Capabilities.Convert.Architectures) == 0 {
				t.Fatal("capabilities.convert.architectures is empty")
			}
			if len(request.ExcludePaths) != 1 || request.ExcludePaths[0] != "acme/seen" {
				t.Fatalf("exclude paths = %v, want [acme/seen]", request.ExcludePaths)
			}
			_ = json.NewEncoder(w).Encode(csghub.APIResponse[csghub.LocalModelFilterResponse]{
				Msg: "OK",
				Data: csghub.LocalModelFilterResponse{
					Data: []csghub.Model{{
						Name: "demo",
						Path: "acme/demo",
						Tags: []csghub.Tag{
							{Name: "gguf", Category: "framework"},
							{Name: "text-generation", Category: "task"},
						},
					}},
					TotalConfirmed:      1,
					TotalExact:          true,
					HasMore:             true,
					CandidatesQueried:   true,
					LocalCheckExhausted: true,
				},
			})
			return
		}
		atomic.AddInt32(&listCalls, 1)
		t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true&exclude_paths=acme/seen", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&listCalls); got != 0 {
		t.Fatalf("scan list calls = %d, want 0", got)
	}
	var response struct {
		Data         []csghub.Model `json:"data"`
		Total        int            `json:"total"`
		HasMore      bool           `json:"has_more"`
		TotalExact   bool           `json:"total_exact"`
		ExcludePaths []string       `json:"exclude_paths"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Path != "acme/demo" {
		t.Fatalf("models = %#v", response.Data)
	}
	if !response.HasMore || !response.TotalExact {
		t.Fatalf("has_more=%v total_exact=%v, want the endpoint values", response.HasMore, response.TotalExact)
	}
	if len(response.ExcludePaths) != 1 || response.ExcludePaths[0] != "acme/seen" {
		t.Fatalf("exclude paths = %v, want the cursor echoed", response.ExcludePaths)
	}
}

func TestHandleMarketplaceModelsFallsBackWhenLocalModelFilterMissing(t *testing.T) {
	clearMarketplaceCache()
	var listCalls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/local_filter" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/models" {
			atomic.AddInt32(&listCalls, 1)
			_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
				Data: []csghub.Model{{
					Name: "demo",
					Path: "acme/demo",
					Tags: []csghub.Tag{
						{Name: "gguf", Category: "framework"},
						{Name: "text-generation", Category: "task"},
					},
				}},
				Total: 1,
			})
			return
		}
		t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&listCalls); got == 0 {
		t.Fatal("scan list was not called after the endpoint 404")
	}
	var response struct {
		Data []csghub.Model `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Path != "acme/demo" {
		t.Fatalf("models = %#v", response.Data)
	}
}

func TestHandleMarketplaceModelsSkipsProbeAfterEndpointMissing(t *testing.T) {
	clearMarketplaceCache()
	var probeCalls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/local_filter" {
			atomic.AddInt32(&probeCalls, 1)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/models" {
			_ = json.NewEncoder(w).Encode(csghub.ListResponse[csghub.Model]{
				Data: []csghub.Model{{
					Name: "demo",
					Path: "acme/demo",
					Tags: []csghub.Tag{
						{Name: "gguf", Category: "framework"},
						{Name: "text-generation", Category: "task"},
					},
				}},
				Total: 1,
			})
			return
		}
		t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	request := func(page int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/marketplace/models?local_inference=true&page=%d", page), nil)
		w := httptest.NewRecorder()
		s.handleMarketplaceModels(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page %d status = %d, want 200 body=%s", page, w.Code, w.Body.String())
		}
	}

	request(1)
	if got := atomic.LoadInt32(&probeCalls); got != 1 {
		t.Fatalf("probe calls after first page = %d, want 1", got)
	}
	request(2)
	if got := atomic.LoadInt32(&probeCalls); got != 1 {
		t.Fatalf("probe calls after cached miss = %d, want 1", got)
	}

	s.localFilterEndpointMu.Lock()
	s.localFilterEndpointUnavailableAt = time.Now().Add(-marketplaceLocalFilterEndpointTTL - time.Minute)
	s.localFilterEndpointMu.Unlock()
	request(3)
	if got := atomic.LoadInt32(&probeCalls); got != 2 {
		t.Fatalf("probe calls after TTL = %d, want 2", got)
	}
}

func TestHandleMarketplaceModelsDoesNotFallBackOnRateLimit(t *testing.T) {
	clearMarketplaceCache()
	var listCalls int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/local_filter" {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		atomic.AddInt32(&listCalls, 1)
		t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL

	req := httptest.NewRequest(http.MethodGet, "/api/marketplace/models?local_inference=true", nil)
	w := httptest.NewRecorder()
	s.handleMarketplaceModels(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&listCalls); got != 0 {
		t.Fatalf("scan list calls = %d, want 0", got)
	}
}

func TestMarketplaceListCacheStaysBounded(t *testing.T) {
	clearMarketplaceCache()
	t.Cleanup(clearMarketplaceCache)
	for i := 0; i < marketplaceListCacheMaxEntries+50; i++ {
		setMarketplaceCache("models?test="+strconv.Itoa(i), []byte("{}"))
	}
	marketplaceListCache.Lock()
	size := len(marketplaceListCache.entries)
	marketplaceListCache.Unlock()
	if size > marketplaceListCacheMaxEntries {
		t.Fatalf("cache entries = %d, want at most %d", size, marketplaceListCacheMaxEntries)
	}
}
