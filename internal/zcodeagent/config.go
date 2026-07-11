package zcodeagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ProviderID           = "csghub-lite"
	defaultContextWindow = int64(128000)
	defaultOutputLimit   = int64(16384)
)

type providerConfig struct {
	Name    string                 `json:"name"`
	Kind    string                 `json:"kind"`
	Options providerOptions        `json:"options"`
	Source  string                 `json:"source"`
	Models  map[string]modelConfig `json:"models"`
}

type providerOptions struct {
	BaseURL        string `json:"baseURL"`
	APIKey         string `json:"apiKey"`
	APIKeyRequired bool   `json:"apiKeyRequired"`
}

type modelConfig struct {
	Name       string          `json:"name"`
	Limit      modelLimit      `json:"limit"`
	Modalities modelModalities `json:"modalities"`
}

type modelLimit struct {
	Context int64 `json:"context"`
	Output  int64 `json:"output"`
}

type modelModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// SyncConfig registers csghub-lite as an OpenAI-compatible provider in the
// ZCode desktop configuration while preserving all unrelated user settings.
func SyncConfig(serverURL, apiKey, selectedModelID string, modelIDs []string) error {
	configPath, err := ConfigPath()
	if err != nil {
		return err
	}

	root := make(map[string]json.RawMessage)
	if data, readErr := os.ReadFile(configPath); readErr == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse ZCode config: %w", err)
		}
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read ZCode config: %w", readErr)
	}

	providers := make(map[string]json.RawMessage)
	if raw := root["provider"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &providers); err != nil {
			return fmt.Errorf("parse ZCode providers: %w", err)
		}
	}

	selectedModelID = strings.TrimSpace(selectedModelID)
	models := make(map[string]modelConfig, 1)
	for _, rawID := range modelIDs {
		modelID := strings.TrimSpace(rawID)
		if modelID == "" || (selectedModelID != "" && modelID != selectedModelID) {
			continue
		}
		models[modelID] = modelConfig{
			Name: modelID,
			Limit: modelLimit{
				Context: defaultContextWindow,
				Output:  defaultOutputLimit,
			},
			Modalities: modelModalities{
				Input:  []string{"text"},
				Output: []string{"text"},
			},
		}
		if selectedModelID == "" {
			selectedModelID = modelID
			break
		}
	}
	if selectedModelID != "" {
		if _, ok := models[selectedModelID]; !ok {
			models[selectedModelID] = modelConfig{
				Name: selectedModelID,
				Limit: modelLimit{
					Context: defaultContextWindow,
					Output:  defaultOutputLimit,
				},
				Modalities: modelModalities{
					Input:  []string{"text"},
					Output: []string{"text"},
				},
			}
		}
	}
	if len(models) == 0 {
		return fmt.Errorf("no models available for ZCode")
	}

	providerData, err := json.Marshal(providerConfig{
		Name: "CSGLite",
		Kind: "openai-compatible",
		Options: providerOptions{
			BaseURL:        strings.TrimRight(strings.TrimSpace(serverURL), "/") + "/v1",
			APIKey:         strings.TrimSpace(apiKey),
			APIKeyRequired: true,
		},
		Source: "custom",
		Models: models,
	})
	if err != nil {
		return err
	}
	providers[ProviderID] = providerData
	providersData, err := json.Marshal(providers)
	if err != nil {
		return err
	}
	root["provider"] = providersData

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeConfigAtomically(configPath, data, 0o600); err != nil {
		return err
	}
	return syncSelectedModel(selectedModelID)
}

// ConfigPath returns the ZCode desktop model configuration path.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zcode", "v2", "config.json"), nil
}

func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zcode", "v2", "setting.json"), nil
}

func syncSelectedModel(modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	path, err := SettingsPath()
	if err != nil {
		return err
	}

	root := make(map[string]json.RawMessage)
	mode := os.FileMode(0o600)
	if data, readErr := os.ReadFile(path); readErr == nil && len(data) > 0 {
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse ZCode settings: %w", err)
		}
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read ZCode settings: %w", readErr)
	}

	selected := make(map[string]string)
	if raw := root["modelProviderFamilySelectedKeys"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &selected); err != nil {
			return fmt.Errorf("parse ZCode selected models: %w", err)
		}
	}
	selectionKey := ProviderID + "/" + modelID + "#"
	// ZCode keeps separate selections for its global and China provider
	// families. Point both at the explicitly selected local model so either
	// product domain opens with the model chosen in csghub-lite.
	selected["zai"] = selectionKey
	selected["bigmodel"] = selectionKey

	selectedData, err := json.Marshal(selected)
	if err != nil {
		return err
	}
	root["modelProviderFamilySelectedKeys"] = selectedData
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return writeConfigAtomically(path, append(data, '\n'), mode)
}

func writeConfigAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
