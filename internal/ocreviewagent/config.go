package ocreviewagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const ProviderID = "csghub-lite"

var UnsetEnvKeys = []string{
	"OCR_LLM_URL",
	"OCR_LLM_TOKEN",
	"OCR_LLM_MODEL",
	"OCR_LLM_AUTH_HEADER",
	"OCR_LLM_EXTRA_HEADERS",
	"OCR_USE_ANTHROPIC",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_API_KEY",
}

// SyncConfig writes Open Code Review config into the official user config path
// so direct `ocr` invocations and csghub-lite launches share the same provider.
func SyncConfig(storageRoot, serverURL, apiKey, selectedModelID string, models []api.ModelInfo) (string, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", fmt.Errorf("creating Open Code Review home: %w", err)
	}
	if err := os.MkdirAll(TempDir(storageRoot), 0o755); err != nil {
		return "", fmt.Errorf("creating Open Code Review temp dir: %w", err)
	}

	modelIDs := normalizeModelIDs(selectedModelID, models)
	if strings.TrimSpace(selectedModelID) == "" && len(modelIDs) > 0 {
		selectedModelID = modelIDs[0]
	}

	configPath := filepath.Join(configDir, "config.json")
	payload, err := readConfig(configPath)
	if err != nil {
		return "", err
	}
	payload["provider"] = ProviderID
	payload["model"] = strings.TrimSpace(selectedModelID)
	payload["custom_providers"] = mergeProviderConfig(payload["custom_providers"], map[string]interface{}{
		ProviderID: map[string]interface{}{
			"name":        "OpenCSG",
			"url":         strings.TrimRight(serverURL, "/") + "/v1",
			"protocol":    "openai",
			"api_key":     strings.TrimSpace(apiKey),
			"auth_header": "authorization",
			"model":       strings.TrimSpace(selectedModelID),
			"models":      modelIDs,
		},
	})
	payload["telemetry"] = map[string]interface{}{
		"enabled":         false,
		"content_logging": false,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')

	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return "", fmt.Errorf("writing Open Code Review config: %w", err)
	}
	return configPath, nil
}

func EnvOverrides(storageRoot string) map[string]string {
	tempDir := TempDir(storageRoot)
	return map[string]string{
		"TMPDIR":        tempDir,
		"TMP":           tempDir,
		"TEMP":          tempDir,
		"OCR_NO_UPDATE": "1",
	}
}

func HomeDir(storageRoot string) string {
	return filepath.Join(filepath.Clean(storageRoot), "apps", "open-code-review", "home")
}

func ConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving user home for Open Code Review config: %w", err)
	}
	if strings.TrimSpace(homeDir) == "" {
		return "", fmt.Errorf("resolving user home for Open Code Review config: empty home directory")
	}
	return filepath.Join(homeDir, ".opencodereview"), nil
}

func TempDir(storageRoot string) string {
	return filepath.Join(config.TempDirForStorage(storageRoot), "open-code-review")
}

func DefaultArgs(args []string) []string {
	if len(args) > 0 {
		return append([]string{}, args...)
	}
	return []string{"review", "--audience", "human"}
}

func normalizeModelIDs(selected string, models []api.ModelInfo) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(models)+1)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	add(selected)
	for _, model := range models {
		add(model.Model)
	}
	return out
}

func readConfig(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("reading Open Code Review config: %w", err)
	}
	payload := map[string]interface{}{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		backupPath := path + ".malformed.bak"
		_ = os.Rename(path, backupPath)
		return map[string]interface{}{}, nil
	}
	return payload, nil
}

func mergeProviderConfig(existing interface{}, updates map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	if providers, ok := existing.(map[string]interface{}); ok {
		for key, value := range providers {
			out[key] = value
		}
	}
	for key, value := range updates {
		provider, ok := value.(map[string]interface{})
		if !ok {
			out[key] = value
			continue
		}
		merged := map[string]interface{}{}
		if existingProvider, ok := out[key].(map[string]interface{}); ok {
			for field, existingValue := range existingProvider {
				merged[field] = existingValue
			}
		}
		for field, updatedValue := range provider {
			merged[field] = updatedValue
		}
		out[key] = merged
	}
	return out
}
