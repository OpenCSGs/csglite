package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func newCloudModelListServer(requests *int, currentModel *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*requests)++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":           *currentModel,
					"task":         "text-generation",
					"display_name": *currentModel,
				},
			},
		})
	}))
}

func TestHandleTagsWithoutTokenIncludesAndRefreshesCloudModels(t *testing.T) {
	requests := 0
	currentModel := "stale/model"
	apiServer := newCloudModelListServer(&requests, &currentModel)
	defer apiServer.Close()

	s := newTestServer(t)
	s.cloud = cloud.NewService(apiServer.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}

	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tags response: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "stale/model" {
		t.Fatalf("models = %#v, want stale/model", resp.Models)
	}

	currentModel = "fresh/model"
	req = httptest.NewRequest(http.MethodGet, "/api/tags?refresh=1", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want %d", w.Code, http.StatusOK)
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode refreshed tags response: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "fresh/model" {
		t.Fatalf("refreshed models = %#v, want fresh/model", resp.Models)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestHandleTagsProviderFilterCloudModels(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":           "xiaomi/model",
					"task":         "text-generation",
					"display_name": "Xiaomi Model",
					"owned_by":     "xiaomi",
				},
				{
					"id":           "opencsg/model",
					"task":         "text-generation",
					"display_name": "OpenCSG Model",
					"owned_by":     "OpenCSG",
				},
			},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cloud = cloud.NewService(apiServer.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/tags?provider=csghub", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tags response: %v", err)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("models = %#v, want both cloud models under csghub", resp.Models)
	}
	for _, model := range resp.Models {
		if model.Provider != "csghub" {
			t.Fatalf("model provider = %q, want csghub", model.Provider)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("xiaomi status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode xiaomi tags response: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Fatalf("models = %#v, want no CSGHub cloud models for xiaomi provider", resp.Models)
	}
}

func TestHandleModelProvidersListIncludesModelProviders(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":           "xiaomi/one",
					"task":         "text-generation",
					"display_name": "Xiaomi One",
					"owned_by":     "xiaomi",
				},
				{
					"id":           "xiaomi/two",
					"task":         "text-generation",
					"display_name": "Xiaomi Two",
					"owned_by":     "xiaomi",
				},
				{
					"id":           "opencsg/model",
					"task":         "text-generation",
					"display_name": "OpenCSG Model",
					"owned_by":     "OpenCSG",
				},
			},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cloud = cloud.NewService(apiServer.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/providers?source=model", nil)
	w := httptest.NewRecorder()
	s.handleProvidersList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	var resp api.ModelProvidersResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode providers response: %v", err)
	}
	counts := map[string]int{}
	for _, provider := range resp.Providers {
		counts[provider.ID] = provider.ModelCount
	}
	if len(counts) != 1 || counts["csghub"] != 3 {
		t.Fatalf("model provider counts = %#v, want csghub=3", counts)
	}
}

