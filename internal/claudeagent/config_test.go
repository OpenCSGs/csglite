package claudeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncConfigWritesEnvSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	err := SyncConfig("http://127.0.0.1:11435", "test-token", "glm-5(infini-ai)")
	if err != nil {
		t.Fatalf("SyncConfig returned error: %v", err)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings struct {
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	if settings.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:11435" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want http://127.0.0.1:11435", settings.Env["ANTHROPIC_BASE_URL"])
	}
	if settings.Model != "glm-5(infini-ai)" {
		t.Fatalf("model = %q, want glm-5(infini-ai)", settings.Model)
	}
	if settings.Env["ANTHROPIC_API_KEY"] != "test-token" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want test-token", settings.Env["ANTHROPIC_API_KEY"])
	}
	if settings.Env["CLAUDE_API_BASE_URL"] != "http://127.0.0.1:11435" {
		t.Fatalf("CLAUDE_API_BASE_URL = %q, want http://127.0.0.1:11435", settings.Env["CLAUDE_API_BASE_URL"])
	}
	if settings.Env["CLAUDE_CODE_ATTRIBUTION_HEADER"] != "0" {
		t.Fatalf("CLAUDE_CODE_ATTRIBUTION_HEADER = %q, want 0", settings.Env["CLAUDE_CODE_ATTRIBUTION_HEADER"])
	}
	if _, ok := settings.Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be written with ANTHROPIC_API_KEY")
	}

	claudeJSON := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatalf("read %s: %v", claudeJSON, err)
	}
	var state map[string]interface{}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode claude.json: %v", err)
	}
	if v, ok := state["hasCompletedOnboarding"].(bool); !ok || !v {
		t.Fatalf("hasCompletedOnboarding = %#v, want true", state["hasCompletedOnboarding"])
	}
}

func TestSyncConfigPreservesClaudeDotJSONFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	existing := `{"installMethod": "native", "autoUpdates": false}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write claude.json: %v", err)
	}

	if err := SyncConfig("http://127.0.0.1:11435", "test-token", "x"); err != nil {
		t.Fatalf("SyncConfig: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read claude.json: %v", err)
	}
	var state map[string]interface{}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if state["installMethod"] != "native" {
		t.Fatalf("installMethod = %#v, want native", state["installMethod"])
	}
	if v, ok := state["autoUpdates"].(bool); !ok || v {
		t.Fatalf("autoUpdates = %#v, want false", state["autoUpdates"])
	}
	if v, ok := state["hasCompletedOnboarding"].(bool); !ok || !v {
		t.Fatalf("hasCompletedOnboarding = %#v, want true", state["hasCompletedOnboarding"])
	}
}

func TestSyncConfigPreservesExistingSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	existing := `{"theme": "dark", "env": {"OTHER_VAR": "other-value", "ANTHROPIC_AUTH_TOKEN": "old-token"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing settings: %v", err)
	}

	err := SyncConfig("http://127.0.0.1:11435", "test-token", "glm-5(infini-ai)")
	if err != nil {
		t.Fatalf("SyncConfig returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings struct {
		Theme string            `json:"theme"`
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	if settings.Theme != "dark" {
		t.Fatalf("theme = %q, want dark", settings.Theme)
	}
	if settings.Env["OTHER_VAR"] != "other-value" {
		t.Fatalf("OTHER_VAR = %q, want other-value", settings.Env["OTHER_VAR"])
	}
	if settings.Model != "glm-5(infini-ai)" {
		t.Fatalf("model = %q, want glm-5(infini-ai)", settings.Model)
	}
	if settings.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:11435" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want http://127.0.0.1:11435", settings.Env["ANTHROPIC_BASE_URL"])
	}
	if _, ok := settings.Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should be removed from existing settings")
	}
}
