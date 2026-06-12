package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestProviderCRUDDoesNotExposeAPIKey(t *testing.T) {
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
			"data": []map[string]string{{"id": "gpt-4o-mini"}},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	createReq := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader(`{
		"name": "OpenAI",
		"base_url": "`+apiServer.URL+`/v1",
		"api_key": "secret",
		"provider": "openai"
	}`))
	w := httptest.NewRecorder()
	s.handleProviderCreate(w, createReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("create response exposed API key: %s", w.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	w = httptest.NewRecorder()
	s.handleProvidersList(w, listReq)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("list response exposed API key: %s", w.Body.String())
	}
}

func TestHandleModelProvidersListLocalizesLocalProviderName(t *testing.T) {
	s := newTestServer(t)
	if err := model.SaveManifest(s.cfg.ModelDir, &model.LocalModel{
		Namespace:    "test",
		Name:         "model",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save local model: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers?source=model&locale=zh", nil)
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
		t.Fatalf("providers = %#v, want one local provider", resp.Providers)
	}
	if got := resp.Providers[0]; got.ID != "local" || got.Name != "本地" || got.Source != "local" || got.ModelCount != 1 {
		t.Fatalf("provider = %#v, want localized local provider", got)
	}
}

func TestProviderCreateRejectsInvalidConfigWithoutSaving(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader(`{
		"name": "Bad",
		"base_url": "`+apiServer.URL+`/v1",
		"api_key": "bad",
		"provider": "openai"
	}`))
	w := httptest.NewRecorder()
	s.handleProviderCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if providers := config.GetProviders(); len(providers) != 0 {
		t.Fatalf("providers saved despite invalid config: %#v", providers)
	}
}

func TestProviderCreateRejectsDuplicateName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:      "provider1",
		Name:    "Xiaomi Plan",
		BaseURL: "http://example.invalid/v1",
		APIKey:  "secret",
		Enabled: false,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader(`{
		"name": "xiaomi plan",
		"base_url": "http://example.invalid/v1",
		"api_key": "secret",
		"enabled": false
	}`))
	w := httptest.NewRecorder()
	s.handleProviderCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if providers := config.GetProviders(); len(providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(providers))
	}
}

func TestListOpenAICompatibleProviderModels(t *testing.T) {
	var authHeader string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "gpt-4o-mini"}},
		})
	}))
	defer apiServer.Close()

	models, err := listOpenAICompatibleProviderModels(context.Background(), config.ThirdPartyProvider{
		ID:      "provider1",
		Name:    "OpenAI",
		BaseURL: apiServer.URL + "/v1",
		APIKey:  "secret",
	})
	if err != nil {
		t.Fatalf("list models returned error: %v", err)
	}
	if authHeader != "Bearer secret" {
		t.Fatalf("Authorization header = %q, want bearer token", authHeader)
	}
	if len(models) != 1 || models[0].Model != "gpt-4o-mini" || models[0].Source != "provider:provider1" {
		t.Fatalf("models = %#v", models)
	}
	if models[0].DisplayName != "gpt-4o-mini [OpenAI]" {
		t.Fatalf("display name = %q, want provider label", models[0].DisplayName)
	}
	if models[0].Label != "gpt-4o-mini [OpenAI]" {
		t.Fatalf("label = %q, want provider label", models[0].Label)
	}
}

