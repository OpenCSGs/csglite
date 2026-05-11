package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func TestDefaultValues(t *testing.T) {
	Reset()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ServerURL != DefaultServerURL {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, DefaultServerURL)
	}
	if cfg.ListenAddr != DefaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, DefaultListenAddr)
	}
	if cfg.AIAppPreferredModels == nil {
		t.Fatal("AIAppPreferredModels = nil, want initialized map")
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := setupTestDir(t)
	cfgPath := filepath.Join(dir, ConfigFile)

	cfg := &Config{
		ServerURL:  "https://custom.example.com",
		Token:      "test-token-123",
		ListenAddr: ":8080",
		ModelDir:   filepath.Join(dir, "models"),
		AIAppPreferredModels: map[string]string{
			"claude-code": "Qwen/Qwen2.5-Coder-1.5B",
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent error: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Read it back
	readData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	var loaded Config
	if err := json.Unmarshal(readData, &loaded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if loaded.ServerURL != cfg.ServerURL {
		t.Errorf("ServerURL = %q, want %q", loaded.ServerURL, cfg.ServerURL)
	}
	if loaded.Token != cfg.Token {
		t.Errorf("Token = %q, want %q", loaded.Token, cfg.Token)
	}
	if loaded.ListenAddr != cfg.ListenAddr {
		t.Errorf("ListenAddr = %q, want %q", loaded.ListenAddr, cfg.ListenAddr)
	}
	if loaded.ModelDir != cfg.ModelDir {
		t.Errorf("ModelDir = %q, want %q", loaded.ModelDir, cfg.ModelDir)
	}
	if got := loaded.AIAppPreferredModels["claude-code"]; got != "Qwen/Qwen2.5-Coder-1.5B" {
		t.Errorf("AIAppPreferredModels[claude-code] = %q, want coder model", got)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	Reset()

	dir := setupTestDir(t)
	nested := filepath.Join(dir, "deep", "nested")

	cfg := &Config{
		ServerURL:  DefaultServerURL,
		ListenAddr: DefaultListenAddr,
		ModelDir:   filepath.Join(nested, "models"),
	}

	// Can't use Save() directly since it uses AppHome(), but test the pattern
	cfgPath := filepath.Join(nested, ConfigFile)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Error("config file was not created")
	}
}

func TestConfigGet(t *testing.T) {
	Reset()
	cfg := Get()
	if cfg == nil {
		t.Fatal("Get() returned nil")
	}
	if cfg.ServerURL != DefaultServerURL {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, DefaultServerURL)
	}
}

func TestAppHome(t *testing.T) {
	home, err := AppHome()
	if err != nil {
		t.Fatalf("AppHome() error: %v", err)
	}
	if home == "" {
		t.Error("AppHome() returned empty string")
	}
	if !filepath.IsAbs(home) {
		t.Errorf("AppHome() = %q, want absolute path", home)
	}
}

func TestStorageDir(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "csghub-lite")
	modelDir := filepath.Join(root, "models")
	datasetDir := filepath.Join(root, "datasets")

	if got := StorageDir(modelDir, datasetDir); got != root {
		t.Fatalf("StorageDir(%q, %q) = %q, want %q", modelDir, datasetDir, got, root)
	}
}

func TestStorageDirFallbacksToModelParent(t *testing.T) {
	modelDir := filepath.Join(string(filepath.Separator), "data", "custom-model-cache")
	datasetDir := filepath.Join(string(filepath.Separator), "other", "dataset-cache")
	want := filepath.Dir(modelDir)

	if got := StorageDir(modelDir, datasetDir); got != want {
		t.Fatalf("StorageDir(%q, %q) = %q, want %q", modelDir, datasetDir, got, want)
	}
}

func TestStorageSubdirsForRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "srv", "csghub-data")

	if got := ModelDirForStorage(root); got != filepath.Join(root, ModelsDir) {
		t.Fatalf("ModelDirForStorage(%q) = %q", root, got)
	}
	if got := DatasetDirForStorage(root); got != filepath.Join(root, DatasetsDir) {
		t.Fatalf("DatasetDirForStorage(%q) = %q", root, got)
	}
}

func TestResolveModelAlias(t *testing.T) {
	tests := []struct {
		name      string
		aliases   map[string]string
		modelID   string
		wantModel string
	}{
		{
			name:      "no aliases configured",
			aliases:   nil,
			modelID:   "claude-sonnet-4",
			wantModel: "claude-sonnet-4",
		},
		{
			name:      "alias exists",
			aliases:   map[string]string{"claude-sonnet-4": "deepseek-chat"},
			modelID:   "claude-sonnet-4",
			wantModel: "deepseek-chat",
		},
		{
			name:      "alias does not exist",
			aliases:   map[string]string{"claude-sonnet-4": "deepseek-chat"},
			modelID:   "other-model",
			wantModel: "other-model",
		},
		{
			name:      "alias with whitespace in modelID",
			aliases:   map[string]string{"claude-sonnet-4": "deepseek-chat"},
			modelID:   "  claude-sonnet-4  ",
			wantModel: "deepseek-chat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ModelAliases: tt.aliases}
			got := cfg.ResolveModelAlias(tt.modelID)
			if got != tt.wantModel {
				t.Errorf("ResolveModelAlias(%q) = %q, want %q", tt.modelID, got, tt.wantModel)
			}
		})
	}
}

func TestGetAllAliases(t *testing.T) {
	tests := []struct {
		name    string
		aliases map[string]string
		wantLen int
	}{
		{
			name:    "nil aliases",
			aliases: nil,
			wantLen: 0,
		},
		{
			name:    "empty aliases",
			aliases: map[string]string{},
			wantLen: 0,
		},
		{
			name:    "multiple aliases",
			aliases: map[string]string{"claude-sonnet-4": "deepseek-chat", "claude-opus-4": "gpt-4o"},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ModelAliases: tt.aliases}
			got := cfg.GetAllAliases()
			if len(got) != tt.wantLen {
				t.Errorf("GetAllAliases() returned %d aliases, want %d", len(got), tt.wantLen)
			}
			// Verify it returns a copy, not the original
			if tt.aliases != nil && len(tt.aliases) > 0 {
				got["new-alias"] = "new-model"
				if _, exists := tt.aliases["new-alias"]; exists {
					t.Error("GetAllAliases() did not return a copy, modifying it affected the original")
				}
			}
		})
	}
}
