package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
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
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
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

func TestHandleTagsRefreshesCloudModelsWhenCacheIsEmpty(t *testing.T) {
	requests := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		data := []map[string]any{}
		if requests > 1 {
			data = append(data, map[string]any{
				"id":   "fallback/model",
				"task": "text-generation",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   data,
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cloud = cloud.NewService(apiServer.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tags response: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want initial empty fetch plus fallback refresh", requests)
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "fallback/model" {
		t.Fatalf("models = %#v, want fallback/model", resp.Models)
	}
}

func TestHandleTagsSendsLoginTokenToCloudGateway(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want %q", got, "Bearer access-token")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":   "auth/model",
					"task": "text-generation",
				},
			},
		})
	}))
	defer apiServer.Close()

	cfg := &config.Config{
		AIGatewayURL: apiServer.URL,
		ListenAddr:   ":0",
		ModelDir:     t.TempDir(),
		DatasetDir:   t.TempDir(),
		Token:        "access-token",
	}
	s := New(cfg, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/tags?refresh=1", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tags response: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "auth/model" {
		t.Fatalf("models = %#v, want auth/model", resp.Models)
	}
}

func TestHandleTagsIncludesSupportedCloudInferenceTasks(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "chat/model", "task": "text-generation"},
				{"id": "vision/model", "task": "image-text-to-text"},
				{"id": "image/model", "task": "text-to-image"},
				{"id": "edit/model", "task": "image-to-image"},
				{"id": "asr/model", "task": "speech-to-text"},
				{"id": "video/model", "task": "text-to-video"},
			},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cloud = cloud.NewService(apiServer.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/tags?refresh=1", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tags response: %v", err)
	}

	byID := map[string]api.ModelInfo{}
	for _, item := range resp.Models {
		byID[item.Model] = item
	}
	if _, ok := byID["video/model"]; ok {
		t.Fatalf("video/model should not be listed until text-to-video inference is supported: %#v", resp.Models)
	}
	for _, id := range []string{"chat/model", "vision/model", "image/model", "edit/model", "asr/model"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("model %q missing from /api/tags response: %#v", id, resp.Models)
		}
	}
	if got := byID["image/model"]; got.PipelineTag != "text-to-image" || !sameStrings(got.OutputModalities, []string{"image"}) {
		t.Fatalf("image metadata = %#v, want text-to-image output image", got)
	}
	if got := byID["edit/model"]; got.PipelineTag != "image-to-image" || !sameStrings(got.InputModalities, []string{"text", "image"}) || !sameStrings(got.OutputModalities, []string{"image"}) {
		t.Fatalf("edit metadata = %#v, want image-to-image text+image->image", got)
	}
	if got := byID["asr/model"]; got.PipelineTag != "automatic-speech-recognition" || !sameStrings(got.InputModalities, []string{"audio"}) || !sameStrings(got.OutputModalities, []string{"transcription"}) {
		t.Fatalf("asr metadata = %#v, want ASR audio->transcription", got)
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

func TestHandleModelProvidersListUsesConfiguredCloudProviderName(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
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
	s.cfg.CloudProviderName = "OpenCSG"
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
	if len(resp.Providers) != 1 {
		t.Fatalf("providers = %#v, want one cloud provider", resp.Providers)
	}
	if resp.Providers[0].ID != "opencsg" || resp.Providers[0].Name != "OpenCSG" || resp.Providers[0].ModelCount != 1 {
		t.Fatalf("provider = %#v, want configured OpenCSG provider", resp.Providers[0])
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

func TestResolveAIAppLaunchModelsPreservesCloudAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviderModelAllowlist)

	requests := 0
	currentModel := "glm-5.1"
	apiServer := newCloudModelListServer(&requests, &currentModel)
	defer apiServer.Close()

	s := New(&config.Config{
		ModelDir:             t.TempDir(),
		ListenAddr:           ":11435",
		Token:                "test-token",
		AIAppPreferredModels: map[string]string{},
	}, "test")
	s.cloud = cloud.NewService(apiServer.URL)
	if err := config.AddProviderModelSelection(config.DefaultCloudProviderName, config.ProviderModelSelection{
		Model:         "glm-5.1-1",
		OriginalModel: "glm-5.1",
	}); err != nil {
		t.Fatalf("save cloud alias: %v", err)
	}

	modelID, modelIDs, err := s.resolveAIAppLaunchModels(context.Background(), "glm-5.1-1", "")
	if err != nil {
		t.Fatalf("resolveAIAppLaunchModels returned error: %v", err)
	}
	if modelID != "glm-5.1-1" {
		t.Fatalf("modelID = %q, want cloud alias", modelID)
	}
	if !containsModelID(modelIDs, "glm-5.1-1") {
		t.Fatalf("modelIDs = %#v, want cloud alias", modelIDs)
	}

	s.savePreferredAIAppModel("open-code-review", modelID)
	if got := s.preferredAIAppModel("open-code-review"); got != "glm-5.1-1" {
		t.Fatalf("preferred model = %q, want cloud alias", got)
	}
}

func TestResolveAIAppLaunchModelsUsesSelectedProviderModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "selected/model"},
				{"id": "unselected/model"},
			},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:      "provider1",
		Name:    "OpenAI",
		BaseURL: apiServer.URL + "/v1",
		APIKey:  "secret",
		Enabled: true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}
	if err := config.ReplaceProviderModelAllowlist("provider1", []string{"selected/model"}); err != nil {
		t.Fatalf("save provider model allowlist: %v", err)
	}

	modelID, modelIDs, err := s.resolveAIAppLaunchModels(context.Background(), "", "")
	if err != nil {
		t.Fatalf("resolveAIAppLaunchModels returned error: %v", err)
	}
	if modelID != "selected/model" {
		t.Fatalf("default model = %q, want selected/model", modelID)
	}
	if !containsModelID(modelIDs, "selected/model") {
		t.Fatalf("modelIDs = %#v, want selected/model", modelIDs)
	}
	if containsModelID(modelIDs, "unselected/model") {
		t.Fatalf("modelIDs = %#v, want unselected/model excluded", modelIDs)
	}
}

func TestGetChatEngineRefreshesCloudModelsAfterCacheMiss(t *testing.T) {
	requests := 0
	currentModel := "stale/model"
	apiServer := newCloudModelListServer(&requests, &currentModel)
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.OpenCSGAPIKey = "test-api-key"
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