func TestInferThirdPartyPipelineFromOpenRouterArchitecture(t *testing.T) {
	cases := []struct {
		name       string
		item       thirdPartyProviderModel
		wantTag    string
		wantInput  string
		wantOutput string
	}{
		{
			name:       "text to image",
			item:       thirdPartyProviderModel{ID: "image", Architecture: &providerArchitecture{InputModalities: []string{"text"}, OutputModalities: []string{"image"}}},
			wantTag:    "text-to-image",
			wantInput:  "text",
			wantOutput: "image",
		},
		{
			name:       "image to video",
			item:       thirdPartyProviderModel{ID: "video", Architecture: &providerArchitecture{InputModalities: []string{"image"}, OutputModalities: []string{"video"}}},
			wantTag:    "image-to-video",
			wantInput:  "image",
			wantOutput: "video",
		},
		{
			name:       "audio speech",
			item:       thirdPartyProviderModel{ID: "speech", Architecture: &providerArchitecture{Modality: "text->audio"}},
			wantTag:    "text-to-speech",
			wantInput:  "text",
			wantOutput: "audio",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag, inputs, outputs := inferThirdPartyModelMetadata(config.ThirdPartyProvider{}, tc.item)
			if tag != tc.wantTag || !stringSliceContains(inputs, tc.wantInput) || !stringSliceContains(outputs, tc.wantOutput) {
				t.Fatalf("metadata = tag %q inputs %#v outputs %#v", tag, inputs, outputs)
			}
		})
	}
}

func TestInferThirdPartyPipelineFromLiteLLMRulesAndProviderHints(t *testing.T) {
	tag, inputs, outputs := inferThirdPartyModelMetadata(config.ThirdPartyProvider{Provider: "openai"}, thirdPartyProviderModel{ID: "dall-e-3"})
	if tag != "text-to-image" || !stringSliceContains(outputs, "image") {
		t.Fatalf("dall-e-3 metadata = tag %q inputs %#v outputs %#v", tag, inputs, outputs)
	}

	tag, inputs, outputs = inferThirdPartyModelMetadata(config.ThirdPartyProvider{Provider: "openai"}, thirdPartyProviderModel{ID: "gpt-4o"})
	if tag != "text-generation" || !stringSliceContains(inputs, "image") {
		t.Fatalf("gpt-4o metadata = tag %q inputs %#v outputs %#v", tag, inputs, outputs)
	}

	tag, inputs, outputs = inferThirdPartyModelMetadata(config.ThirdPartyProvider{Provider: "dashscope"}, thirdPartyProviderModel{ID: "qwen-image-2.0-pro-2026-04-22"})
	if tag != "text-to-image" || !stringSliceContains(outputs, "image") {
		t.Fatalf("dashscope qwen image metadata = tag %q inputs %#v outputs %#v", tag, inputs, outputs)
	}

	tag, inputs, outputs = inferThirdPartyModelMetadata(config.ThirdPartyProvider{}, thirdPartyProviderModel{ID: "xiaomi-tts-1"})
	if tag != "text-to-speech" || !stringSliceContains(outputs, "audio") {
		t.Fatalf("xiaomi tts metadata = tag %q inputs %#v outputs %#v", tag, inputs, outputs)
	}

	tag, inputs, outputs = inferThirdPartyModelMetadata(config.ThirdPartyProvider{}, thirdPartyProviderModel{ID: "kimi-video", SupportsVideoIn: true})
	if tag != "video-text-to-text" || !stringSliceContains(inputs, "video") {
		t.Fatalf("kimi video metadata = tag %q inputs %#v outputs %#v", tag, inputs, outputs)
	}

	tag, inputs, outputs = inferThirdPartyModelMetadata(config.ThirdPartyProvider{}, thirdPartyProviderModel{ID: "unknown-model"})
	if tag != "text-generation" || len(inputs) != 0 || !stringSliceContains(outputs, "text") {
		t.Fatalf("unknown metadata = tag %q inputs %#v outputs %#v", tag, inputs, outputs)
	}
}

