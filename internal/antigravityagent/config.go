package antigravityagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

const (
	ProviderID = "LiteLLM"
	APIKeyEnv  = "CSGHUB_LITE_API_KEY"
)

// SyncConfig writes Antigravity CLI settings so launches use csghub-lite as the
// OpenAI-compatible model provider.
func SyncConfig(serverURL, apiKey, selectedModelID string, models []api.ModelInfo) error {
	modelIDs := modelIDsFromInfos(models, selectedModelID)
	if len(modelIDs) == 0 {
		return fmt.Errorf("syncing Antigravity config: no models available")
	}
	if strings.TrimSpace(selectedModelID) == "" {
		selectedModelID = modelIDs[0]
	}

	baseURL := strings.TrimRight(serverURL, "/") + "/v1"
	_ = removeLegacyConfigTOML()
	return writeSettingsJSON(baseURL, strings.TrimSpace(apiKey), strings.TrimSpace(selectedModelID))
}

// EnvOverrides returns process environment variables that force Antigravity to
// use the csghub-lite gateway instead of falling back to its own auth flow.
func EnvOverrides(serverURL, apiKey, selectedModelID string) map[string]string {
	baseURL := strings.TrimRight(serverURL, "/") + "/v1"
	apiKey = strings.TrimSpace(apiKey)

	return map[string]string{
		APIKeyEnv:         apiKey,
		"OPENAI_API_KEY":  apiKey,
		"OPENAI_BASE_URL": baseURL,
	}
}

func modelIDsFromInfos(models []api.ModelInfo, selectedModelID string) []string {
	seen := make(map[string]struct{}, len(models)+1)
	out := make([]string, 0, len(models)+1)
	for _, item := range models {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		out = append(out, modelID)
	}
	if selectedModelID = strings.TrimSpace(selectedModelID); selectedModelID != "" {
		if _, ok := seen[selectedModelID]; !ok {
			out = append([]string{selectedModelID}, out...)
		}
	}
	return out
}

func removeLegacyConfigTOML() error {
	configPath, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeSettingsJSON(baseURL, apiKey, selectedModelID string) error {
	settingsPath, err := SettingsPath()
	if err != nil {
		return err
	}
	return syncJSONFile(settingsPath, func(doc map[string]interface{}) {
		doc["model"] = selectedModelID
		doc["customProviders"] = []map[string]interface{}{
			{
				"name":    ProviderID,
				"type":    "openai",
				"baseUrl": baseURL,
				"apiKey":  apiKey,
				"modelId": selectedModelID,
			},
		}
		delete(doc, "env")
	})
}

// ConfigPath returns ~/.config/antigravity/config.toml.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "antigravity", "config.toml"), nil
}

// SettingsPath returns ~/.gemini/antigravity-cli/settings.json.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), nil
}

func syncJSONFile(path string, mutate func(map[string]interface{})) error {
	doc := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &doc)
	}
	mutate(doc)

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
