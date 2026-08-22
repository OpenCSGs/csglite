package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/modelmetadata"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestHandleSettingsReturnsStorageDir(t *testing.T) {
	s := newTestServer(t)
	s.cfg.HiddenNavItems = []string{"datasets", "ai-apps"}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()

	s.handleSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantStorage := config.StorageDir(s.cfg.ModelDir, s.cfg.DatasetDir)
	if resp.StorageDir != wantStorage {
		t.Fatalf("storage_dir = %q, want %q", resp.StorageDir, wantStorage)
	}
	if resp.ModelDir != s.cfg.ModelDir {
		t.Fatalf("model_dir = %q, want %q", resp.ModelDir, s.cfg.ModelDir)
	}
	if resp.DatasetDir != s.cfg.DatasetDir {
		t.Fatalf("dataset_dir = %q, want %q", resp.DatasetDir, s.cfg.DatasetDir)
	}
	if resp.ServerURL != s.cfg.ServerURL || resp.DefaultServerURL != config.DefaultServerURL {
		t.Fatalf("server URLs = %q/%q, want %q/%q", resp.ServerURL, resp.DefaultServerURL, s.cfg.ServerURL, config.DefaultServerURL)
	}
	if resp.AIGatewayURL != cloud.DefaultBaseURL || resp.DefaultAIGatewayURL != cloud.DefaultBaseURL {
		t.Fatalf("AI Gateway URLs = %q/%q, want default %q", resp.AIGatewayURL, resp.DefaultAIGatewayURL, cloud.DefaultBaseURL)
	}
	if resp.CloudProviderName != config.DefaultCloudProviderName || resp.DefaultCloudProviderName != config.DefaultCloudProviderName {
		t.Fatalf("cloud provider names = %q/%q, want default %q", resp.CloudProviderName, resp.DefaultCloudProviderName, config.DefaultCloudProviderName)
	}
	if got, want := strings.Join(resp.HiddenNavItems, ","), "datasets,ai-apps"; got != want {
		t.Fatalf("hidden_nav_items = %q, want %q", got, want)
	}
}

func TestHandleSettingsUpdatesLlamaUseModelMaxCtx(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CSGHUB_LITE_LLAMA_USE_MODEL_MAX_CTX", "")
	config.Reset()

	s := newTestServer(t)
	enabled := true
	body, err := json.Marshal(api.SettingsUpdateRequest{LlamaUseModelMaxCtx: &enabled})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSettingsUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !s.cfg.Inference.LlamaUseModelMaxCtx {
		t.Fatal("LlamaUseModelMaxCtx = false, want true")
	}

	var resp api.SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.LlamaUseModelMaxCtx {
		t.Fatal("response llama_use_model_max_ctx = false, want true")
	}
}

func TestHandleSettingsUpdateStorageDirUpdatesModelAndDatasetDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.Reset()

	s := newTestServer(t)
	root := filepath.Join(t.TempDir(), "shared-storage")

	body, err := json.Marshal(api.SettingsUpdateRequest{StorageDir: root})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleSettingsUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantModelDir := filepath.Join(root, config.ModelsDir)
	wantDatasetDir := filepath.Join(root, config.DatasetsDir)
	if resp.StorageDir != root {
		t.Fatalf("storage_dir = %q, want %q", resp.StorageDir, root)
	}
	if resp.ModelDir != wantModelDir {
		t.Fatalf("model_dir = %q, want %q", resp.ModelDir, wantModelDir)
	}
	if resp.DatasetDir != wantDatasetDir {
		t.Fatalf("dataset_dir = %q, want %q", resp.DatasetDir, wantDatasetDir)
	}
	wantCachePath := filepath.Join(root, modelmetadata.DirName, modelmetadata.DatabaseFile)
	if s.modelMetadata == nil || s.modelMetadata.Path() != wantCachePath {
		t.Fatalf("model metadata cache path = %v, want %q", s.modelMetadata, wantCachePath)
	}

	if _, err := os.Stat(wantModelDir); err != nil {
		t.Fatalf("model dir not created: %v", err)
	}
	if _, err := os.Stat(wantDatasetDir); err != nil {
		t.Fatalf("dataset dir not created: %v", err)
	}
}

