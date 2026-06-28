package ocreviewagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestSyncConfigWritesOfficialUserConfigAndPreservesExistingProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configDir := filepath.Join(home, ".opencodereview")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	existing := []byte(`{
  "provider": "other",
  "custom_providers": {
    "existing": {
      "name": "Existing",
      "url": "https://example.invalid/v1"
    },
    "csghub-lite": {
      "extra_headers": "X-Test=1",
      "extra_body": {
        "trace": true
      },
      "model": "old-model"
    }
  },
  "kept": true
}`)
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), existing, 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	configPath, err := SyncConfig(t.TempDir(), "http://127.0.0.1:11435", "test-token", "glm-5.1-1", []api.ModelInfo{
		{Model: "glm-5.1-1"},
		{Model: "Qwen/Qwen3.5-2B"},
	})
	if err != nil {
		t.Fatalf("SyncConfig returned error: %v", err)
	}
	if configPath != filepath.Join(configDir, "config.json") {
		t.Fatalf("config path = %q, want official user config path", configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var payload struct {
		Provider        string                 `json:"provider"`
		Model           string                 `json:"model"`
		Language        string                 `json:"language"`
		Kept            bool                   `json:"kept"`
		CustomProviders map[string]interface{} `json:"custom_providers"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if payload.Provider != ProviderID || payload.Model != "glm-5.1-1" || !payload.Kept {
		t.Fatalf("unexpected top-level config: %#v", payload)
	}
	if payload.Language != DefaultLanguage {
		t.Fatalf("language = %q, want %q", payload.Language, DefaultLanguage)
	}
	if _, ok := payload.CustomProviders["existing"]; !ok {
		t.Fatalf("existing provider was not preserved: %#v", payload.CustomProviders)
	}
	provider, ok := payload.CustomProviders[ProviderID].(map[string]interface{})
	if !ok {
		t.Fatalf("missing csghub-lite provider: %#v", payload.CustomProviders)
	}
	if provider["url"] != "http://127.0.0.1:11435/v1" || provider["api_key"] != "test-token" || provider["model"] != "glm-5.1-1" {
		t.Fatalf("unexpected csghub-lite provider: %#v", provider)
	}
	if provider["extra_headers"] != "X-Test=1" {
		t.Fatalf("custom provider field was not preserved: %#v", provider)
	}
	if extraBody, ok := provider["extra_body"].(map[string]interface{}); !ok || extraBody["trace"] != true {
		t.Fatalf("custom provider object was not preserved: %#v", provider["extra_body"])
	}
	models, ok := provider["models"].([]interface{})
	if !ok || len(models) != 2 || models[0] != "glm-5.1-1" {
		t.Fatalf("unexpected model list: %#v", provider["models"])
	}
}

func TestSyncConfigPreservesExistingLanguage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configDir := filepath.Join(home, ".opencodereview")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"language":"English"}`), 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	if _, err := SyncConfig(t.TempDir(), "http://127.0.0.1:11435", "test-token", "glm-5.1-1", nil); err != nil {
		t.Fatalf("SyncConfig returned error: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var payload struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if payload.Language != "English" {
		t.Fatalf("language = %q, want existing language", payload.Language)
	}
}

func TestSyncConfigBacksUpMalformedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configDir := filepath.Join(home, ".opencodereview")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"provider":`), 0o600); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}

	gotPath, err := SyncConfig(t.TempDir(), "http://127.0.0.1:11435", "test-token", "glm-5.1-1", nil)
	if err != nil {
		t.Fatalf("SyncConfig returned error: %v", err)
	}
	if gotPath != configPath {
		t.Fatalf("config path = %q, want %q", gotPath, configPath)
	}
	if _, err := os.Stat(configPath + ".malformed.bak"); err != nil {
		t.Fatalf("expected malformed config backup: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read repaired config: %v", err)
	}
	var payload struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode repaired config: %v", err)
	}
	if payload.Provider != ProviderID || payload.Model != "glm-5.1-1" {
		t.Fatalf("unexpected repaired config: %#v", payload)
	}
}
