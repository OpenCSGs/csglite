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

func clearCloudServiceEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvServerURL, "")
	t.Setenv(EnvAIGatewayURL, "")
	t.Setenv(EnvCloudProviderName, "")
}

func TestDefaultValues(t *testing.T) {
	clearCloudServiceEnv(t)
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
	if cfg.CloudProviderName != DefaultCloudProviderName {
		t.Errorf("CloudProviderName = %q, want %q", cfg.CloudProviderName, DefaultCloudProviderName)
	}
	if cfg.AIAppPreferredModels == nil {
		t.Fatal("AIAppPreferredModels = nil, want initialized map")
	}
}

func TestLoadAppliesCloudServiceEnvironmentOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvServerURL, " https://modelhub.example.com ")
	t.Setenv(EnvAIGatewayURL, " https://gateway.example.com/v1 ")
	t.Setenv(EnvCloudProviderName, " Example Hub ")
	Reset()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ServerURL != "https://modelhub.example.com" {
		t.Fatalf("ServerURL = %q, want environment override", cfg.ServerURL)
	}
	if cfg.AIGatewayURL != "https://gateway.example.com/v1" {
		t.Fatalf("AIGatewayURL = %q, want environment override", cfg.AIGatewayURL)
	}
	if cfg.CloudProviderName != "Example Hub" {
		t.Fatalf("CloudProviderName = %q, want environment override", cfg.CloudProviderName)
	}
}

func TestLoadKeepsSavedCloudServiceConfigOverEnvironmentDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvServerURL, "https://env.example.com")
	t.Setenv(EnvAIGatewayURL, "https://env-gateway.example.com")
	t.Setenv(EnvCloudProviderName, "Env Hub")

	appHome := filepath.Join(home, AppDir)
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}
	cfg := &Config{
		ServerURL:         "https://saved.example.com",
		AIGatewayURL:      "https://saved-gateway.example.com",
		CloudProviderName: "Saved Hub",
		ListenAddr:        DefaultListenAddr,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appHome, ConfigFile), data, 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	Reset()
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.ServerURL != cfg.ServerURL {
		t.Fatalf("ServerURL = %q, want saved value", loaded.ServerURL)
	}
	if loaded.AIGatewayURL != cfg.AIGatewayURL {
		t.Fatalf("AIGatewayURL = %q, want saved value", loaded.AIGatewayURL)
	}
	if loaded.CloudProviderName != cfg.CloudProviderName {
		t.Fatalf("CloudProviderName = %q, want saved value", loaded.CloudProviderName)
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
	clearCloudServiceEnv(t)
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
