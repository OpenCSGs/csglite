package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/region"
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
	t.Setenv(EnvOpenAIStreamDefault, "")
	t.Setenv(EnvHiddenNavItems, "")
}

func TestDefaultValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
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
	if cfg.HuggingFaceEndpoint != DefaultHuggingFaceEndpoint {
		t.Fatalf("HuggingFaceEndpoint = %q, want official default in tests", cfg.HuggingFaceEndpoint)
	}
}

func TestLoadRepairsLegacyDesktopListenAddress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clearCloudServiceEnv(t)
	appHome := filepath.Join(home, AppDir)
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(appHome, ConfigFile),
		[]byte(`{"listen_addr":"127.0.0.1:0"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	Reset()
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != DefaultListenAddr {
		t.Fatalf("ListenAddr = %q, want repaired %q", cfg.ListenAddr, DefaultListenAddr)
	}
}

func TestRuntimeListenOverrideIsNotPersisted(t *testing.T) {
	cfg := &Config{
		ListenAddr:         DefaultListenAddr,
		ListenAddrOverride: "127.0.0.1:0",
	}
	if got := cfg.EffectiveListenAddr(); got != "127.0.0.1:0" {
		t.Fatalf("EffectiveListenAddr = %q", got)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "127.0.0.1:0") {
		t.Fatalf("runtime listen override was persisted: %s", data)
	}
	if !strings.Contains(string(data), DefaultListenAddr) {
		t.Fatalf("persistent listen address missing: %s", data)
	}
}

func TestRuntimeAPIAddrUsesDesktopListener(t *testing.T) {
	cfg := &Config{
		DesktopMode:         true,
		ListenAddr:          DefaultListenAddr,
		BoundAddr:           "127.0.0.1:43123",
		DesktopAPIAddr:      DefaultDesktopAPIAddr,
		DesktopAPIBindAddr:  DefaultDesktopAPIBindAddr,
		DesktopAPIBoundAddr: "0.0.0.0:11436",
	}
	if got := cfg.RuntimeAPIAddr(); got != DefaultDesktopAPIAddr {
		t.Fatalf("RuntimeAPIAddr = %q, want %q", got, DefaultDesktopAPIAddr)
	}
	if got := cfg.RuntimeDockerAPIAddr(); got != DefaultDesktopAPIBindAddr {
		t.Fatalf("RuntimeDockerAPIAddr = %q, want %q", got, DefaultDesktopAPIBindAddr)
	}
	cfg.DesktopMode = false
	if got := cfg.RuntimeAPIAddr(); got != "127.0.0.1:43123" {
		t.Fatalf("non-desktop RuntimeAPIAddr = %q, want internal listener", got)
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

func TestLoadAppliesOpenAIStreamDefaultEnvironmentOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvOpenAIStreamDefault, "true")
	Reset()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.OpenAIStreamDefault {
		t.Fatal("OpenAIStreamDefault = false, want environment override")
	}
}

func TestLoadAppliesHiddenNavItemsEnvironmentOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvHiddenNavItems, " Marketplace, datasets,AI-APPS,marketplace ,, ")
	Reset()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got, want := strings.Join(cfg.HiddenNavItems, ","), "marketplace,datasets,ai-apps"; got != want {
		t.Fatalf("HiddenNavItems = %q, want %q", got, want)
	}
}

func TestParseHiddenNavItemsEmptyValue(t *testing.T) {
	if got := parseHiddenNavItems(" , "); len(got) != 0 {
		t.Fatalf("parseHiddenNavItems() = %#v, want empty", got)
	}
}

func TestEnvironmentBoolKeepsFallbackForInvalidValue(t *testing.T) {
	t.Setenv(EnvOpenAIStreamDefault, "invalid")
	if !environmentBool(EnvOpenAIStreamDefault, true) {
		t.Fatal("environmentBool() = false, want fallback true")
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
		Inference: InferenceConfig{LlamaUseModelMaxCtx: true},
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
	if !loaded.Inference.LlamaUseModelMaxCtx {
		t.Error("Inference.LlamaUseModelMaxCtx = false, want true")
	}
}

func TestInferenceConfigDefaultsForLegacyConfig(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"server_url":"https://hub.opencsg.com"}`), &cfg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if cfg.Inference.LlamaUseModelMaxCtx {
		t.Fatal("legacy config enabled model-max context by default")
	}
}

