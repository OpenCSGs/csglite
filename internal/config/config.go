package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const (
	DefaultServerURL  = "https://hub.opencsg.com"
	DefaultDisplayURL = "https://opencsg.com"
	DefaultListenAddr = ":11435"
	AppDir            = ".csghub-lite"
	ConfigFile        = "config.json"
	ModelsDir         = "models"
	DatasetsDir       = "datasets"
)

func (c *Config) DisplayURL() string {
	if c.ServerURL == DefaultServerURL || c.ServerURL == "" {
		return DefaultDisplayURL
	}
	return c.ServerURL
}

func (c *Config) StorageDir() string {
	return StorageDir(c.ModelDir, c.DatasetDir)
}

type Config struct {
	ServerURL            string            `json:"server_url"`
	AIGatewayURL         string            `json:"ai_gateway_url,omitempty"`
	Token                string            `json:"token,omitempty"`
	OpenCSGAPIKey        string            `json:"opencsg_api_key,omitempty"`
	ListenAddr           string            `json:"listen_addr"`
	ModelDir             string            `json:"model_dir"`
	DatasetDir           string            `json:"dataset_dir"`
	AIAppPreferredModels map[string]string `json:"ai_app_preferred_models,omitempty"`
	WebSearch            WebSearchConfig   `json:"web_search,omitempty"`
}

type WebSearchConfig struct {
	Enabled        bool     `json:"enabled,omitempty"`
	MaxResults     int      `json:"max_results,omitempty"`
	Language       string   `json:"language,omitempty"`
	Providers      []string `json:"providers,omitempty"`
	SafeSearch     int      `json:"safe_search,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

var (
	globalConfig *Config
	configOnce   sync.Once
	configMu     sync.RWMutex
)

func AppHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, AppDir), nil
}

func DefaultStorageDir() (string, error) {
	return AppHome()
}

func DefaultModelDir() (string, error) {
	home, err := DefaultStorageDir()
	if err != nil {
		return "", err
	}
	return ModelDirForStorage(home), nil
}

func DefaultDatasetDir() (string, error) {
	home, err := DefaultStorageDir()
	if err != nil {
		return "", err
	}
	return DatasetDirForStorage(home), nil
}

func ModelDirForStorage(storageDir string) string {
	return filepath.Join(filepath.Clean(storageDir), ModelsDir)
}

func DatasetDirForStorage(storageDir string) string {
	return filepath.Join(filepath.Clean(storageDir), DatasetsDir)
}

func StorageDir(modelDir, datasetDir string) string {
	modelDir = cleanConfigPath(modelDir)
	datasetDir = cleanConfigPath(datasetDir)

	if modelDir != "" && datasetDir != "" && filepath.Dir(modelDir) == filepath.Dir(datasetDir) {
		return filepath.Dir(modelDir)
	}
	if modelDir != "" && filepath.Base(modelDir) == ModelsDir {
		return filepath.Dir(modelDir)
	}
	if datasetDir != "" && filepath.Base(datasetDir) == DatasetsDir {
		return filepath.Dir(datasetDir)
	}
	if modelDir != "" {
		return filepath.Dir(modelDir)
	}
	if datasetDir != "" {
		return filepath.Dir(datasetDir)
	}
	return ""
}

func cleanConfigPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func ConfigPath() (string, error) {
	home, err := AppHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ConfigFile), nil
}

func Load() (*Config, error) {
	var loadErr error
	configOnce.Do(func() {
		globalConfig = &Config{
			ServerURL:            DefaultServerURL,
			ListenAddr:           DefaultListenAddr,
			AIAppPreferredModels: map[string]string{},
			WebSearch:            DefaultWebSearchConfig(),
		}

		modelDir, err := DefaultModelDir()
		if err != nil {
			loadErr = err
			return
		}
		globalConfig.ModelDir = modelDir

		datasetDir, err := DefaultDatasetDir()
		if err != nil {
			loadErr = err
			return
		}
		globalConfig.DatasetDir = datasetDir

		cfgPath, err := ConfigPath()
		if err != nil {
			loadErr = err
			return
		}

		data, err := os.ReadFile(cfgPath)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			loadErr = err
			return
		}

		if err := json.Unmarshal(data, globalConfig); err != nil {
			loadErr = err
			return
		}

		if globalConfig.ServerURL == "" {
			globalConfig.ServerURL = DefaultServerURL
		}
		if globalConfig.ListenAddr == "" {
			globalConfig.ListenAddr = DefaultListenAddr
		}
		if globalConfig.ModelDir == "" {
			globalConfig.ModelDir = modelDir
		}
		if globalConfig.DatasetDir == "" {
			globalConfig.DatasetDir = datasetDir
		}
		if globalConfig.AIAppPreferredModels == nil {
			globalConfig.AIAppPreferredModels = map[string]string{}
		}
		globalConfig.WebSearch = NormalizeWebSearchConfig(globalConfig.WebSearch)
	})
	return globalConfig, loadErr
}

func DefaultWebSearchConfig() WebSearchConfig {
	return WebSearchConfig{
		Enabled:        true,
		MaxResults:     5,
		SafeSearch:     1,
		TimeoutSeconds: 5,
	}
}

func NormalizeWebSearchConfig(cfg WebSearchConfig) WebSearchConfig {
	defaults := DefaultWebSearchConfig()
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = defaults.MaxResults
	}
	if cfg.MaxResults > 10 {
		cfg.MaxResults = 10
	}
	if cfg.SafeSearch < 0 || cfg.SafeSearch > 2 {
		cfg.SafeSearch = defaults.SafeSearch
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if cfg.TimeoutSeconds > 30 {
		cfg.TimeoutSeconds = 30
	}
	return cfg
}

func Get() *Config {
	configMu.RLock()
	defer configMu.RUnlock()
	if globalConfig == nil {
		cfg, _ := Load()
		return cfg
	}
	return globalConfig
}

func Save(cfg *Config) error {
	configMu.Lock()
	defer configMu.Unlock()

	cfgPath, err := ConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	globalConfig = cfg
	return os.WriteFile(cfgPath, data, 0o644)
}

func Reset() {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig = nil
	configOnce = sync.Once{}
}
