package opencodeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
)

const (
	ProviderID = "csghub-lite"
)

// SyncConfig writes OpenCode configuration to ~/.opencode.json
// so subsequent launches use csghub-lite as the model provider.
func SyncConfig(serverURL, apiKey, selectedModelID string, models []api.ModelInfo) error {
	if strings.TrimSpace(selectedModelID) == "" && len(models) > 0 {
		selectedModelID = models[0].Model
	}

	configPath, err := ConfigPath()
	if err != nil {
		return err
	}

	modelMap := make(map[string]interface{}, len(models))
	for _, m := range models {
		modelID := strings.TrimSpace(m.Model)
		if modelID == "" {
			continue
		}
		modelMap[modelID] = map[string]interface{}{
			"name": modelID,
		}
	}

	payload := map[string]interface{}{
		"$schema":           "https://opencode.ai/config.json",
		"enabled_providers": []string{ProviderID},
		"provider": map[string]interface{}{
			ProviderID: map[string]interface{}{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "OpenCSG",
				"options": map[string]interface{}{
					"baseURL": strings.TrimRight(serverURL, "/") + "/v1",
				},
				"models": modelMap,
			},
		},
		"model":       ProviderID + "/" + strings.TrimSpace(selectedModelID),
		"small_model": ProviderID + "/" + strings.TrimSpace(selectedModelID),
	}

	// Read existing config to preserve user settings
	if data, err := os.ReadFile(configPath); err == nil && len(data) > 0 {
		var existing map[string]interface{}
		if json.Unmarshal(data, &existing) == nil {
			// Preserve any additional keys not set by us
			for key, value := range existing {
				if key == "$schema" || key == "enabled_providers" || key == "provider" || key == "model" || key == "small_model" {
					continue
				}
				payload[key] = value
			}
		}
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0o644)
}

// ConfigPath returns the path to OpenCode config file.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".opencode.json"), nil
}