func TestInferenceConfigPersistsAcrossSaveAndLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clearCloudServiceEnv(t)
	Reset()
	t.Cleanup(Reset)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Inference.LlamaUseModelMaxCtx = true
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	Reset()
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Inference.LlamaUseModelMaxCtx {
		t.Fatal("Inference.LlamaUseModelMaxCtx = false after reload, want true")
	}
}

func TestMarketplaceModelSourcePersistsAndDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clearCloudServiceEnv(t)
	Reset()
	t.Cleanup(Reset)
	cfg := &Config{MarketplaceModelSource: "modelscope"}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	Reset()
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MarketplaceModelSource != "modelscope" {
		t.Fatalf("MarketplaceModelSource = %q", loaded.MarketplaceModelSource)
	}

	loaded.MarketplaceModelSource = "unsupported"
	if err := Save(loaded); err != nil {
		t.Fatal(err)
	}
	Reset()
	loaded, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MarketplaceModelSource != DefaultMarketplaceSource {
		t.Fatalf("invalid source normalized to %q", loaded.MarketplaceModelSource)
	}
}

func TestMarketplaceDatasetSourcePersistsAndDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clearCloudServiceEnv(t)
	Reset()
	t.Cleanup(Reset)
	cfg := &Config{MarketplaceDatasetSource: "huggingface"}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	Reset()
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MarketplaceDatasetSource != "huggingface" {
		t.Fatalf("MarketplaceDatasetSource = %q", loaded.MarketplaceDatasetSource)
	}

	loaded.MarketplaceDatasetSource = "unsupported"
	if err := Save(loaded); err != nil {
		t.Fatal(err)
	}
	Reset()
	loaded, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MarketplaceDatasetSource != DefaultMarketplaceSource {
		t.Fatalf("invalid dataset source normalized to %q", loaded.MarketplaceDatasetSource)
	}
}

func TestResolveHuggingFaceEndpointUsesRegionAndOverrides(t *testing.T) {
	t.Setenv(EnvHuggingFaceEndpoint, "")
	t.Setenv(region.EnvName, "CN")
	if got := ResolveHuggingFaceEndpoint(""); got != ChinaHuggingFaceEndpoint {
		t.Fatalf("CN default = %q, want %q", got, ChinaHuggingFaceEndpoint)
	}
	t.Setenv(region.EnvName, "INTL")
	if got := ResolveHuggingFaceEndpoint(DefaultHuggingFaceEndpoint); got != DefaultHuggingFaceEndpoint {
		t.Fatalf("INTL default = %q, want %q", got, DefaultHuggingFaceEndpoint)
	}
	if got := ResolveHuggingFaceEndpoint("https://hf.example.test"); got != "https://hf.example.test" {
		t.Fatalf("custom endpoint = %q", got)
	}
	t.Setenv(EnvHuggingFaceEndpoint, "https://hf-env.example.test")
	if got := ResolveHuggingFaceEndpoint("https://hf.example.test"); got != "https://hf-env.example.test" {
		t.Fatalf("env override = %q", got)
	}
}

