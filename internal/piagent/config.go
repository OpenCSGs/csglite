package piagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
)

const (
	ProviderID  = "csghub-lite"
	ProviderAPI = "openai-completions"
)

type modelEntry struct {
	ID            string       `json:"id"`
	Managed       bool         `json:"_launch"`
	Input         []string     `json:"input,omitempty"`
	ContextWindow int64        `json:"contextWindow,omitempty"`
	Compat        *compatEntry `json:"compat,omitempty"`
}

type compatEntry struct {
	SupportsDeveloperRole    bool   `json:"supportsDeveloperRole"`
	SupportsReasoningEffort  bool   `json:"supportsReasoningEffort"`
	SupportsUsageInStreaming bool   `json:"supportsUsageInStreaming"`
	MaxTokensField           string `json:"maxTokensField"`
}

// SyncConfig wires Pi to the csghub-lite OpenAI-compatible endpoint while
// preserving unrelated user-managed Pi providers and settings.
func SyncConfig(serverURL, apiKey, selectedModelID string, models []api.ModelInfo) error {
	modelIDs := make([]string, 0, len(models))
	modelByID := make(map[string]api.ModelInfo, len(models))
	for _, item := range models {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			continue
		}
		if _, ok := modelByID[modelID]; ok {
			continue
		}
		modelIDs = append(modelIDs, modelID)
		modelByID[modelID] = item
	}
	if len(modelIDs) == 0 && strings.TrimSpace(selectedModelID) != "" {
		modelIDs = append(modelIDs, strings.TrimSpace(selectedModelID))
	}
	if len(modelIDs) == 0 {
		return fmt.Errorf("syncing Pi config: no models available")
	}

	if strings.TrimSpace(selectedModelID) == "" {
		selectedModelID = modelIDs[0]
	}

	modelsPath, err := ModelsPath()
	if err != nil {
		return err
	}
	if err := syncJSONFile(modelsPath, func(doc map[string]interface{}) {
		providers := ensureObject(doc, "providers")
		providers[ProviderID] = map[string]interface{}{
			"baseUrl": strings.TrimRight(serverURL, "/") + "/v1",
			"api":     ProviderAPI,
			"apiKey":  strings.TrimSpace(apiKey),
			"compat": map[string]interface{}{
				"supportsDeveloperRole":    false,
				"supportsReasoningEffort":  false,
				"supportsUsageInStreaming": false,
				"maxTokensField":           "max_tokens",
			},
			"models": buildModelEntries(modelIDs, modelByID),
		}
	}); err != nil {
		return err
	}

	settingsPath, err := SettingsPath()
	if err != nil {
		return err
	}
	return syncJSONFile(settingsPath, func(doc map[string]interface{}) {
		doc["defaultProvider"] = ProviderID
		doc["defaultModel"] = strings.TrimSpace(selectedModelID)
	})
}

func ModelsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent", "models.json"), nil
}

func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent", "settings.json"), nil
}

func buildModelEntries(modelIDs []string, modelByID map[string]api.ModelInfo) []modelEntry {
	entries := make([]modelEntry, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		item := modelByID[modelID]
		input := []string{"text"}
		if item.HasMMProj || strings.EqualFold(strings.TrimSpace(item.PipelineTag), "image-text-to-text") {
			input = append(input, "image")
		}
		entry := modelEntry{
			ID:      modelID,
			Managed: true,
			Input:   input,
		}
		if item.ContextWindow > 0 {
			entry.ContextWindow = item.ContextWindow
		}
		entries = append(entries, entry)
	}
	return entries
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

func ensureObject(parent map[string]interface{}, key string) map[string]interface{} {
	if child, ok := parent[key].(map[string]interface{}); ok {
		return child
	}
	child := map[string]interface{}{}
	parent[key] = child
	return child
}
