package zcodeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSyncConfigPreservesExistingProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".zcode", "v2", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	existing := `{
  "provider": {
    "existing-provider": {
      "name": "Existing",
      "options": {"apiKey": "keep-me"}
    }
  },
  "theme": "dark"
}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}
	settingsPath := filepath.Join(home, ".zcode", "v2", "setting.json")
	settings := `{
  "modelProviderFamilySelectedKeys": {
    "zai": "existing/model#",
    "other-family": "keep/model#"
  },
  "otherSetting": true
}`
	if err := os.WriteFile(settingsPath, []byte(settings), 0o640); err != nil {
		t.Fatalf("write existing settings: %v", err)
	}

	if err := SyncConfig("http://127.0.0.1:11435/", "test-token", "Qwen3.5-2B", []string{"Qwen3.5-2B", "GLM-4.7"}); err != nil {
		t.Fatalf("SyncConfig() error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	var theme string
	if err := json.Unmarshal(root["theme"], &theme); err != nil || theme != "dark" {
		t.Fatalf("theme = %q, err=%v; want dark", theme, err)
	}
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(root["provider"], &providers); err != nil {
		t.Fatalf("parse providers: %v", err)
	}
	if _, ok := providers["existing-provider"]; !ok {
		t.Fatal("existing provider was removed")
	}
	var got providerConfig
	if err := json.Unmarshal(providers[ProviderID], &got); err != nil {
		t.Fatalf("parse csghub-lite provider: %v", err)
	}
	if got.Kind != "openai-compatible" || got.Options.BaseURL != "http://127.0.0.1:11435/v1" {
		t.Fatalf("provider = %#v", got)
	}
	if got.Name != "CSGLite" {
		t.Fatalf("provider name = %q, want CSGLite", got.Name)
	}
	if got.Options.APIKey != "test-token" || !got.Options.APIKeyRequired {
		t.Fatal("provider API credentials were not configured")
	}
	if len(got.Models) != 1 {
		t.Fatalf("models = %d, want only the selected model", len(got.Models))
	}
	if got.Models["Qwen3.5-2B"].Name != "Qwen3.5-2B" {
		t.Fatal("model display name was not written")
	}
	if _, ok := got.Models["GLM-4.7"]; ok {
		t.Fatal("unselected model should not be written")
	}

	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var gotSettings struct {
		Selected map[string]string `json:"modelProviderFamilySelectedKeys"`
		Other    bool              `json:"otherSetting"`
	}
	if err := json.Unmarshal(settingsData, &gotSettings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	wantSelection := ProviderID + "/Qwen3.5-2B#"
	if gotSettings.Selected["zai"] != wantSelection ||
		gotSettings.Selected["bigmodel"] != wantSelection ||
		gotSettings.Selected["other-family"] != "keep/model#" ||
		!gotSettings.Other {
		t.Fatalf("settings = %#v, want selected model and preserved fields", gotSettings)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(settingsPath)
		if err != nil {
			t.Fatalf("stat settings: %v", err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("settings mode = %v, want 0640", info.Mode().Perm())
		}
	}
}

func TestSyncConfigRejectsInvalidExistingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".zcode", "v2", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	if err := SyncConfig("http://127.0.0.1:11435", "token", "model", []string{"model"}); err == nil {
		t.Fatal("SyncConfig() should reject invalid existing config")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read invalid config: %v", err)
	}
	if string(data) != "{invalid" {
		t.Fatal("invalid existing config was overwritten")
	}
}