func TestLoadAppliesChinaHuggingFaceMirrorForAutoEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvHuggingFaceEndpoint, "")
	t.Setenv(region.EnvName, "CN")
	clearCloudServiceEnv(t)
	appHome := filepath.Join(home, AppDir)
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(appHome, ConfigFile),
		[]byte(`{"huggingface_endpoint":"https://huggingface.co"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	Reset()
	t.Cleanup(Reset)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HuggingFaceEndpoint != ChinaHuggingFaceEndpoint {
		t.Fatalf("HuggingFaceEndpoint = %q, want China mirror", cfg.HuggingFaceEndpoint)
	}
}

func TestLoadKeepsCustomHuggingFaceEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvHuggingFaceEndpoint, "")
	t.Setenv(region.EnvName, "CN")
	clearCloudServiceEnv(t)
	appHome := filepath.Join(home, AppDir)
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(appHome, ConfigFile),
		[]byte(`{"huggingface_endpoint":"https://hf.example.test"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	Reset()
	t.Cleanup(Reset)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HuggingFaceEndpoint != "https://hf.example.test" {
		t.Fatalf("HuggingFaceEndpoint = %q, want custom URL", cfg.HuggingFaceEndpoint)
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

// A configuration written before the realtime section existed must keep working
// and take the documented defaults.
func TestRealtimeConfigDefaultsForLegacyConfig(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"server_url":"https://hub.opencsg.com"}`), &cfg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if got := cfg.RealtimeMaxSessions(); got != DefaultRealtimeMaxSessions {
		t.Fatalf("RealtimeMaxSessions() = %d, want %d", got, DefaultRealtimeMaxSessions)
	}
	if _, _, ok := cfg.RealtimeICEPortRange(); ok {
		t.Fatal("RealtimeICEPortRange() reported a range for a config that has none")
	}
	if cfg.Realtime.DefaultTTSModel != "" || cfg.Realtime.DefaultASRModel != "" {
		t.Fatal("legacy config carried realtime model defaults")
	}
}

func TestRealtimeConfigPersistsAcrossSaveAndLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clearCloudServiceEnv(t)
	Reset()
	t.Cleanup(Reset)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Realtime = RealtimeConfig{
		DefaultASRModel: "iic/SenseVoiceSmall",
		DefaultTTSModel: "hexgrad/Kokoro-82M",
		MaxSessions:     2,
		ICEUDPPortRange: []int{50000, 50100},
		ICEExtraHostIPs: []string{"203.0.113.7"},
		ICEServers:      []string{"stun:stun.example.com:3478"},
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	Reset()
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Realtime.DefaultTTSModel != "hexgrad/Kokoro-82M" {
		t.Fatalf("DefaultTTSModel = %q after reload", loaded.Realtime.DefaultTTSModel)
	}
	if loaded.Realtime.DefaultASRModel != "iic/SenseVoiceSmall" {
		t.Fatalf("DefaultASRModel = %q after reload", loaded.Realtime.DefaultASRModel)
	}
	if got := loaded.RealtimeMaxSessions(); got != 2 {
		t.Fatalf("RealtimeMaxSessions() = %d, want 2", got)
	}
	low, high, ok := loaded.RealtimeICEPortRange()
	if !ok || low != 50000 || high != 50100 {
		t.Fatalf("RealtimeICEPortRange() = %d, %d, %v", low, high, ok)
	}
	if len(loaded.Realtime.ICEExtraHostIPs) != 1 || loaded.Realtime.ICEExtraHostIPs[0] != "203.0.113.7" {
		t.Fatalf("ICEExtraHostIPs = %v after reload", loaded.Realtime.ICEExtraHostIPs)
	}
	if len(loaded.Realtime.ICEServers) != 1 {
		t.Fatalf("ICEServers = %v after reload", loaded.Realtime.ICEServers)
	}
}

// A negative cap means unlimited, which is how an operator turns the guard off;
// a malformed port range must fall back to any ephemeral port rather than be
// passed on to the ICE agent.
func TestRealtimeConfigEdgeValues(t *testing.T) {
	unlimited := Config{Realtime: RealtimeConfig{MaxSessions: -1}}
	if got := unlimited.RealtimeMaxSessions(); got != 0 {
		t.Fatalf("RealtimeMaxSessions() = %d for a negative cap, want 0 (unlimited)", got)
	}
	for _, bad := range [][]int{{}, {50000}, {0, 100}, {200, 100}, {50000, 70000}, {50000, 50100, 50200}} {
		cfg := Config{Realtime: RealtimeConfig{ICEUDPPortRange: bad}}
		if _, _, ok := cfg.RealtimeICEPortRange(); ok {
			t.Errorf("RealtimeICEPortRange() accepted %v", bad)
		}
	}
}

// Per-model settings were first written as one map per option. They are now one
// typed object per model, so an existing config.json has to keep its settings
// and stop carrying the old keys once it is saved again.
func TestLoadMigratesLegacyPerModelSettingMaps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clearCloudServiceEnv(t)

	cfgPath := filepath.Join(home, ".csghub-lite", ConfigFile)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
	  "inference": {
	    "model_num_ctx": {"local/a": 32768, "local/b": 8192},
	    "model_num_parallel": {"local/a": 2},
	    "model_dtype": {"local/a": "q4_k_m", "local/c": "q8_0"}
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	Reset()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	want := map[string]ModelRuntimeSettings{
		"local/a": {NumCtx: 32768, NumParallel: 2, DType: "q4_k_m"},
		"local/b": {NumCtx: 8192},
		"local/c": {DType: "q8_0"},
	}
	for modelID, settings := range want {
		if got := cfg.Inference.ModelSettings(modelID); got != settings {
			t.Errorf("ModelSettings(%q) = %+v, want %+v", modelID, got, settings)
		}
	}
	if len(cfg.Inference.Models) != len(want) {
		t.Errorf("Models has %d entries, want %d: %+v", len(cfg.Inference.Models), len(want), cfg.Inference.Models)
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	saved, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacyKey := range []string{"model_num_ctx", "model_num_parallel", "model_dtype"} {
		if strings.Contains(string(saved), legacyKey) {
			t.Errorf("saved config still writes the legacy key %q:\n%s", legacyKey, saved)
		}
	}
	if !strings.Contains(string(saved), `"models"`) {
		t.Errorf("saved config does not write the per-model settings:\n%s", saved)
	}
}

// A build that already wrote the typed object may be downgraded and then
// upgraded again, leaving both shapes in the file. The newer one wins.
func TestLoadPrefersTypedPerModelSettingsOverLegacyMaps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clearCloudServiceEnv(t)

	cfgPath := filepath.Join(home, ".csghub-lite", ConfigFile)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mixed := `{
	  "inference": {
	    "models": {"local/a": {"num_ctx": 65536, "keep_alive": "-1"}},
	    "model_num_ctx": {"local/a": 32768},
	    "model_dtype": {"local/a": "q4_k_m"}
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(mixed), 0o600); err != nil {
		t.Fatal(err)
	}

	Reset()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := ModelRuntimeSettings{NumCtx: 65536, DType: "q4_k_m", KeepAlive: "-1"}
	if got := cfg.Inference.ModelSettings("local/a"); got != want {
		t.Fatalf("ModelSettings = %+v, want %+v", got, want)
	}
}

func TestSetModelSettingsDropsEmptyEntries(t *testing.T) {
	var inference InferenceConfig
	inference.SetModelSettings("local/a", ModelRuntimeSettings{KeepAlive: "-1"})
	if got := inference.ModelSettings("local/a"); got.KeepAlive != "-1" {
		t.Fatalf("ModelSettings = %+v, want the keep-alive stored", got)
	}
	inference.SetModelSettings("local/a", ModelRuntimeSettings{})
	if _, ok := inference.Models["local/a"]; ok {
		t.Fatalf("Models still holds an empty entry: %+v", inference.Models)
	}
}

func TestClusterConfigRoundTripAndEnvOverride(t *testing.T) {
	dir := setupTestDir(t)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	cfg := &Config{ModelDir: filepath.Join(dir, "models"), Cluster: ClusterConfig{Secret: "lab-secret", Name: "机房一层"}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfgPath, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	section, ok := onDisk["cluster"].(map[string]any)
	if !ok || section["secret"] != "lab-secret" || section["name"] != "机房一层" {
		t.Fatalf("cluster section on disk: %v", onDisk["cluster"])
	}
	var loaded Config
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Cluster != cfg.Cluster {
		t.Fatalf("round trip %+v != %+v", loaded.Cluster, cfg.Cluster)
	}
	// A config without the section still loads, and the environment overrides.
	var legacy Config
	if err := json.Unmarshal([]byte(`{"server_url":"https://hub.opencsg.com"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Cluster.Secret != "" {
		t.Fatal("legacy config grew a secret")
	}
	t.Setenv(EnvClusterSecret, "from-env")
	t.Setenv(EnvClusterName, "env-name")
	ApplyEnvironmentDefaults(&loaded)
	if loaded.Cluster.Secret != "from-env" || loaded.Cluster.Name != "env-name" {
		t.Fatalf("env override %+v", loaded.Cluster)
	}
}
