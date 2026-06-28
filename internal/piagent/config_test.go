package piagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestSyncConfigWritesProviderAndPreservesOtherProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	agentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	existing := `{
  "providers": {
    "custom": {
      "baseUrl": "http://example.test/v1",
      "models": [{"id": "custom-model"}]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing models: %v", err)
	}

	err := SyncConfig("http://127.0.0.1:11435", "test-token", "Qwen/Qwen3.5-2B", []api.ModelInfo{
		{Model: "Qwen/Qwen3.5-2B", ContextWindow: 32768, Source: "local"},
		{Model: "vision/model", HasMMProj: true, Source: "local"},
	})
	if err != nil {
		t.Fatalf("SyncConfig returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if err != nil {
		t.Fatalf("read models config: %v", err)
	}
	var models struct {
		Providers map[string]struct {
			BaseURL string `json:"baseUrl"`
			API     string `json:"api"`
			APIKey  string `json:"apiKey"`
			Compat  struct {
				SupportsDeveloperRole    bool   `json:"supportsDeveloperRole"`
				SupportsReasoningEffort  bool   `json:"supportsReasoningEffort"`
				SupportsUsageInStreaming bool   `json:"supportsUsageInStreaming"`
				MaxTokensField           string `json:"maxTokensField"`
			} `json:"compat"`
			Models []struct {
				ID            string   `json:"id"`
				Managed       bool     `json:"_launch"`
				Input         []string `json:"input"`
				ContextWindow int64    `json:"contextWindow"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatalf("decode models config: %v", err)
	}
	if _, ok := models.Providers["custom"]; !ok {
		t.Fatalf("custom provider was not preserved: %#v", models.Providers)
	}
	provider, ok := models.Providers[ProviderID]
	if !ok {
		t.Fatalf("provider %q missing: %#v", ProviderID, models.Providers)
	}
	if provider.BaseURL != "http://127.0.0.1:11435/v1" {
		t.Fatalf("baseUrl = %q, want local v1 URL", provider.BaseURL)
	}
	if provider.API != ProviderAPI || provider.APIKey != "test-token" {
		t.Fatalf("unexpected provider api fields: %#v", provider)
	}
	if provider.Compat.SupportsDeveloperRole || provider.Compat.SupportsReasoningEffort || provider.Compat.SupportsUsageInStreaming {
		t.Fatalf("compat should disable unsupported OpenAI-compatible extras: %#v", provider.Compat)
	}
	if provider.Compat.MaxTokensField != "max_tokens" {
		t.Fatalf("maxTokensField = %q, want max_tokens", provider.Compat.MaxTokensField)
	}
	if len(provider.Models) != 2 {
		t.Fatalf("model count = %d, want 2", len(provider.Models))
	}
	if provider.Models[0].ID != "Qwen/Qwen3.5-2B" || !provider.Models[0].Managed || provider.Models[0].ContextWindow != 32768 {
		t.Fatalf("unexpected first model: %#v", provider.Models[0])
	}
	if got := provider.Models[1].Input; len(got) != 2 || got[0] != "text" || got[1] != "image" {
		t.Fatalf("vision model input = %#v, want text and image", got)
	}

	data, err = os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings config: %v", err)
	}
	var settings map[string]string
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if settings["defaultProvider"] != ProviderID || settings["defaultModel"] != "Qwen/Qwen3.5-2B" {
		t.Fatalf("unexpected settings: %#v", settings)
	}
}