func TestHandleTagsIncludesSelectedThirdPartyProviderModels(t *testing.T) {
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
			"data": []map[string]string{{"id": "gpt-4o-mini"}},
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

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Models []struct {
			Model       string `json:"model"`
			Source      string `json:"source"`
			Label       string `json:"label"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tags: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Fatalf("models before selection = %#v, want none", resp.Models)
	}

	if err := config.ReplaceProviderModelAllowlist("provider1", []string{"gpt-4o-mini"}); err != nil {
		t.Fatalf("save provider model allowlist: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("selected status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode selected tags: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "gpt-4o-mini" || resp.Models[0].Source != "provider:provider1" {
		t.Fatalf("models = %#v", resp.Models)
	}
	if resp.Models[0].DisplayName != "gpt-4o-mini [OpenAI]" {
		t.Fatalf("display name = %q, want provider label", resp.Models[0].DisplayName)
	}
	if resp.Models[0].Label != "gpt-4o-mini [OpenAI]" {
		t.Fatalf("label = %q, want provider label in tags response", resp.Models[0].Label)
	}
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestThirdPartyModelProviderUsesStableProviderID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	providerModelRequests := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerModelRequests++
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "mi-model"}},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:       "provider1",
		Name:     "xiaomi-plan",
		BaseURL:  apiServer.URL + "/v1",
		APIKey:   "secret",
		Provider: "openai",
		Enabled:  true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	if err := config.ReplaceProviderModelAllowlist("provider1", []string{"mi-model"}); err != nil {
		t.Fatalf("save provider model allowlist: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers?source=model", nil)
	w := httptest.NewRecorder()
	s.handleProvidersList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("providers status = %d body=%s", w.Code, w.Body.String())
	}
	var providersResp struct {
		Providers []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Source     string `json:"source"`
			ModelCount int    `json:"model_count"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&providersResp); err != nil {
		t.Fatalf("decode providers: %v", err)
	}
	if len(providersResp.Providers) != 1 {
		t.Fatalf("providers = %#v, want one third-party provider", providersResp.Providers)
	}
	got := providersResp.Providers[0]
	if got.ID != "provider1" || got.Name != "xiaomi-plan" || got.Source != "provider" || got.ModelCount != 1 {
		t.Fatalf("model provider = %#v, want provider1 with xiaomi-plan display name", got)
	}
	if providerModelRequests != 0 {
		t.Fatalf("providers list requested third-party models %d times, want none", providerModelRequests)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=provider1", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("tags status = %d body=%s", w.Code, w.Body.String())
	}
	var tagsResp struct {
		Models []struct {
			Model    string `json:"model"`
			Provider string `json:"provider"`
			Category string `json:"category"`
		} `json:"models"`
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode tags: %v", err)
	}
	if len(tagsResp.Models) != 1 || tagsResp.Models[0].Model != "mi-model" || tagsResp.Models[0].Provider != "xiaomi-plan" {
		t.Fatalf("models = %#v, want xiaomi-plan model", tagsResp.Models)
	}
	if tagsResp.Models[0].Category != "language_model" {
		t.Fatalf("category = %q, want language_model", tagsResp.Models[0].Category)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=provider1&category=language_model", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("language category status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode language category tags: %v", err)
	}
	if len(tagsResp.Models) != 1 || tagsResp.Models[0].Model != "mi-model" {
		t.Fatalf("language category models = %#v, want mi-model", tagsResp.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=provider1&category=image_generation", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("image category status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode image category tags: %v", err)
	}
	if len(tagsResp.Models) != 0 {
		t.Fatalf("image category models = %#v, want none", tagsResp.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=provider1&category=bad", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid category status = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=openai", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("openai filter status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode openai filtered tags: %v", err)
	}
	if len(tagsResp.Models) != 0 {
		t.Fatalf("openai-compatible type should not be exposed as model provider: %#v", tagsResp.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi-plan", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("provider name filter status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode provider name filtered tags: %v", err)
	}
	if len(tagsResp.Models) != 1 || tagsResp.Models[0].Model != "mi-model" {
		t.Fatalf("provider name compatibility models = %#v, want mi-model", tagsResp.Models)
	}
}

func TestProviderTagsManageSelectsModels(t *testing.T) {
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
			"data": []map[string]any{
				{"id": "mi-model", "task": "text-generation"},
				{"id": "scope/with/slash", "display_name": "Scope Slash", "pipeline_tag": "text-to-image", "architecture": map[string]any{"input_modalities": []string{"text"}, "output_modalities": []string{"image"}}},
			},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:       "provider1",
		Name:     "xiaomi-plan",
		BaseURL:  apiServer.URL + "/v1",
		APIKey:   "secret",
		Provider: "openai",
		Enabled:  true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/tags/manage?provider=xiaomi-plan", nil)
	w := httptest.NewRecorder()
	s.handleProviderTagsManageList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("manage list status = %d body=%s", w.Code, w.Body.String())
	}
	var tagsResp struct {
		Models []struct {
			Model       string `json:"model"`
			Provider    string `json:"provider"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode manage list: %v", err)
	}
	if len(tagsResp.Models) != 2 {
		t.Fatalf("manage models = %#v, want two catalog models", tagsResp.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags/manage?provider=xiaomi-plan&category=image_generation", nil)
	w = httptest.NewRecorder()
	s.handleProviderTagsManageList(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("manage filtered list status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode filtered manage list: %v", err)
	}
	if len(tagsResp.Models) != 1 || tagsResp.Models[0].Model != "scope/with/slash" {
		t.Fatalf("filtered manage models = %#v, want image model", tagsResp.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi-plan", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("selected tags status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode selected tags: %v", err)
	}
	if len(tagsResp.Models) != 0 {
		t.Fatalf("selected models before add = %#v, want none", tagsResp.Models)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/tags/manage?provider=xiaomi-plan", strings.NewReader(`{"model":"scope/with/slash"}`))
	w = httptest.NewRecorder()
	s.handleProviderTagsManageAdd(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add status = %d body=%s", w.Code, w.Body.String())
	}
	selections := config.GetProviderModelSelections("provider1")
	if len(selections) != 1 {
		t.Fatalf("selections = %#v, want one", selections)
	}
	if selections[0].CatalogDisplayName != "Scope Slash" || selections[0].PipelineTag != "text-to-image" {
		t.Fatalf("selection metadata = %#v, want catalog display name and pipeline tag", selections[0])
	}
	if !stringSliceContains(selections[0].InputModalities, "text") || !stringSliceContains(selections[0].OutputModalities, "image") {
		t.Fatalf("selection modalities = inputs %#v outputs %#v, want text->image", selections[0].InputModalities, selections[0].OutputModalities)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi-plan", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("selected tags status after add = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode selected tags after add: %v", err)
	}
	if len(tagsResp.Models) != 1 || tagsResp.Models[0].Model != "scope/with/slash" || tagsResp.Models[0].Provider != "xiaomi-plan" || tagsResp.Models[0].DisplayName != "Scope Slash [xiaomi-plan]" {
		t.Fatalf("selected models = %#v, want slash model", tagsResp.Models)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/tags/manage?provider=xiaomi-plan&model=scope%2Fwith%2Fslash", nil)
	w = httptest.NewRecorder()
	s.handleProviderTagsManageDelete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi-plan", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("selected tags status after delete = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&tagsResp); err != nil {
		t.Fatalf("decode selected tags after delete: %v", err)
	}
	if len(tagsResp.Models) != 0 {
		t.Fatalf("selected models after delete = %#v, want none", tagsResp.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags/manage?provider=xiaomi-plan&category=bad", nil)
	w = httptest.NewRecorder()
	s.handleProviderTagsManageList(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid category status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestProviderTagsManageReplaceModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "a", "task": "text-generation"},
				{"id": "b", "task": "text-to-image"},
			},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:      "provider1",
		Name:    "xiaomi-plan",
		BaseURL: apiServer.URL + "/v1",
		APIKey:  "secret",
		Enabled: true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/tags/manage?provider=provider1", strings.NewReader(`{"models":["a","b","a"]}`))
	w := httptest.NewRecorder()
	s.handleProviderTagsManageReplace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("replace status = %d body=%s", w.Code, w.Body.String())
	}
	got := config.GetProviderModelAllowlist("provider1")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("allowlist = %#v, want a,b", got)
	}
	req = httptest.NewRequest(http.MethodPatch, "/api/tags/manage?provider=provider1&model=a", strings.NewReader(`{"model":"b"}`))
	w = httptest.NewRecorder()
	s.handleProviderTagsManageUpdate(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate update status = %d body=%s, want conflict", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/tags/manage?provider=provider1", strings.NewReader(`{"models":[{"model":"a","display_name":"Renamed A"}]}`))
	w = httptest.NewRecorder()
	s.handleProviderTagsManageReplace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("replace display name status = %d body=%s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi-plan", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("tags display name status = %d body=%s", w.Code, w.Body.String())
	}
	var selectedResp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&selectedResp); err != nil {
		t.Fatalf("decode display name tags: %v", err)
	}
	if len(selectedResp.Models) != 1 || selectedResp.Models[0].DisplayName != "Renamed A" || selectedResp.Models[0].Label != "Renamed A" {
		t.Fatalf("renamed selected models = %#v, want Renamed A", selectedResp.Models)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/tags/manage?provider=provider1&model=a", strings.NewReader(`{"display_name":"Patched A","description":"Patched description"}`))
	w = httptest.NewRecorder()
	s.handleProviderTagsManageUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update model metadata status = %d body=%s", w.Code, w.Body.String())
	}
	var patched api.ModelInfo
	if err := json.NewDecoder(w.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patched model: %v", err)
	}
	if patched.DisplayName != "Patched A" || patched.Description != "Patched description" {
		t.Fatalf("patched model = %#v, want custom name and description", patched)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi-plan", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("tags patched status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&selectedResp); err != nil {
		t.Fatalf("decode patched selected tags: %v", err)
	}
	if len(selectedResp.Models) != 1 || selectedResp.Models[0].DisplayName != "Patched A" || selectedResp.Models[0].Description != "Patched description" {
		t.Fatalf("patched selected models = %#v, want patched metadata", selectedResp.Models)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/tags/manage?provider=provider1&model=a", strings.NewReader(`{"model":"alias-a"}`))
	w = httptest.NewRecorder()
	s.handleProviderTagsManageUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update model id status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&patched); err != nil {
		t.Fatalf("decode model id patch: %v", err)
	}
	if patched.Model != "alias-a" || patched.DisplayName != "Patched A" || patched.Description != "Patched description" {
		t.Fatalf("model id patch = %#v, want alias-a with patched metadata", patched)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi-plan", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("tags model id status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&selectedResp); err != nil {
		t.Fatalf("decode model id selected tags: %v", err)
	}
	if len(selectedResp.Models) != 1 || selectedResp.Models[0].Model != "alias-a" {
		t.Fatalf("aliased selected models = %#v, want alias-a", selectedResp.Models)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/tags/manage?provider=provider1", strings.NewReader(`{"model":"a"}`))
	w = httptest.NewRecorder()
	s.handleProviderTagsManageAdd(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add existing original status = %d body=%s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi-plan", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("tags after duplicate original add status = %d body=%s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&selectedResp); err != nil {
		t.Fatalf("decode duplicate original selected tags: %v", err)
	}
	if len(selectedResp.Models) != 1 || selectedResp.Models[0].Model != "alias-a" {
		t.Fatalf("duplicate original selected models = %#v, want only alias-a", selectedResp.Models)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/tags/manage?provider=provider1&category=image_generation", strings.NewReader(`{"models":["a","b"]}`))
	w = httptest.NewRecorder()
	s.handleProviderTagsManageReplace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("replace filtered status = %d body=%s", w.Code, w.Body.String())
	}
	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode replace filtered response: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "b" {
		t.Fatalf("replace filtered models = %#v, want b", resp.Models)
	}
}

func TestProviderModelAliasForwardsOriginalModelID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	upstreamModel := ""
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": "a", "task": "text-generation"}},
			})
		case "/v1/chat/completions":
			var body struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode upstream chat body: %v", err)
			}
			upstreamModel = body.Model
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:      "provider1",
		Name:    "xiaomi-plan",
		BaseURL: apiServer.URL + "/v1",
		APIKey:  "secret",
		Enabled: true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tags/manage?provider=provider1", strings.NewReader(`{"model":"a"}`))
	w := httptest.NewRecorder()
	s.handleProviderTagsManageAdd(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add status = %d body=%s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodPatch, "/api/tags/manage?provider=provider1&model=a", strings.NewReader(`{"model":"alias-a"}`))
	w = httptest.NewRecorder()
	s.handleProviderTagsManageUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("alias status = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"alias-a","messages":[{"role":"user","content":"hi"}]}`))
	w = httptest.NewRecorder()
	s.handleOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chat status = %d body=%s", w.Code, w.Body.String())
	}
	if upstreamModel != "a" {
		t.Fatalf("upstream model = %q, want original model a", upstreamModel)
	}
}

func TestThirdPartyProviderEngineTrimsV1BaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	var chatPath string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatPath = r.URL.Path
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer apiServer.Close()

	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:      "provider1",
		Name:    "OpenAI",
		BaseURL: apiServer.URL + "/v1",
		APIKey:  "secret",
		Enabled: true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	eng, err := newThirdPartyProviderEngine("provider:provider1", "gpt-4o-mini")
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	got, err := eng.Chat(context.Background(), nil, inference.DefaultOptions(), nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("chat = %q, want ok", got)
	}
	if chatPath != "/v1/chat/completions" {
		t.Fatalf("chat path = %q", chatPath)
	}
}

