package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/inference"
)

func TestProviderCRUDDoesNotExposeAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	t.Cleanup(config.ResetProviders)

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

func TestProviderCreateRejectsInvalidConfigWithoutSaving(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	t.Cleanup(config.ResetProviders)

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

func TestHandleTagsIncludesThirdPartyProviderModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	t.Cleanup(config.ResetProviders)

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

func TestThirdPartyProviderEngineTrimsV1BaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	t.Cleanup(config.ResetProviders)

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
	t.Cleanup(config.ResetProviders)

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
	t.Cleanup(config.ResetProviders)

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

func TestDisabledProviderExcludedFromTagsAndEngine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	t.Cleanup(config.ResetProviders)

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
