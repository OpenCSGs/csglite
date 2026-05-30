package antigravityagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestSyncConfigWritesAntigravityProviderFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	models := []api.ModelInfo{
		{Model: "Qwen/Qwen3.5-2B"},
		{Model: "minimax-m2.5"},
	}
	legacyConfigPath := filepath.Join(home, ".config", "antigravity", "config.toml")
	if err := os.MkdirAll(filepath.Dir(legacyConfigPath), 0o755); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	if err := os.WriteFile(legacyConfigPath, []byte("[[custom_models]]\n"), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	if err := SyncConfig("http://127.0.0.1:11435", "csghub-lite", "Qwen/Qwen3.5-2B", models); err != nil {
		t.Fatalf("SyncConfig returned error: %v", err)
	}
	if _, err := os.Stat(legacyConfigPath); !os.IsNotExist(err) {
		t.Fatalf("legacy config.toml should be removed, stat err = %v", err)
	}

	settingsText, err := os.ReadFile(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings struct {
		Model           string `json:"model"`
		CustomProviders []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			BaseURL string `json:"baseUrl"`
			APIKey  string `json:"apiKey"`
			ModelID string `json:"modelId"`
		} `json:"customProviders"`
	}
	if err := json.Unmarshal(settingsText, &settings); err != nil {
		t.Fatalf("decode settings.json: %v", err)
	}
	if settings.Model != "Qwen/Qwen3.5-2B" {
		t.Fatalf("model = %q, want selected model", settings.Model)
	}
	if len(settings.CustomProviders) != 1 {
		t.Fatalf("customProviders = %#v, want one provider", settings.CustomProviders)
	}
	provider := settings.CustomProviders[0]
	for got, want := range map[string]string{
		provider.Name:    ProviderID,
		provider.Type:    "openai",
		provider.BaseURL: "http://127.0.0.1:11435/v1",
		provider.APIKey:  "csghub-lite",
		provider.ModelID: "Qwen/Qwen3.5-2B",
	} {
		if got != want {
			t.Fatalf("custom provider value = %q, want %q; provider=%#v", got, want, provider)
		}
	}

	if strings.Contains(string(settingsText), `"env"`) {
		t.Fatalf("settings.json should not include legacy env block:\n%s", settingsText)
	}
}