func TestThirdPartyProviderEngineUsesCompatibleBaseURLPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	var modelPath, chatPath string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/paas/v4/models":
			modelPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"id":"glm-5.1"}]}`)
		case "/api/paas/v4/chat/completions":
			chatPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:       "provider1",
		Name:     "BigModel",
		BaseURL:  apiServer.URL + "/api/paas/v4",
		APIKey:   "secret",
		Provider: "bigmodel",
		Enabled:  true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	models, err := listOpenAICompatibleProviderModels(context.Background(), config.GetProviders()[0])
	if err != nil {
		t.Fatalf("list models returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "glm-5.1" {
		t.Fatalf("models = %#v", models)
	}

	eng, err := newThirdPartyProviderEngine("provider:provider1", "glm-5.1")
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	got, err := eng.Chat(context.Background(), nil, inference.DefaultOptions(), nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("chat = %q, want ok", got)
	}
	if modelPath != "/api/paas/v4/models" {
		t.Fatalf("model path = %q", modelPath)
	}
	if chatPath != "/api/paas/v4/chat/completions" {
		t.Fatalf("chat path = %q", chatPath)
	}
}

func TestBigModelLegacyPresetBaseURLNormalizesToOfficialPath(t *testing.T) {
	provider := config.ThirdPartyProvider{
		Name:     "BigModel",
		BaseURL:  "https://open.bigmodel.cn/api/coding/paas/v4/",
		Provider: "bigmodel",
	}

	if got := normalizeThirdPartyProviderBaseURL(provider); got != bigModelOfficialBaseURL {
		t.Fatalf("base URL = %q, want %q", got, bigModelOfficialBaseURL)
	}
}

func TestGetChatEnginePrefersThirdPartyWhenCloudLoginMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"id":"shared/model"}]}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"provider ok"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	cloudServer := newCloudOpenAIAPIServer(t, "")
	defer cloudServer.Close()

	s := newTestServer(t)
	s.cloud = cloud.NewService(cloudServer.URL)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:      "provider1",
		Name:    "OpenAI",
		BaseURL: providerServer.URL + "/v1",
		APIKey:  "secret",
		Enabled: true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}
	if err := config.ReplaceProviderModelAllowlist("provider1", []string{"shared/model"}); err != nil {
		t.Fatalf("save provider model allowlist: %v", err)
	}

	eng, err := s.getChatEngine(context.Background(), "shared/model", "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatalf("getChatEngine returned error: %v", err)
	}
	got, err := eng.Chat(context.Background(), nil, inference.DefaultOptions(), nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}
	if got != "provider ok" {
		t.Fatalf("chat = %q, want provider ok", got)
	}
}

func TestGetChatEngineFallsBackToThirdPartyWhenCloudListFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"provider ok"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	cloudServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cloud unavailable", http.StatusInternalServerError)
	}))
	defer cloudServer.Close()

	s := newTestServer(t)
	s.cfg.OpenCSGAPIKey = "cloud-key"
	s.cloud = cloud.NewService(cloudServer.URL)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:      "provider1",
		Name:    "OpenAI",
		BaseURL: providerServer.URL + "/v1",
		APIKey:  "secret",
		Enabled: true,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}
	if err := config.ReplaceProviderModelAllowlist("provider1", []string{"deepseek-v4-pro"}); err != nil {
		t.Fatalf("save provider model allowlist: %v", err)
	}

	eng, err := s.getChatEngine(context.Background(), "deepseek-v4-pro", "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatalf("getChatEngine returned error: %v", err)
	}
	got, err := eng.Chat(context.Background(), nil, inference.DefaultOptions(), nil)
	if err != nil {
		t.Fatalf("chat returned error: %v", err)
	}
	if got != "provider ok" {
		t.Fatalf("chat = %q, want provider ok", got)
	}
}

func TestDisabledProviderExcludedFromTagsAndEngine(t *testing.T) {
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
			"data": []map[string]string{{"id": "gpt-4o-mini"}},
		})
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID:      "provider1",
		Name:    "OpenAI",
		BaseURL: apiServer.URL + "/v1",
		APIKey:  "secret",
		Enabled: false,
	}}); err != nil {
		t.Fatalf("save providers: %v", err)
	}

	// Disabled provider should not appear in tags
	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Models []struct {
			Source string `json:"source"`
		} `json:"models"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tags: %v", err)
	}
	for _, m := range resp.Models {
		if m.Source == "provider:provider1" {
			t.Fatalf("disabled provider model should not appear in tags")
		}
	}

	// Engine creation for disabled provider should fail
	_, err := newThirdPartyProviderEngine("provider:provider1", "gpt-4o-mini")
	if err == nil {
		t.Fatalf("expected error for disabled provider engine")
	}
}

