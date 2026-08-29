package dshagent

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/opencsgs/csglite/pkg/api"
)

const (
	ProviderID  = "csghub_lite"
	ProviderAPI = "openai-completions"

	// apiKeyEnv is the environment variable name that dsh resolves at
	// request time. The actual secret is stored in ~/.dsh/.credentials.yaml.
	apiKeyEnv = "CSGHUB_LITE_API_KEY"
)

// SyncConfig writes DeepSeek Harness configuration to
// ~/.dsh/settings.yaml so subsequent launches use csghub-lite as the model
// provider. dsh reads settings.yaml for providers and models; the
// credentials are stored separately in .credentials.yaml as env-var
// references, never inline.
func SyncConfig(serverURL, apiKey, selectedModelID string, models []api.ModelInfo) error {
	if strings.TrimSpace(selectedModelID) == "" && len(models) > 0 {
		selectedModelID = models[0].Model
	}

	configPath, err := SettingsPath()
	if err != nil {
		return err
	}

	// Read existing config to preserve user settings.
	existing := make(map[string]interface{})
	if data, err := os.ReadFile(configPath); err == nil && len(data) > 0 {
		_ = yaml.Unmarshal(data, existing)
	}

	baseURL := strings.TrimRight(serverURL, "/") + "/v1"
	provider := map[string]interface{}{
		"apiKeyEnv": apiKeyEnv,
		"api":       ProviderAPI,
		"baseURL":   baseURL,
		"models":    buildModelEntries(models),
	}

	// Set the provider under llm-pi-ai.providers.<id>.
	llmPiAI := ensureMap(existing, "llm-pi-ai")
	providers := ensureMap(llmPiAI, "providers")
	providers[ProviderID] = provider

	// Set default model.
	llmPiAI["default_model"] = selectedModelID

	data, err := yaml.Marshal(existing)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return err
	}

	// Write the actual API key into .credentials.yaml so dsh can resolve it
	// via the apiKeyEnv reference.
	return writeCredentials(apiKey)
}

// ModelAlias sanitizes a model ID into a valid key for dsh's model entries.
// dsh looks up models by their id, so the --model flag and default_model
// must use the raw model ID (dsh does not require aliasing like kimi-code).
func ModelAlias(modelID string) string {
	return modelID
}

// SettingsPath returns the path to dsh settings.yaml.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dsh", "settings.yaml"), nil
}

// ConfigDir returns the dsh data directory.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dsh"), nil
}

type modelEntry struct {
	ID    string   `yaml:"id"`
	Input []string `yaml:"input,omitempty"`
}

func buildModelEntries(models []api.ModelInfo) []modelEntry {
	entries := make([]modelEntry, 0, len(models))
	for _, item := range models {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			continue
		}
		input := []string{"text"}
		if item.HasMMProj || strings.EqualFold(strings.TrimSpace(item.PipelineTag), "image-text-to-text") {
			input = append(input, "image")
		}
		entries = append(entries, modelEntry{
			ID:    modelID,
			Input: input,
		})
	}
	return entries
}

func ensureMap(parent map[string]interface{}, key string) map[string]interface{} {
	if child, ok := parent[key].(map[string]interface{}); ok {
		return child
	}
	child := map[string]interface{}{}
	parent[key] = child
	return child
}

func writeCredentials(apiKey string) error {
	credPath, err := CredentialsPath()
	if err != nil {
		return err
	}

	existing := make(map[string]interface{})
	if data, err := os.ReadFile(credPath); err == nil && len(data) > 0 {
		_ = yaml.Unmarshal(data, existing)
	}

	existing[apiKeyEnv] = strings.TrimSpace(apiKey)

	data, err := yaml.Marshal(existing)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(credPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(credPath, data, 0o600)
}

// CredentialsPath returns the path to dsh .credentials.yaml.
func CredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dsh", ".credentials.yaml"), nil
}

// ConfigPath is an alias for SettingsPath for interface compatibility.
func ConfigPath() (string, error) {
	return SettingsPath()
}
