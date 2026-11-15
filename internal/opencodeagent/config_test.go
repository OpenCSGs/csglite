package opencodeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestSyncConfigWritesOpenCodeGlobalConfig(t *testing.T) {
	for _, customConfigHome := range []bool{false, true} {
		name := "default"
		if customConfigHome {
			name = "xdg"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			configHome := filepath.Join(home, ".config")
			if customConfigHome {
				configHome = t.TempDir()
				t.Setenv("XDG_CONFIG_HOME", configHome)
			}
			configPath := filepath.Join(configHome, "opencode", "opencode.json")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(`{"username":"existing-user","model":"opencode/big-pickle"}`), 0o644); err != nil {
				t.Fatal(err)
			}

			if err := SyncConfig("http://localhost:11435/providers/csghub/", "test-key", "qwen3.8-max", []api.ModelInfo{
				{Model: "deepseek-v4.1-flash-1017"},
				{Model: "qwen3.8-max"},
			}); err != nil {
				t.Fatalf("SyncConfig: %v", err)
			}

			data, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Username         string   `json:"username"`
				Model            string   `json:"model"`
				SmallModel       string   `json:"small_model"`
				EnabledProviders []string `json:"enabled_providers"`
				Provider         map[string]struct {
					Options struct {
						BaseURL string `json:"baseURL"`
					} `json:"options"`
					Models map[string]json.RawMessage `json:"models"`
				} `json:"provider"`
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Username != "existing-user" {
				t.Fatalf("username = %q, want existing user setting preserved", got.Username)
			}
			if got.Model != "csghub-lite/qwen3.8-max" || got.SmallModel != got.Model {
				t.Fatalf("models = %q, %q, want selected csghub-lite model", got.Model, got.SmallModel)
			}
			if len(got.EnabledProviders) != 1 || got.EnabledProviders[0] != ProviderID {
				t.Fatalf("enabled_providers = %v, want only csghub-lite", got.EnabledProviders)
			}
			provider := got.Provider[ProviderID]
			if provider.Options.BaseURL != "http://localhost:11435/providers/csghub/v1" || len(provider.Models) != 2 {
				t.Fatalf("unexpected provider config: %+v", provider)
			}
			if _, err := os.Stat(filepath.Join(home, ".opencode.json")); !os.IsNotExist(err) {
				t.Fatalf("legacy config should not be written, stat error: %v", err)
			}
		})
	}
}