func TestProviderUpdateDisablesSelectedProviderModelsWithoutRemoteFetch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)

	modelRequests := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		modelRequests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "gpt-4o-mini"}},
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
	if err := config.ReplaceProviderModelAllowlist("provider1", []string{"gpt-4o-mini"}); err != nil {
		t.Fatalf("save provider model allowlist: %v", err)
	}

	hasProviderModel := func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
		w := httptest.NewRecorder()
		s.handleTags(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("tags status = %d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Models []struct {
				Source string `json:"source"`
			} `json:"models"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode tags: %v", err)
		}
		for _, m := range resp.Models {
			if m.Source == "provider:provider1" {
				return true
			}
		}
		return false
	}

	if !hasProviderModel() {
		t.Fatalf("provider model should appear before disabling")
	}
	if modelRequests != 0 {
		t.Fatalf("tags requested third-party models %d times, want none", modelRequests)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/providers/provider1", strings.NewReader(`{"enabled":false}`))
	req.SetPathValue("id", "provider1")
	w := httptest.NewRecorder()
	s.handleProviderUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", w.Code, w.Body.String())
	}

	if hasProviderModel() {
		t.Fatalf("disabled provider model should not remain in cached tags response")
	}
	if modelRequests != 0 {
		t.Fatalf("disabling provider should not revalidate remote models, got %d requests", modelRequests)
	}
}