func TestHandleSettingsUpdateServiceURLs(t *testing.T) {
	s := newTestServer(t)
	s.cfg.ServerURL = config.DefaultServerURL
	s.cfg.Token = "old-token"

	serverURL := " https://opencsg-stg.com "
	aiGatewayURL := " https://gateway.example.com "
	cloudProviderName := " OpenCSG "
	body, err := json.Marshal(api.SettingsUpdateRequest{
		ServerURL:         &serverURL,
		AIGatewayURL:      &aiGatewayURL,
		CloudProviderName: &cloudProviderName,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleSettingsUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if s.cfg.ServerURL != "https://opencsg-stg.com" {
		t.Fatalf("ServerURL = %q, want staging URL", s.cfg.ServerURL)
	}
	if s.cfg.Token != "" {
		t.Fatalf("Token = %q, want cleared after server URL change", s.cfg.Token)
	}
	if s.cfg.AIGatewayURL != "https://gateway.example.com" {
		t.Fatalf("AIGatewayURL = %q, want custom gateway", s.cfg.AIGatewayURL)
	}
	if s.cfg.CloudProviderName != "OpenCSG" {
		t.Fatalf("CloudProviderName = %q, want custom provider name", s.cfg.CloudProviderName)
	}
	if got := s.cloud.BaseURL(); got != "https://gateway.example.com" {
		t.Fatalf("cloud base URL = %q, want custom gateway", got)
	}
}

func TestHandleSettingsUpdateServiceURLsRestoreDefaults(t *testing.T) {
	s := newTestServer(t)
	s.cfg.ServerURL = "https://opencsg-stg.com"
	s.cfg.AIGatewayURL = "https://gateway.example.com"
	s.cfg.CloudProviderName = "OpenCSG"

	empty := ""
	body, err := json.Marshal(api.SettingsUpdateRequest{
		ServerURL:         &empty,
		AIGatewayURL:      &empty,
		CloudProviderName: &empty,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleSettingsUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.SettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if s.cfg.ServerURL != config.DefaultServerURL || resp.ServerURL != config.DefaultServerURL {
		t.Fatalf("server_url = cfg %q resp %q, want %q", s.cfg.ServerURL, resp.ServerURL, config.DefaultServerURL)
	}
	if s.cfg.AIGatewayURL != "" || resp.AIGatewayURL != cloud.DefaultBaseURL {
		t.Fatalf("ai_gateway_url = cfg %q resp %q, want empty/default %q", s.cfg.AIGatewayURL, resp.AIGatewayURL, cloud.DefaultBaseURL)
	}
	if s.cfg.CloudProviderName != config.DefaultCloudProviderName || resp.CloudProviderName != config.DefaultCloudProviderName {
		t.Fatalf("cloud_provider_name = cfg %q resp %q, want %q", s.cfg.CloudProviderName, resp.CloudProviderName, config.DefaultCloudProviderName)
	}
	if got := s.cloud.BaseURL(); got != cloud.DefaultBaseURL {
		t.Fatalf("cloud base URL = %q, want default %q", got, cloud.DefaultBaseURL)
	}
}

func TestHandleSettingsUpdateRejectsAutostartInDesktopMode(t *testing.T) {
	s := newTestServer(t)
	s.cfg.DesktopMode = true
	enabled := true
	body, err := json.Marshal(api.SettingsUpdateRequest{Autostart: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSettingsUpdate(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want %d", w.Code, w.Body.String(), http.StatusConflict)
	}
}

func TestCurrentSettingsIncludesDesktopAPIURL(t *testing.T) {
	cfg := &config.Config{
		DesktopMode:         true,
		DesktopAPIAddr:      config.DefaultDesktopAPIAddr,
		DesktopAPIBoundAddr: config.DefaultDesktopAPIAddr,
	}
	settings := currentSettingsResponse(cfg, "test")
	if settings.LocalAPIURL != "http://127.0.0.1:11436" {
		t.Fatalf("LocalAPIURL = %q, want stable desktop API URL", settings.LocalAPIURL)
	}
}

func TestHandleSettingsUpdatesObservabilityRetention(t *testing.T) {
	s := newTestServer(t)
	body, err := json.Marshal(api.SettingsUpdateRequest{
		Observability: &api.ObservabilitySettings{RetentionDays: 90},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	recorder := httptest.NewRecorder()

	s.handleSettingsUpdate(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := config.ObservabilityRetentionDays(s.cfg.Observability); got != 90 {
		t.Fatalf("retention days = %d, want 90", got)
	}
	var response api.SettingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Observability.RetentionDays != 90 {
		t.Fatalf("response retention days = %d, want 90", response.Observability.RetentionDays)
	}
}

func TestHandleSettingsResolvesChinaHuggingFaceMirror(t *testing.T) {
	t.Setenv(config.EnvHuggingFaceEndpoint, "")
	t.Setenv("CSGHUB_LITE_REGION", "CN")
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	recorder := httptest.NewRecorder()
	s.handleSettings(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response api.SettingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.HuggingFaceEndpoint != config.ChinaHuggingFaceEndpoint {
		t.Fatalf("huggingface_endpoint = %q, want %q", response.HuggingFaceEndpoint, config.ChinaHuggingFaceEndpoint)
	}
}

func TestHandleSettingsUpdatesRegistryCredentialsWithoutReturningSecrets(t *testing.T) {
	s := newTestServer(t)
	body := []byte(`{
		"huggingface_endpoint":"https://hf.example.test",
		"huggingface_token":"hf-secret",
		"modelscope_endpoint":"https://ms.example.test",
		"modelscope_token":"ms-secret"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	recorder := httptest.NewRecorder()

	s.handleSettingsUpdate(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "hf-secret") || strings.Contains(recorder.Body.String(), "ms-secret") {
		t.Fatal("settings response exposed a registry token")
	}
	var response api.SettingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.HuggingFaceTokenSet || !response.ModelScopeTokenSet {
		t.Fatalf("token status = HF %v MS %v", response.HuggingFaceTokenSet, response.ModelScopeTokenSet)
	}
	if s.cfg.HuggingFaceToken != "hf-secret" || s.cfg.ModelScopeToken != "ms-secret" {
		t.Fatal("registry tokens were not saved in server config")
	}
}

func TestHandleSettingsPersistsMarketplaceModelSource(t *testing.T) {
	s := newTestServer(t)
	source := "huggingface"
	body, err := json.Marshal(api.SettingsUpdateRequest{MarketplaceModelSource: &source})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleSettingsUpdate(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if s.cfg.MarketplaceModelSource != source {
		t.Fatalf("MarketplaceModelSource = %q", s.cfg.MarketplaceModelSource)
	}
	var response api.SettingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.MarketplaceModelSource != source {
		t.Fatalf("response source = %q", response.MarketplaceModelSource)
	}
}

func TestHandleSettingsRejectsUnknownMarketplaceModelSource(t *testing.T) {
	s := newTestServer(t)
	source := "unknown"
	body, err := json.Marshal(api.SettingsUpdateRequest{MarketplaceModelSource: &source})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleSettingsUpdate(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleSettingsPersistsMarketplaceDatasetSource(t *testing.T) {
	s := newTestServer(t)
	source := "modelscope"
	body, err := json.Marshal(api.SettingsUpdateRequest{MarketplaceDatasetSource: &source})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleSettingsUpdate(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if s.cfg.MarketplaceDatasetSource != source {
		t.Fatalf("MarketplaceDatasetSource = %q", s.cfg.MarketplaceDatasetSource)
	}
	var response api.SettingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.MarketplaceDatasetSource != source {
		t.Fatalf("response source = %q", response.MarketplaceDatasetSource)
	}
}

func TestHandleSettingsRejectsUnknownMarketplaceDatasetSource(t *testing.T) {
	s := newTestServer(t)
	source := "unknown"
	body, err := json.Marshal(api.SettingsUpdateRequest{MarketplaceDatasetSource: &source})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleSettingsUpdate(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}
