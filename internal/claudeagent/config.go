package claudeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/safefile"
)

const (
	ProviderID = "csghub-lite"
)

// SyncConfig persists Claude Code settings to ~/.claude/settings.json
// so that subsequent launches without csghub-lite will still use the
// configured API endpoint.
func SyncConfig(serverURL, apiKey, modelID string) error {
	settingsPath, err := SettingsPath()
	if err != nil {
		return err
	}

	if err := syncJSONFile(settingsPath, func(doc map[string]interface{}) {
		if modelID = strings.TrimSpace(modelID); modelID != "" {
			doc["model"] = modelID
		}
		env := ensureObject(doc, "env")
		env["ANTHROPIC_BASE_URL"] = strings.TrimRight(serverURL, "/")
		env["ANTHROPIC_API_KEY"] = strings.TrimSpace(apiKey)
		delete(env, "ANTHROPIC_AUTH_TOKEN")
		env["CLAUDE_API_BASE_URL"] = strings.TrimRight(serverURL, "/")
		env["CLAUDE_API_KEY"] = strings.TrimSpace(apiKey)
		env["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0"
	}); err != nil {
		return err
	}

	// Claude Code 2.0.65+ can ignore ANTHROPIC_BASE_URL from settings until
	// onboarding completes, and instead calls api.anthropic.com (breaking
	// gateway-only setups). Mark onboarding complete in ~/.claude.json.
	// See anthropics/claude-code#13827 and #26935.
	return syncClaudeDotJSONOnboardingComplete()
}

func IsManagedConfigData(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return false
	}
	env, _ := doc["env"].(map[string]interface{})
	return envString(env, "ANTHROPIC_API_KEY") == "csghub-lite" ||
		envString(env, "CLAUDE_API_KEY") == "csghub-lite"
}

// RemoveManagedConfig removes settings written by legacy csghub-lite launches
// when no exact pre-switch snapshot is available.
func RemoveManagedConfig() error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	if !IsManagedConfigData(data) {
		return nil
	}

	delete(doc, "model")
	if env, ok := doc["env"].(map[string]interface{}); ok {
		for _, key := range []string{
			"ANTHROPIC_BASE_URL",
			"ANTHROPIC_API_KEY",
			"CLAUDE_API_BASE_URL",
			"CLAUDE_API_KEY",
			"CLAUDE_CODE_ATTRIBUTION_HEADER",
		} {
			delete(env, key)
		}
		if len(env) == 0 {
			delete(doc, "env")
		}
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return safefile.Write(path, out, 0o644)
}

func envString(env map[string]interface{}, key string) string {
	if env == nil {
		return ""
	}
	value, _ := env[key].(string)
	return strings.TrimSpace(value)
}

// SettingsPath returns the path to Claude Code's settings.json.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func syncJSONFile(path string, mutate func(map[string]interface{})) error {
	doc := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			// If the file is malformed, start fresh
			doc = map[string]interface{}{}
		}
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
	return safefile.Write(path, data, 0o644)
}

func ensureObject(parent map[string]interface{}, key string) map[string]interface{} {
	if child, ok := parent[key].(map[string]interface{}); ok {
		return child
	}
	child := map[string]interface{}{}
	parent[key] = child
	return child
}

func syncClaudeDotJSONOnboardingComplete() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude.json")

	data, readErr := os.ReadFile(path)
	doc := map[string]interface{}{}
	if readErr == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			// Avoid overwriting a malformed file; settings + launch env still apply.
			return nil
		}
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}

	if v, ok := doc["hasCompletedOnboarding"].(bool); ok && v {
		return nil
	}
	doc["hasCompletedOnboarding"] = true

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return safefile.Write(path, out, 0o644)
}
