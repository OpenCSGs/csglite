package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestExtractDashboardURL(t *testing.T) {
	output := []byte("Using model Qwen/Qwen3.5-2B\nDashboard URL: http://127.0.0.1:18789/#token=abc123\nBrowser launch disabled (--no-open).\n")

	got, err := extractDashboardURL(output)
	if err != nil {
		t.Fatalf("extractDashboardURL returned error: %v", err)
	}
	if want := "http://127.0.0.1:18789/#token=abc123"; got != want {
		t.Fatalf("extractDashboardURL = %q, want %q", got, want)
	}
}

func TestDashboardHostPort(t *testing.T) {
	got, err := dashboardHostPort("http://127.0.0.1:18789/#token=abc123")
	if err != nil {
		t.Fatalf("dashboardHostPort returned error: %v", err)
	}
	if want := "127.0.0.1:18789"; got != want {
		t.Fatalf("dashboardHostPort = %q, want %q", got, want)
	}
}

func TestOpenClawDirectChatURL(t *testing.T) {
	got, err := openClawDirectChatURL("http://127.0.0.1:18789/#token=abc123", "main")
	if err != nil {
		t.Fatalf("openClawDirectChatURL returned error: %v", err)
	}
	if want := "http://127.0.0.1:18789/chat?session=main#token=abc123"; got != want {
		t.Fatalf("openClawDirectChatURL = %q, want %q", got, want)
	}
}

func TestOpenClawURLWithGatewayToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".openclaw-"+openClawWebProfile)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configJSON := `{
  "gateway": {
    "auth": {
      "mode": "token",
      "token": "test-gateway-token"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "openclaw.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	got, err := openClawURLWithGatewayToken("http://127.0.0.1:18789/")
	if err != nil {
		t.Fatalf("openClawURLWithGatewayToken returned error: %v", err)
	}
	if want := "http://127.0.0.1:18789/#token=test-gateway-token"; got != want {
		t.Fatalf("openClawURLWithGatewayToken = %q, want %q", got, want)
	}
}

func TestOpenClawURLWithGatewayTokenKeepsExistingToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".openclaw-"+openClawWebProfile)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configJSON := `{
  "gateway": {
    "auth": {
      "mode": "token",
      "token": "config-token"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "openclaw.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	got, err := openClawURLWithGatewayToken("http://127.0.0.1:18789/#token=cli-token")
	if err != nil {
		t.Fatalf("openClawURLWithGatewayToken returned error: %v", err)
	}
	if want := "http://127.0.0.1:18789/#token=cli-token"; got != want {
		t.Fatalf("openClawURLWithGatewayToken = %q, want %q", got, want)
	}
}

func TestOpenClawProfileMatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".openclaw-"+openClawWebProfile)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configJSON := `{
  "models": {
    "providers": {
      "opencsg": {
        "baseUrl": "http://127.0.0.1:11435/v1"
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "opencsg/Qwen/Qwen3.5-2B"
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "openclaw.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ok, err := openClawProfileMatches("http://127.0.0.1:11435", "Qwen/Qwen3.5-2B")
	if err != nil {
		t.Fatalf("openClawProfileMatches returned error: %v", err)
	}
	if !ok {
		t.Fatal("openClawProfileMatches returned false, want true")
	}
}

func TestSyncOpenClawProfileRewritesStaleModelCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	profileDir := filepath.Join(home, ".openclaw-"+openClawWebProfile)
	agentDir := filepath.Join(profileDir, "agents", "main", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}

	staleProfile := `{
  "models": {
    "mode": "merge",
    "providers": {
      "ollama": {
        "baseUrl": "http://127.0.0.1:11436",
        "models": [{"id": "old-local"}]
      },
      "csghub-lite-2": {
        "baseUrl": "http://127.0.0.1:11435/v1",
        "models": [{"id": "old-provider"}]
      },
      "csghub": {
        "baseUrl": "http://127.0.0.1:11435/v1",
        "models": [{"id": "old-cloud"}]
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "csghub/old-cloud"
      },
      "models": {
        "csghub/old-cloud": {}
      },
      "workspace": "/tmp/openclaw-workspace"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(profileDir, "openclaw.json"), []byte(staleProfile), 0o644); err != nil {
		t.Fatalf("write stale profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"csghub":{"models":[{"id":"old-cloud"}]}}}`), 0o644); err != nil {
		t.Fatalf("write stale agent models: %v", err)
	}

	models := []api.ModelInfo{
		{Model: "minimax-m2.5", DisplayName: "MiniMax M2.5", Source: "cloud"},
		{Model: "Qwen/Qwen3.5-2B", DisplayName: "Qwen/Qwen3.5-2B", Source: "local"},
	}
	if err := syncOpenClawProfile("http://127.0.0.1:11435", "user-token", "minimax-m2.5", models); err != nil {
		t.Fatalf("syncOpenClawProfile returned error: %v", err)
	}

	var profile struct {
		Models struct {
			Providers map[string]struct {
				BaseURL string `json:"baseUrl"`
				APIKey  string `json:"apiKey"`
				Models  []struct {
					ID string `json:"id"`
				} `json:"models"`
			} `json:"providers"`
		} `json:"models"`
		Agents struct {
			Defaults struct {
				Model struct {
					Primary string `json:"primary"`
				} `json:"model"`
				Models map[string]map[string]interface{} `json:"models"`
			} `json:"defaults"`
		} `json:"agents"`
	}
	data, err := os.ReadFile(filepath.Join(profileDir, "openclaw.json"))
	if err != nil {
		t.Fatalf("read synced profile: %v", err)
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("decode synced profile: %v", err)
	}
	if len(profile.Models.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(profile.Models.Providers))
	}
	provider, ok := profile.Models.Providers[openClawProviderID]
	if !ok {
		t.Fatalf("provider %q missing after sync: %#v", openClawProviderID, profile.Models.Providers)
	}
	if provider.BaseURL != "http://127.0.0.1:11435/v1" {
		t.Fatalf("provider baseUrl = %q, want local v1 URL", provider.BaseURL)
	}
	if provider.APIKey != "user-token" {
		t.Fatalf("provider apiKey = %q, want saved user token", provider.APIKey)
	}
	if got := collectOpenClawModelIDs(provider.Models); !sameStrings(got, []string{"minimax-m2.5", "Qwen/Qwen3.5-2B"}) {
		t.Fatalf("provider model ids = %#v, want refreshed model ids", got)
	}
	if profile.Agents.Defaults.Model.Primary != "opencsg/minimax-m2.5" {
		t.Fatalf("primary model = %q, want refreshed cloud model", profile.Agents.Defaults.Model.Primary)
	}
	if got := mapKeys(profile.Agents.Defaults.Models); !sameStrings(got, []string{
		"opencsg/minimax-m2.5",
		"opencsg/Qwen/Qwen3.5-2B",
	}) {
		t.Fatalf("defaults.models = %#v, want refreshed managed models", got)
	}

	var agentModels struct {
		Providers map[string]struct {
			APIKey string `json:"apiKey"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	data, err = os.ReadFile(filepath.Join(agentDir, "models.json"))
	if err != nil {
		t.Fatalf("read synced agent models: %v", err)
	}
	if err := json.Unmarshal(data, &agentModels); err != nil {
		t.Fatalf("decode synced agent models: %v", err)
	}
	if len(agentModels.Providers) != 1 {
		t.Fatalf("agent providers len = %d, want 1", len(agentModels.Providers))
	}
	if agentModels.Providers[openClawProviderID].APIKey != "user-token" {
		t.Fatalf("agent provider apiKey = %q, want saved user token", agentModels.Providers[openClawProviderID].APIKey)
	}
	if got := collectOpenClawModelIDs(agentModels.Providers[openClawProviderID].Models); !sameStrings(got, []string{"minimax-m2.5", "Qwen/Qwen3.5-2B"}) {
		t.Fatalf("agent model ids = %#v, want refreshed model ids", got)
	}
}

func TestLocalBaseURLDefaultsToConfigListenAddr(t *testing.T) {
	s := &Server{cfg: &config.Config{}}

	if got := s.localBaseURL(); got != "http://127.0.0.1:11435" {
		t.Fatalf("localBaseURL = %q, want %q", got, "http://127.0.0.1:11435")
	}
}

func TestCSGClawReachableBaseURLUsesHostReachableAddress(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.10.215"), Mask: net.CIDRMask(24, 32)},
	}

	tests := []struct {
		name       string
		listenAddr string
		want       string
	}{
		{name: "default", listenAddr: "", want: "http://192.168.10.215:11435"},
		{name: "implicit host", listenAddr: ":11435", want: "http://192.168.10.215:11435"},
		{name: "loopback ip", listenAddr: "127.0.0.1:11435", want: "http://192.168.10.215:11435"},
		{name: "localhost", listenAddr: "localhost:11435", want: "http://192.168.10.215:11435"},
		{name: "wildcard", listenAddr: "0.0.0.0:11435", want: "http://192.168.10.215:11435"},
		{name: "explicit host", listenAddr: "192.168.1.8:2244", want: "http://192.168.1.8:2244"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := csgclawReachableBaseURL(tt.listenAddr, addrs); got != tt.want {
				t.Fatalf("csgclawReachableBaseURL(%q) = %q, want %q", tt.listenAddr, got, tt.want)
			}
		})
	}
}

func TestCSGClawReachableBaseURLFallsBackToLoopback(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
	}

	if got := csgclawReachableBaseURL(":11435", addrs); got != "http://127.0.0.1:11435" {
		t.Fatalf("csgclawReachableBaseURL fallback = %q, want %q", got, "http://127.0.0.1:11435")
	}
}

func TestCSGClawConfigNeedsManagerRecreateWhenModelConfigDrifts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".csgclaw")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configTOML := `# Generated by csgclaw onboard.

[server]
listen_addr = "0.0.0.0:18080"
advertise_base_url = ""
access_token = "your_access_token"

[bootstrap]
manager_image = "opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/picoclaw:2026.4.26"

[sandbox]
provider = "boxlite"
home_dir_name = "boxlite"
boxlite_cli_path = "boxlite"

[models]
default = "csghub-lite.glm-5"

[models.providers.csghub-lite]
base_url = "http://192.168.10.215:11435/v1"
api_key = "test-token"
models = ["glm-5", "Qwen/Qwen3-0.6B-GGUF"]
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	if csgclawConfigNeedsManagerRecreate("http://192.168.10.215:11435/v1", "test-token", "glm-5", csgclawManagerImage) {
		t.Fatal("expected matching csgclaw model config to skip manager recreation")
	}
	if !csgclawConfigNeedsManagerRecreate("http://192.168.10.215:11435/v1", "test-token", "Qwen/Qwen3-0.6B-GGUF", csgclawManagerImage) {
		t.Fatal("expected model drift to require manager recreation")
	}
	if !csgclawConfigNeedsManagerRecreate("http://127.0.0.1:11435/v1", "test-token", "glm-5", csgclawManagerImage) {
		t.Fatal("expected base URL drift to require manager recreation")
	}
	if !csgclawConfigNeedsManagerRecreate("http://192.168.10.215:11435/v1", "other-token", "glm-5", csgclawManagerImage) {
		t.Fatal("expected API key drift to require manager recreation")
	}
	if !csgclawConfigNeedsManagerRecreate("http://192.168.10.215:11435/v1", "test-token", "glm-5", "opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/picoclaw:2026.4.24.0") {
		t.Fatal("expected manager image drift to require manager recreation")
	}
}

func TestCSGClawConfigNeedsManagerRecreateForLegacyDefaultProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".csgclaw")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configTOML := `[models]
default = "default.glm-5"

[models.providers.default]
base_url = "http://192.168.10.215:11435/v1"
api_key = "test-token"
models = ["glm-5"]
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	if !csgclawConfigNeedsManagerRecreate("http://192.168.10.215:11435/v1", "test-token", "glm-5", csgclawManagerImage) {
		t.Fatal("expected legacy default provider to require manager recreation")
	}
}

func TestCSGClawOrderedModelsPutsSelectedModelFirst(t *testing.T) {
	got := csgclawOrderedModels("glm-5", []string{
		"Qwen/Qwen3-0.6B-GGUF",
		"glm-5",
		"minimax-m2.5",
		"glm-5",
	})
	want := []string{"glm-5", "Qwen/Qwen3-0.6B-GGUF", "minimax-m2.5"}
	if !sameStrings(got, want) || got[0] != "glm-5" {
		t.Fatalf("csgclawOrderedModels = %#v, want %#v", got, want)
	}
}

func TestParseCSGClawAgentStatusFindsManagerBox(t *testing.T) {
	agents, err := parseCSGClawAgentStatus([]byte(`[
		{"id":"u-worker","name":"worker","role":"worker","box_id":"worker-box"},
		{"id":"u-manager","name":"manager","role":"manager","box_id":"manager-box"}
	]`))
	if err != nil {
		t.Fatalf("parse status: %v", err)
	}

	var managerBox string
	for _, agent := range agents {
		if agent.IsManager() {
			managerBox = agent.BoxID
		}
	}
	if managerBox != "manager-box" {
		t.Fatalf("manager box = %q, want manager-box", managerBox)
	}
}

func TestSetCSGClawConfigDefaultUpdatesExistingDefault(t *testing.T) {
	input := `[server]
listen_addr = "0.0.0.0:18080"

[models]
default = "csghub-lite.old-model"

[models.providers.csghub-lite]
models = ["new-model"]
`
	got, changed := setCSGClawConfigDefault(input, "csghub-lite.new-model")
	if !changed {
		t.Fatal("expected config default to change")
	}
	if !strings.Contains(got, `default = "csghub-lite.new-model"`) {
		t.Fatalf("updated config missing new default:\n%s", got)
	}
}

func TestSetCSGClawConfigDefaultInsertsMissingDefault(t *testing.T) {
	input := `[models]

[models.providers.csghub-lite]
models = ["new-model"]
`
	got, changed := setCSGClawConfigDefault(input, "csghub-lite.new-model")
	if !changed {
		t.Fatal("expected config default to be inserted")
	}
	if !strings.Contains(got, `default = "csghub-lite.new-model"`) ||
		strings.Index(got, `default = "csghub-lite.new-model"`) > strings.Index(got, "[models.providers.csghub-lite]") {
		t.Fatalf("updated config inserted default in wrong location:\n%s", got)
	}
}

func TestRefreshOpenClawModelCatalogRefreshesCloudCache(t *testing.T) {
	currentModel := "stale/model"
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":           currentModel,
					"task":         "text-generation",
					"display_name": currentModel,
				},
			},
		})
	}))
	defer apiServer.Close()

	s := &Server{
		cfg:   &config.Config{Token: "test-token"},
		cloud: cloud.NewService(apiServer.URL),
	}

	models, err := s.cloud.ListChatModels(context.Background())
	if err != nil {
		t.Fatalf("ListChatModels returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "stale/model" {
		t.Fatalf("initial models = %#v, want stale/model", models)
	}

	currentModel = "fresh/model"
	s.refreshOpenClawModelCatalog(context.Background())

	models, err = s.cloud.ListChatModels(context.Background())
	if err != nil {
		t.Fatalf("ListChatModels after refresh returned error: %v", err)
	}
	if len(models) != 1 || models[0].Model != "fresh/model" {
		t.Fatalf("models after refresh = %#v, want fresh/model", models)
	}
}

func TestOpenAIAppShellURLReturnsShellPage(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir(), ListenAddr: ":11435"}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3.5-2B",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "claude")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "claude.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := New(cfg, "test")

	url, err := s.openAIAppShellURL(context.Background(), "claude-code", "", "", "")
	if err != nil {
		t.Fatalf("openAIAppShellURL returned error: %v", err)
	}

	parsed, err := neturl.Parse(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Path != "/ai-apps/shell" {
		t.Fatalf("path = %q, want /ai-apps/shell", parsed.Path)
	}
	sessionID := parsed.Query().Get("session_id")
	if sessionID == "" {
		t.Fatal("expected session_id in shell url")
	}
	if !s.appShells.Close(sessionID) {
		t.Fatalf("expected session %q to exist", sessionID)
	}
}

func TestOpenAIAppShellURLUsesRequestedWorkDir(t *testing.T) {
	cfg := &config.Config{ModelDir: t.TempDir(), ListenAddr: ":11435"}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3.5-2B",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "claude")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "claude.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workDir := t.TempDir()
	s := New(cfg, "test")

	url, err := s.openAIAppShellURL(context.Background(), "claude-code", "", "", workDir)
	if err != nil {
		t.Fatalf("openAIAppShellURL returned error: %v", err)
	}

	parsed, err := neturl.Parse(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	sessionID := parsed.Query().Get("session_id")
	if sessionID == "" {
		t.Fatal("expected session_id in shell url")
	}
	session, ok := s.appShells.Get(sessionID)
	if !ok {
		t.Fatalf("expected session %q to exist", sessionID)
	}
	if session.workDir != workDir {
		t.Fatalf("session workDir = %q, want %q", session.workDir, workDir)
	}
	_ = s.appShells.Close(sessionID)
}

func TestOpenAIAppShellURLRemembersRequestedModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{
		ModelDir:             t.TempDir(),
		ListenAddr:           ":11435",
		AIAppPreferredModels: map[string]string{},
	}
	for _, item := range []*model.LocalModel{
		{
			Namespace:    "Qwen",
			Name:         "Qwen3.5-2B",
			Format:       model.FormatGGUF,
			Size:         4_000_000_000,
			Files:        []string{"model.gguf"},
			DownloadedAt: time.Unix(123, 0),
		},
		{
			Namespace:    "Qwen",
			Name:         "Qwen2.5-Coder-1.5B",
			Format:       model.FormatGGUF,
			Size:         1_500_000_000,
			Files:        []string{"model.gguf"},
			DownloadedAt: time.Unix(124, 0),
		},
	} {
		if err := model.SaveManifest(cfg.ModelDir, item); err != nil {
			t.Fatalf("save model manifest: %v", err)
		}
	}

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "claude")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "claude.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := New(cfg, "test")

	url, err := s.openAIAppShellURL(context.Background(), "claude-code", "Qwen/Qwen2.5-Coder-1.5B", "", "")
	if err != nil {
		t.Fatalf("openAIAppShellURL returned error: %v", err)
	}
	parsed, err := neturl.Parse(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	sessionID := parsed.Query().Get("session_id")
	session, ok := s.appShells.Get(sessionID)
	if !ok {
		t.Fatalf("expected session %q to exist", sessionID)
	}
	if session.modelID != "Qwen/Qwen2.5-Coder-1.5B" {
		t.Fatalf("session modelID = %q, want coder model", session.modelID)
	}
	_ = s.appShells.Close(sessionID)

	if got := s.preferredAIAppModel("claude-code"); got != "Qwen/Qwen2.5-Coder-1.5B" {
		t.Fatalf("preferredAIAppModel = %q, want coder model", got)
	}
}

func TestOpenAIAppShellURLUsesRememberedModelWhenRequestOmitted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
		AIAppPreferredModels: map[string]string{
			"claude-code": "Qwen/Qwen2.5-Coder-1.5B",
		},
	}
	for _, item := range []*model.LocalModel{
		{
			Namespace:    "Qwen",
			Name:         "Qwen3.5-2B",
			Format:       model.FormatGGUF,
			Size:         4_000_000_000,
			Files:        []string{"model.gguf"},
			DownloadedAt: time.Unix(123, 0),
		},
		{
			Namespace:    "Qwen",
			Name:         "Qwen2.5-Coder-1.5B",
			Format:       model.FormatGGUF,
			Size:         1_500_000_000,
			Files:        []string{"model.gguf"},
			DownloadedAt: time.Unix(124, 0),
		},
	} {
		if err := model.SaveManifest(cfg.ModelDir, item); err != nil {
			t.Fatalf("save model manifest: %v", err)
		}
	}

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "claude")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "claude.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := New(cfg, "test")

	url, err := s.openAIAppShellURL(context.Background(), "claude-code", "", "", "")
	if err != nil {
		t.Fatalf("openAIAppShellURL returned error: %v", err)
	}
	parsed, err := neturl.Parse(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	sessionID := parsed.Query().Get("session_id")
	session, ok := s.appShells.Get(sessionID)
	if !ok {
		t.Fatalf("expected session %q to exist", sessionID)
	}
	if session.modelID != "Qwen/Qwen2.5-Coder-1.5B" {
		t.Fatalf("session modelID = %q, want remembered coder model", session.modelID)
	}
	_ = s.appShells.Close(sessionID)
}

func TestResolveCSGClawLaunchModelsUsesRememberedModelWhenRequestOmitted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
		Token:      "test-token",
		AIAppPreferredModels: map[string]string{
			"csgclaw": "glm-5",
		},
	}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3-0.6B-GGUF",
		Format:       model.FormatGGUF,
		Size:         600_000_000,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":           "glm-5",
					"task":         "text-generation",
					"display_name": "glm-5",
				},
			},
		})
	}))
	defer apiServer.Close()

	s := New(cfg, "test")
	s.cloud = cloud.NewService(apiServer.URL)

	modelID, modelIDs, err := s.resolveCSGClawLaunchModels(context.Background(), "", "")
	if err != nil {
		t.Fatalf("resolveCSGClawLaunchModels returned error: %v", err)
	}
	if modelID != "glm-5" {
		t.Fatalf("modelID = %q, want remembered glm-5", modelID)
	}
	if !sameStrings(modelIDs, []string{"Qwen/Qwen3-0.6B-GGUF", "glm-5"}) {
		t.Fatalf("modelIDs = %#v, want local model plus remembered glm-5", modelIDs)
	}
}

func TestOpenAIAppShellURLMissingCloudTokenShowsSettingsHint(t *testing.T) {
	cfg := &config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
	}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3.5-2B",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "claude")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "claude.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := New(cfg, "test")

	_, err := s.openAIAppShellURL(context.Background(), "claude-code", "afrideva/Qwen2-0.5B-Instruct-GGUF:fh23aijhzx8g", "", "")
	if err == nil {
		t.Fatal("openAIAppShellURL returned nil error, want settings hint")
	}
	if got := err.Error(); !strings.Contains(got, "open csghub-lite Settings and save an Access Token first") {
		t.Fatalf("error = %q, want settings hint", got)
	}
}

func TestOpenAIAppShellURLWithoutLocalModelsShowsOpenCSGLoginHint(t *testing.T) {
	s := New(&config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
	}, "test")

	_, err := s.openAIAppShellURL(context.Background(), "codex", "", "", "")
	if err == nil {
		t.Fatal("openAIAppShellURL returned nil error, want OpenCSG login hint")
	}
	if got := err.Error(); !strings.Contains(got, "save an Access Token to use OpenCSG models") {
		t.Fatalf("error = %q, want OpenCSG login hint", got)
	}
}

func TestPrepareAIAppShellLaunchSetsTerminalEnvForClaudeCode(t *testing.T) {
	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "claude")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "claude.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NO_COLOR", "1")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "old-token")

	s := New(&config.Config{ListenAddr: ":11435"}, "test")
	workDir := t.TempDir()
	prepared, err := s.prepareAIAppShellLaunch(aiAppOpenTarget{
		AppID:       "claude-code",
		DisplayName: "Claude Code",
		Binaries:    []string{"claude"},
	}, "Qwen/Qwen3.5-2B", []string{"Qwen/Qwen3.5-2B"}, workDir)
	if err != nil {
		t.Fatalf("prepareAIAppShellLaunch returned error: %v", err)
	}

	for key, want := range map[string]string{
		"TERM":           "xterm-256color",
		"COLORTERM":      "truecolor",
		"FORCE_COLOR":    "1",
		"CLICOLOR":       "1",
		"TERM_PROGRAM":   "csghub-lite",
		"CLAUDE_API_KEY": "csghub-lite",
	} {
		if got := envValue(prepared.Env, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if envHasKey(prepared.Env, "NO_COLOR") {
		t.Fatalf("NO_COLOR should be removed from web shell environment: %#v", prepared.Env)
	}
	if envHasKey(prepared.Env, "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should be removed from web shell environment: %#v", prepared.Env)
	}

	settingsJSON := argValue(prepared.Args, "--settings")
	if settingsJSON == "" {
		t.Fatalf("expected --settings in args: %#v", prepared.Args)
	}
	var payload struct {
		Env         map[string]string `json:"env"`
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(settingsJSON), &payload); err != nil {
		t.Fatalf("decode settings json: %v", err)
	}
	if payload.Permissions.DefaultMode != "acceptEdits" {
		t.Fatalf("permissions.defaultMode = %q, want acceptEdits", payload.Permissions.DefaultMode)
	}
	if _, ok := payload.Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be included in settings json")
	}
}

func TestPrepareAIAppShellLaunchUsesCustomProviderForCodex(t *testing.T) {
	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "codex")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "codex.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := New(&config.Config{ListenAddr: ":11435"}, "test")
	workDir := t.TempDir()
	modelIDs := []string{
		"Qwen/Qwen3.5-2B",
		"afrideva/Qwen2-0.5B-Instruct-GGUF:fh23aijhzx8g",
	}
	prepared, err := s.prepareAIAppShellLaunch(aiAppOpenTarget{
		AppID:       "codex",
		DisplayName: "Codex",
		Binaries:    []string{"codex"},
	}, "Qwen/Qwen3.5-2B", modelIDs, workDir)
	if err != nil {
		t.Fatalf("prepareAIAppShellLaunch returned error: %v", err)
	}

	for _, want := range []string{
		`model_provider="csghub_lite"`,
		`model_providers.csghub_lite.name="OpenCSG"`,
		`model_providers.csghub_lite.base_url="http://127.0.0.1:11435/v1"`,
		`model_providers.csghub_lite.supports_websockets=false`,
	} {
		if !hasConfigOverride(prepared.Args, want) {
			t.Fatalf("missing Codex config override %q in args %#v", want, prepared.Args)
		}
	}
	if !slices.Contains(prepared.Args, "--no-alt-screen") {
		t.Fatalf("expected Codex web shell args to include --no-alt-screen: %#v", prepared.Args)
	}
	if hasConfigPrefix(prepared.Args, "openai_base_url=") {
		t.Fatalf("args = %#v, want custom provider config instead of openai_base_url", prepared.Args)
	}
	catalogValue := configValue(prepared.Args, "model_catalog_json=")
	if catalogValue == "" {
		t.Fatalf("missing model_catalog_json config in args %#v", prepared.Args)
	}
	catalogPath, err := strconv.Unquote(catalogValue)
	if err != nil {
		t.Fatalf("unquote model_catalog_json %q: %v", catalogValue, err)
	}
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read model catalog: %v", err)
	}
	var payload struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
			ShellType  string `json:"shell_type"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode model catalog: %v", err)
	}
	if len(payload.Models) != len(modelIDs) {
		t.Fatalf("model catalog count = %d, want %d", len(payload.Models), len(modelIDs))
	}
	for i, want := range modelIDs {
		if payload.Models[i].Slug != want {
			t.Fatalf("catalog model %d slug = %q, want %q", i, payload.Models[i].Slug, want)
		}
		if payload.Models[i].Visibility != "list" {
			t.Fatalf("catalog model %d visibility = %q, want list", i, payload.Models[i].Visibility)
		}
		if payload.Models[i].ShellType != "shell_command" {
			t.Fatalf("catalog model %d shell_type = %q, want shell_command", i, payload.Models[i].ShellType)
		}
	}
	if envHasKey(prepared.Env, "OPENAI_API_KEY") {
		t.Fatalf("OPENAI_API_KEY should not be set for Codex custom provider: %#v", prepared.Env)
	}
}

func TestPrepareAIAppShellLaunchUsesPiProviderConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "pi")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "pi.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := New(&config.Config{ListenAddr: ":11435"}, "test")
	workDir := t.TempDir()
	prepared, err := s.prepareAIAppShellLaunch(aiAppOpenTarget{
		AppID:       "pi",
		DisplayName: "Pi",
		Binaries:    []string{"pi"},
	}, "Qwen/Qwen3.5-2B", []string{"Qwen/Qwen3.5-2B", "minimax-m2.5"}, workDir)
	if err != nil {
		t.Fatalf("prepareAIAppShellLaunch returned error: %v", err)
	}
	if got := argValue(prepared.Args, "--provider"); got != "csghub-lite" {
		t.Fatalf("--provider = %q, want csghub-lite in args %#v", got, prepared.Args)
	}
	if got := argValue(prepared.Args, "--model"); got != "Qwen/Qwen3.5-2B" {
		t.Fatalf("--model = %q, want selected model in args %#v", got, prepared.Args)
	}
	if envHasKey(prepared.Env, "NO_COLOR") {
		t.Fatalf("NO_COLOR should be removed from Pi web shell environment: %#v", prepared.Env)
	}

	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "models.json"))
	if err != nil {
		t.Fatalf("read Pi models: %v", err)
	}
	var payload struct {
		Providers map[string]struct {
			BaseURL string `json:"baseUrl"`
			Models  []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode Pi models: %v", err)
	}
	provider, ok := payload.Providers["csghub-lite"]
	if !ok {
		t.Fatalf("Pi csghub-lite provider missing: %#v", payload.Providers)
	}
	if provider.BaseURL != "http://127.0.0.1:11435/v1" {
		t.Fatalf("provider baseUrl = %q, want local v1 URL", provider.BaseURL)
	}
	if got := collectOpenClawModelIDs(provider.Models); !sameStrings(got, []string{"Qwen/Qwen3.5-2B", "minimax-m2.5"}) {
		t.Fatalf("Pi model ids = %#v, want launch models", got)
	}
}