func TestRefreshCloudChatModelsWithoutTokenThrottlesRepeatedForceRefresh(t *testing.T) {
	requests := 0
	currentModel := "stale/model"
	apiServer := newCloudModelListServer(&requests, &currentModel)
	defer apiServer.Close()

	s := newTestServer(t)
	s.cloud = cloud.NewService(apiServer.URL)

	models, err := s.refreshCloudChatModels(context.Background())
	if err != nil {
		t.Fatalf("refreshCloudChatModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "stale/model" {
		t.Fatalf("models = %#v, want stale/model", models)
	}

	currentModel = "fresh/model"
	models, err = s.refreshCloudChatModels(context.Background())
	if err != nil {
		t.Fatalf("second refreshCloudChatModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "stale/model" {
		t.Fatalf("throttled models = %#v, want stale/model", models)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 throttled request", requests)
	}
}

func TestResolveAIAppLaunchModelsRefreshesRequestedCloudModelAfterCacheMiss(t *testing.T) {
	requests := 0
	currentModel := "stale/model"
	apiServer := newCloudModelListServer(&requests, &currentModel)
	defer apiServer.Close()

	s := New(&config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
		Token:      "test-token",
	}, "test")
	s.cloud = cloud.NewService(apiServer.URL)

	if _, err := s.refreshCloudChatModels(context.Background()); err != nil {
		t.Fatalf("initial refreshCloudChatModels returned error: %v", err)
	}

	currentModel = "fresh/model"
	modelID, modelIDs, err := s.resolveAIAppLaunchModels(context.Background(), "fresh/model", "")
	if err != nil {
		t.Fatalf("resolveAIAppLaunchModels returned error: %v", err)
	}
	if modelID != "fresh/model" {
		t.Fatalf("modelID = %q, want fresh/model", modelID)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 with cache-miss retry", requests)
	}
	if !containsModelID(modelIDs, "fresh/model") {
		t.Fatalf("modelIDs = %#v, want fresh/model after retry", modelIDs)
	}
}

func TestGetChatEngineRefreshesCloudModelsAfterCacheMiss(t *testing.T) {
	requests := 0
	currentModel := "stale/model"
	apiServer := newCloudModelListServer(&requests, &currentModel)
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.Token = "test-token"
	s.cloud = cloud.NewService(apiServer.URL)

	if _, err := s.refreshCloudChatModels(context.Background()); err != nil {
		t.Fatalf("initial refreshCloudChatModels returned error: %v", err)
	}

	currentModel = "fresh/model"
	eng, err := s.getChatEngine(context.Background(), "fresh/model", "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatalf("getChatEngine returned error: %v", err)
	}
	if eng == nil {
		t.Fatal("getChatEngine returned nil engine")
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 with cache-miss retry", requests)
	}
}

func TestHandleCloudAuthTokenSaveInvalidatesCloudModelCache(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	authServer := newCloudAuthAPIServer(t, "test-token", "alice")
	defer authServer.Close()

	requests := 0
	currentModel := "stale/model"
	modelServer := newCloudModelListServer(&requests, &currentModel)
	defer modelServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = authServer.URL
	s.cloud = cloud.NewService(modelServer.URL)

	models, err := s.cloud.ListChatModels(context.Background())
	if err != nil {
		t.Fatalf("initial ListChatModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "stale/model" {
		t.Fatalf("initial models = %#v, want stale/model", models)
	}

	currentModel = "fresh/model"
	req := httptest.NewRequest(http.MethodPost, "/api/cloud/auth/token", strings.NewReader(`{"token":" test-token "}`))
	w := httptest.NewRecorder()
	s.handleCloudAuthTokenSave(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d", w.Code, http.StatusOK)
	}

	models, err = s.cloud.ListChatModels(context.Background())
	if err != nil {
		t.Fatalf("ListChatModels after save returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "fresh/model" {
		t.Fatalf("models after save = %#v, want fresh/model", models)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 after cache invalidation", requests)
	}
}

func TestSortModelsByPriorityKeepsLocalModelsFirst(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "z/cloud-external", Source: "cloud", Format: "cloud", LLMType: "external_llm"},
		{Model: "z/local", Source: "local", Format: "gguf"},
		{Model: "a/cloud-opencsg", Source: "cloud", Format: "cloud", OwnedBy: "opencsg"},
		{Model: "a/local-safetensors", Format: "safetensors"},
		{Model: "b/cloud-other", Source: "cloud", Format: "cloud"},
	}

	sortModelsByPriority(models)

	got := make([]string, 0, len(models))
	for _, model := range models {
		got = append(got, model.Model)
	}
	want := []string{
		"a/local-safetensors",
		"z/local",
		"z/cloud-external",
		"a/cloud-opencsg",
		"b/cloud-other",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sorted models = %#v, want %#v", got, want)
	}
}

func containsModelID(modelIDs []string, want string) bool {
	for _, item := range modelIDs {
		if item == want {
			return true
		}
	}
	return false
}
