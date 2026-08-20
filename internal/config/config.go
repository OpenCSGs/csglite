package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/pkg/api"
)

const (
	DefaultServerURL           = "https://hub.opencsg.com"
	DefaultDisplayURL          = "https://opencsg.com"
	DefaultListenAddr          = ":11435"
	DefaultDesktopAPIAddr      = "127.0.0.1:11436"
	DefaultDesktopAPIBindAddr  = "0.0.0.0:11436"
	DefaultCloudProviderName   = "csghub"
	DefaultMarketplaceSource   = "opencsg"
	DefaultHuggingFaceEndpoint = "https://huggingface.co"
	DefaultModelScopeEndpoint  = "https://modelscope.cn"
	EnvServerURL               = "CSGHUB_LITE_SERVER_URL"
	EnvAIGatewayURL            = "CSGHUB_LITE_AI_GATEWAY_URL"
	EnvCloudProviderName       = "CSGHUB_LITE_CLOUD_PROVIDER_NAME"
	EnvOpenAIStreamDefault     = "CSGHUB_LITE_OPENAI_STREAM_DEFAULT"
	EnvHiddenNavItems          = "CSGHUB_LITE_HIDDEN_NAV_ITEMS"
	EnvHuggingFaceEndpoint     = "HF_ENDPOINT"
	EnvHuggingFaceToken        = "HF_TOKEN"
	EnvHuggingFaceHubToken     = "HUGGING_FACE_HUB_TOKEN"
	EnvModelScopeEndpoint      = "MODELSCOPE_ENDPOINT"
	EnvModelScopeToken         = "MODELSCOPE_API_TOKEN"
	EnvModelScopeAPIKey        = "MODELSCOPE_API_KEY"
	AppDir                     = ".csghub-lite"
	ConfigFile                 = "config.json"
	ModelsDir                  = "models"
	DatasetsDir                = "datasets"
	TmpDir                     = "tmp"
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

func (c *Config) TempDir() string {
	return TempDirForStorage(c.StorageDir())
}

type Config struct {
	ServerURL                string                             `json:"server_url"`
	AIGatewayURL             string                             `json:"ai_gateway_url,omitempty"`
	CloudProviderName        string                             `json:"cloud_provider_name,omitempty"`
	Token                    string                             `json:"token,omitempty"`
	OpenCSGAPIKey            string                             `json:"opencsg_api_key,omitempty"`
	HuggingFaceEndpoint      string                             `json:"huggingface_endpoint,omitempty"`
	HuggingFaceToken         string                             `json:"huggingface_token,omitempty"`
	ModelScopeEndpoint       string                             `json:"modelscope_endpoint,omitempty"`
	ModelScopeToken          string                             `json:"modelscope_token,omitempty"`
	MarketplaceModelSource   string                             `json:"marketplace_model_source,omitempty"`
	MarketplaceDatasetSource string                             `json:"marketplace_dataset_source,omitempty"`
	ListenAddr               string                             `json:"listen_addr"`
	ModelDir                 string                             `json:"model_dir"`
	DatasetDir               string                             `json:"dataset_dir"`
	OpenAIStreamDefault      bool                               `json:"-"`
	HiddenNavItems           []string                           `json:"-"`
	AIAppPreferredModels     map[string]string                  `json:"ai_app_preferred_models,omitempty"`
	AIAppPreferredSources    map[string]string                  `json:"ai_app_preferred_sources,omitempty"`
	AIAppModelBindings       map[string][]api.AIAppModelBinding `json:"ai_app_model_bindings,omitempty"`
	WebSearch                WebSearchConfig                    `json:"web_search,omitempty"`
	Observability            ObservabilityConfig                `json:"observability,omitempty"`
	DesktopMode              bool                               `json:"-"`
	DesktopToken             string                             `json:"-"`
	DesktopSessionToken      string                             `json:"-"`
	DesktopControlToken      string                             `json:"-"`
	DesktopInstanceID        string                             `json:"-"`
	ListenAddrOverride       string                             `json:"-"`
	BoundAddr                string                             `json:"-"`
	DesktopAPIAddr           string                             `json:"-"`
	DesktopAPIBindAddr       string                             `json:"-"`
	DesktopAPIBoundAddr      string                             `json:"-"`
}

func (c *Config) EffectiveListenAddr() string {
	if strings.TrimSpace(c.ListenAddrOverride) != "" {
		return c.ListenAddrOverride
	}
	return c.ListenAddr
}

func (c *Config) RuntimeListenAddr() string {
	if strings.TrimSpace(c.BoundAddr) != "" {
		return c.BoundAddr
	}
	return c.EffectiveListenAddr()
}

func (c *Config) RuntimeAPIAddr() string {
	if c.DesktopMode {
		if strings.TrimSpace(c.DesktopAPIAddr) != "" {
			return c.DesktopAPIAddr
		}
	}
	return c.RuntimeListenAddr()
}

func (c *Config) RuntimeDockerAPIAddr() string {
	if c.DesktopMode && strings.TrimSpace(c.DesktopAPIBindAddr) != "" {
		return c.DesktopAPIBindAddr
	}
	return c.RuntimeListenAddr()
}

type WebSearchConfig struct {
	Enabled        bool     `json:"enabled,omitempty"`
	MaxResults     int      `json:"max_results,omitempty"`
	Language       string   `json:"language,omitempty"`
	Providers      []string `json:"providers,omitempty"`
	SafeSearch     int      `json:"safe_search,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

const DefaultObservabilityRetentionDays = 30

type ObservabilityConfig struct {
	// RetentionDays is nil for legacy configurations and 0 for unlimited retention.
	RetentionDays *int `json:"retention_days,omitempty"`
}

func ObservabilityRetentionDays(cfg ObservabilityConfig) int {
	if cfg.RetentionDays == nil {
		return DefaultObservabilityRetentionDays
	}
	if *cfg.RetentionDays < 0 {
		return DefaultObservabilityRetentionDays
	}
	return *cfg.RetentionDays
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

func TempDirForStorage(storageDir string) string {
	return filepath.Join(filepath.Clean(storageDir), TmpDir)
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
			ListenAddr:            DefaultListenAddr,
			AIAppPreferredModels:  map[string]string{},
			AIAppPreferredSources: map[string]string{},
			AIAppModelBindings:    map[string][]api.AIAppModelBinding{},
			WebSearch:             DefaultWebSearchConfig(),
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
			if !os.IsNotExist(err) {
				loadErr = err
				return
			}
		} else {
			if err := json.Unmarshal(data, globalConfig); err != nil {
				loadErr = err
				return
			}
		}

		ApplyEnvironmentDefaults(globalConfig)
		if globalConfig.ServerURL == "" {
			globalConfig.ServerURL = DefaultServerURL
		}
		if strings.TrimSpace(globalConfig.HuggingFaceEndpoint) == "" {
			globalConfig.HuggingFaceEndpoint = DefaultHuggingFaceEndpoint
		}
		if strings.TrimSpace(globalConfig.ModelScopeEndpoint) == "" {
			globalConfig.ModelScopeEndpoint = DefaultModelScopeEndpoint
		}
		globalConfig.MarketplaceModelSource = NormalizeMarketplaceModelSource(globalConfig.MarketplaceModelSource)
		globalConfig.MarketplaceDatasetSource = NormalizeMarketplaceDatasetSource(globalConfig.MarketplaceDatasetSource)
		if globalConfig.ListenAddr == "" {
			globalConfig.ListenAddr = DefaultListenAddr
		}
		// Early desktop builds accidentally persisted their ephemeral listener.
		// Restore the normal CLI address while keeping future overrides runtime-only.
		if globalConfig.ListenAddr == "127.0.0.1:0" {
			globalConfig.ListenAddr = DefaultListenAddr
		}
		globalConfig.CloudProviderName = NormalizeCloudProviderName(globalConfig.CloudProviderName)
		if globalConfig.ModelDir == "" {
			globalConfig.ModelDir = modelDir
		}
		if globalConfig.DatasetDir == "" {
			globalConfig.DatasetDir = datasetDir
		}
		if globalConfig.AIAppPreferredModels == nil {
			globalConfig.AIAppPreferredModels = map[string]string{}
		}
		if globalConfig.AIAppPreferredSources == nil {
			globalConfig.AIAppPreferredSources = map[string]string{}
		}
		if globalConfig.AIAppModelBindings == nil {
			globalConfig.AIAppModelBindings = map[string][]api.AIAppModelBinding{}
		}
		globalConfig.WebSearch = NormalizeWebSearchConfig(globalConfig.WebSearch)
	})
	return globalConfig, loadErr
}

func NormalizeMarketplaceModelSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "huggingface":
		return "huggingface"
	case "modelscope":
		return "modelscope"
	default:
		return DefaultMarketplaceSource
	}
}

func IsSupportedMarketplaceModelSource(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "opencsg", "huggingface", "modelscope":
		return true
	default:
		return false
	}
}

func NormalizeMarketplaceDatasetSource(value string) string {
	return NormalizeMarketplaceModelSource(value)
}

func IsSupportedMarketplaceDatasetSource(value string) bool {
	return IsSupportedMarketplaceModelSource(value)
}

func ApplyEnvironmentDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	if value := strings.TrimSpace(os.Getenv(EnvServerURL)); value != "" && strings.TrimSpace(cfg.ServerURL) == "" {
		cfg.ServerURL = value
	}
	if value := strings.TrimSpace(os.Getenv(EnvAIGatewayURL)); value != "" && strings.TrimSpace(cfg.AIGatewayURL) == "" {
		cfg.AIGatewayURL = value
	}
	if value := strings.TrimSpace(os.Getenv(EnvCloudProviderName)); value != "" && strings.TrimSpace(cfg.CloudProviderName) == "" {
		cfg.CloudProviderName = NormalizeCloudProviderName(value)
	}
	cfg.OpenAIStreamDefault = environmentBool(EnvOpenAIStreamDefault, cfg.OpenAIStreamDefault)
	cfg.HiddenNavItems = parseHiddenNavItems(os.Getenv(EnvHiddenNavItems))
}

func parseHiddenNavItems(value string) []string {
	items := make([]string, 0)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(value, ",") {
		item := strings.ToLower(strings.TrimSpace(raw))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		items = append(items, item)
	}
	return items
}

func environmentBool(name string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func NormalizeCloudProviderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return DefaultCloudProviderName
	}
	return name
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

	cfg.CloudProviderName = NormalizeCloudProviderName(cfg.CloudProviderName)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	globalConfig = cfg
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(cfgPath, 0o600)
}

func Reset() {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig = nil
	configOnce = sync.Once{}
}