func TestWriteOpenCodeWebLaunchConfigIncludesAllModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := writeOpenCodeWebLaunchConfig("http://127.0.0.1:11435", "Qwen/Qwen3.5-2B", []string{
		"Qwen/Qwen3.5-2B",
		"Qwen/Qwen2.5-Coder-1.5B",
	})
	if err != nil {
		t.Fatalf("writeOpenCodeWebLaunchConfig returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	providers, ok := payload["provider"].(map[string]interface{})
	if !ok {
		t.Fatalf("provider field missing or invalid: %#v", payload["provider"])
	}
	provider, ok := providers["csghub-lite"].(map[string]interface{})
	if !ok {
		t.Fatalf("csghub-lite provider missing: %#v", providers)
	}
	models, ok := provider["models"].(map[string]interface{})
	if !ok {
		t.Fatalf("provider models missing: %#v", provider["models"])
	}
	if len(models) != 2 {
		t.Fatalf("models count = %d, want 2 (%#v)", len(models), models)
	}
	if _, ok := models["Qwen/Qwen2.5-Coder-1.5B"]; !ok {
		t.Fatalf("missing coder model in config: %#v", models)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func collectOpenClawModelIDs(items []struct {
	ID string `json:"id"`
}) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(want))
	for _, item := range want {
		seen[item]++
	}
	for _, item := range got {
		seen[item]--
		if seen[item] < 0 {
			return false
		}
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func argValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func hasConfigOverride(args []string, want string) bool {
	for i := 0; i+1 < len(args); i++ {
		if (args[i] == "-c" || args[i] == "--config") && args[i+1] == want {
			return true
		}
	}
	return false
}

func hasConfigPrefix(args []string, prefix string) bool {
	for i := 0; i+1 < len(args); i++ {
		if (args[i] == "-c" || args[i] == "--config") && strings.HasPrefix(args[i+1], prefix) {
			return true
		}
	}
	return false
}

func configValue(args []string, prefix string) string {
	for i := 0; i+1 < len(args); i++ {
		if (args[i] == "-c" || args[i] == "--config") && strings.HasPrefix(args[i+1], prefix) {
			return strings.TrimPrefix(args[i+1], prefix)
		}
	}
	return ""
}
