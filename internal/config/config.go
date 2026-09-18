package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/internal/region"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	DefaultServerURL           = "https://hub.opencsg.com"
	DefaultDisplayURL          = "https://opencsg.com"
	DefaultListenAddr          = ":11435"
	DefaultDesktopAPIAddr      = "127.0.0.1:11436"
	DefaultDesktopAPIBindAddr  = "0.0.0.0:11436"
	DefaultAuthCallbackAddr    = "127.0.0.1:11437"
	DefaultCloudProviderName   = "csghub"
	DefaultMarketplaceSource   = "opencsg"
	DefaultHuggingFaceEndpoint = "https://huggingface.co"
	ChinaHuggingFaceEndpoint   = "https://hf-mirror.com"
	DefaultModelScopeEndpoint  = "https://modelscope.cn"
	EnvServerURL               = "CSGHUB_LITE_SERVER_URL"
	EnvAIGatewayURL            = "CSGHUB_LITE_AI_GATEWAY_URL"
	EnvCloudProviderName       = "CSGHUB_LITE_CLOUD_PROVIDER_NAME"
	EnvOpenAIStreamDefault     = "CSGHUB_LITE_OPENAI_STREAM_DEFAULT"
	EnvHiddenNavItems          = "CSGHUB_LITE_HIDDEN_NAV_ITEMS"
	EnvClusterSecret           = "CSGHUB_LITE_CLUSTER_SECRET"
	EnvClusterName             = "CSGHUB_LITE_CLUSTER_NAME"
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
	Inference                InferenceConfig                    `json:"inference,omitempty"`
	Realtime                 RealtimeConfig                     `json:"realtime,omitempty"`
	// Cluster holds the LAN compute cluster settings that must survive
	// however the service is launched (systemd, launchd, `csghub-lite
	// start`): the shared secret that forms the cluster automatically.
	// Membership itself is runtime state and lives in <storage>/cluster/.
	Cluster             ClusterConfig `json:"cluster,omitempty"`
	DesktopMode         bool          `json:"-"`
	DesktopToken        string        `json:"-"`
	DesktopSessionToken string        `json:"-"`
	DesktopControlToken string        `json:"-"`
	DesktopInstanceID   string        `json:"-"`
	ListenAddrOverride  string        `json:"-"`
	BoundAddr           string        `json:"-"`
	DesktopAPIAddr      string        `json:"-"`
	DesktopAPIBindAddr  string        `json:"-"`
	DesktopAPIBoundAddr string        `json:"-"`
	AuthCallbackAddr    string        `json:"-"`
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

// InferenceConfig groups process-wide inference defaults that apply equally to
// the Web UI and external API clients.
type InferenceConfig struct {
	LlamaUseModelMaxCtx bool `json:"llama_use_model_max_ctx,omitempty"`

	// LlamaNumParallel is the global default slot count for llama-server. It
	// lives here rather than in the browser because the chat UI no longer sends
	// a slot count with each request: only a model's own setting and this
	// global default decide how many slots a load gets.
	LlamaNumParallel int `json:"llama_num_parallel,omitempty"`

	// Models holds the per-model load options keyed by model ID. They live in
	// the app config rather than in the model directory so that re-downloading
	// a model keeps its settings.
	Models map[string]ModelRuntimeSettings `json:"models,omitempty"`

	// The fields below are the per-model maps written before the settings were
	// grouped into Models. Load migrates them and stops writing them; they stay
	// here only so an existing config.json is still read correctly.
	LegacyModelNumCtx      map[string]int    `json:"model_num_ctx,omitempty"`
	LegacyModelNumParallel map[string]int    `json:"model_num_parallel,omitempty"`
	LegacyModelDType       map[string]string `json:"model_dtype,omitempty"`
}

// ModelRuntimeSettings are the load options a user pinned for one model. Each
// one overrides the corresponding global default and still loses to a value
// sent with the request.
type ModelRuntimeSettings struct {
	// NumCtx is the context window, 0 when the model follows the global one.
	NumCtx int `json:"num_ctx,omitempty"`
	// NumParallel is the llama-server slot count, 0 when the model follows
	// LlamaNumParallel.
	NumParallel int `json:"num_parallel,omitempty"`
	// DType is the GGUF quantization to serve, "" for the repository default.
	// A repository holding several quantizations keeps serving the one the
	// user picked instead of reverting on the next reload or idle eviction.
	DType string `json:"dtype,omitempty"`
	// KeepAlive is the idle window before the engine is unloaded, in the same
	// spelling the API and CLI accept: "30s", "1h", or "-1" to keep the model
	// loaded until it is stopped. Empty follows the runtime default. Without
	// it the window survived only as long as one engine instance, so a restart
	// -- or a load started by a chat request, which carries no keep-alive --
	// silently went back to the five-minute default.
	KeepAlive string `json:"keep_alive,omitempty"`
}

// IsZero reports whether the model has no pinned setting left, in which case
// it is dropped from the config rather than stored as an empty object.
func (s ModelRuntimeSettings) IsZero() bool {
	return s == ModelRuntimeSettings{}
}

// ModelSettings returns the settings pinned for modelID, or the zero value.
func (c *InferenceConfig) ModelSettings(modelID string) ModelRuntimeSettings {
	if c == nil {
		return ModelRuntimeSettings{}
	}
	return c.Models[modelID]
}

// SetModelSettings stores the settings for modelID, removing the entry once
// nothing is pinned for it any more.
func (c *InferenceConfig) SetModelSettings(modelID string, settings ModelRuntimeSettings) {
	if c == nil {
		return
	}
	if settings.IsZero() {
		delete(c.Models, modelID)
		return
	}
	if c.Models == nil {
		c.Models = make(map[string]ModelRuntimeSettings)
	}
	c.Models[modelID] = settings
}

// migrateLegacyModelSettings folds the pre-Models per-model maps into Models
// and clears them so they are no longer written back. Values already in Models
// win, since they were written by a newer build.
func migrateLegacyModelSettings(c *InferenceConfig) {
	upsert := func(modelID string, apply func(*ModelRuntimeSettings)) {
		if strings.TrimSpace(modelID) == "" {
			return
		}
		settings := c.Models[modelID]
		apply(&settings)
		c.SetModelSettings(modelID, settings)
	}
	for modelID, numCtx := range c.LegacyModelNumCtx {
		upsert(modelID, func(s *ModelRuntimeSettings) {
			if s.NumCtx == 0 {
				s.NumCtx = numCtx
			}
		})
	}
	for modelID, numParallel := range c.LegacyModelNumParallel {
		upsert(modelID, func(s *ModelRuntimeSettings) {
			if s.NumParallel == 0 {
				s.NumParallel = numParallel
			}
		})
	}
	for modelID, dtype := range c.LegacyModelDType {
		upsert(modelID, func(s *ModelRuntimeSettings) {
			if s.DType == "" {
				s.DType = dtype
			}
		})
	}
	c.LegacyModelNumCtx = nil
	c.LegacyModelNumParallel = nil
	c.LegacyModelDType = nil
}

// DefaultRealtimeMaxSessions bounds how many realtime voice sessions may run at
// once. Each one can hold a recognition and a synthesis model, so the cap is
// about memory, not about request rate.
const DefaultRealtimeMaxSessions = 4

// ClusterConfig is the provisioning side of the LAN compute cluster
// (docs/guides/lan-cluster-design.md). Nodes installed with the same Secret
// find each other over the network and form one cluster without any create
// or join step; the secret is the credential, so keep it private.
type ClusterConfig struct {
	Secret string `json:"secret,omitempty"`
	// Name is the display name given to an automatically formed cluster.
	Name string `json:"name,omitempty"`
}

// RealtimeConfig groups the settings of the realtime voice API
// (docs/guides/realtime-audio-api.md). Per-model defaults such as voice and
// speed are not here: they live in the existing per-model configuration.
type RealtimeConfig struct {
	// DefaultASRModel and DefaultTTSModel fill in the halves of the pipeline a
	// client did not name. Clients in the field commonly connect with only
	// ?model=<asr model>, which leaves synthesis unconfigured otherwise.
	DefaultASRModel string `json:"default_asr_model,omitempty"`
	DefaultTTSModel string `json:"default_tts_model,omitempty"`

	// MaxSessions is 0 for the default (DefaultRealtimeMaxSessions) and
	// negative for unlimited.
	MaxSessions int `json:"max_sessions,omitempty"`

	// ICEUDPPortRange restricts WebRTC media to [min, max] so the range can be
	// opened in a firewall. Empty means any ephemeral port.
	ICEUDPPortRange []int `json:"ice_udp_port_range,omitempty"`

	// ICEExtraHostIPs advertises additional host addresses, for a server behind
	// 1:1 NAT whose public address it cannot discover locally.
	ICEExtraHostIPs []string `json:"ice_extra_host_ips,omitempty"`

	// ICEServers lists STUN/TURN URLs. A local deployment needs none, so the
	// default is empty rather than a public STUN server.
	ICEServers []string `json:"ice_servers,omitempty"`
}

// RealtimeMaxSessions resolves the configured cap, returning 0 for unlimited.
func (c *Config) RealtimeMaxSessions() int {
	switch {
	case c.Realtime.MaxSessions < 0:
		return 0
	case c.Realtime.MaxSessions == 0:
		return DefaultRealtimeMaxSessions
	default:
		return c.Realtime.MaxSessions
	}
}

// RealtimeICEPortRange reports the configured media port range, and ok=false
// when it is unset or malformed, in which case any ephemeral port is used.
func (c *Config) RealtimeICEPortRange() (uint16, uint16, bool) {
	r := c.Realtime.ICEUDPPortRange
	if len(r) != 2 {
		return 0, 0, false
	}
	low, high := r[0], r[1]
	if low <= 0 || high <= 0 || low > high || high > 65535 {
		return 0, 0, false
	}
	return uint16(low), uint16(high), true
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
		if isAutoHuggingFaceEndpoint(globalConfig.HuggingFaceEndpoint) {
			globalConfig.HuggingFaceEndpoint = defaultHuggingFaceEndpointForRegion()
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
		migrateLegacyModelSettings(&globalConfig.Inference)
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

func ResolveHuggingFaceEndpoint(configured string) string {
	if env := strings.TrimSpace(os.Getenv(EnvHuggingFaceEndpoint)); env != "" {
		return env
	}
	if configured = strings.TrimSpace(configured); configured != "" && !isAutoHuggingFaceEndpoint(configured) {
		return configured
	}
	return defaultHuggingFaceEndpointForRegion()
}

func defaultHuggingFaceEndpointForRegion() string {
	if region.Detect() == region.CN {
		return ChinaHuggingFaceEndpoint
	}
	return DefaultHuggingFaceEndpoint
}

func isAutoHuggingFaceEndpoint(value string) bool {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	return value == "" ||
		strings.EqualFold(value, DefaultHuggingFaceEndpoint) ||
		strings.EqualFold(value, ChinaHuggingFaceEndpoint)
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
	// The cluster secret from the environment wins over the file: an
	// operator who exports it for a service unit expects that value to be
	// the one in effect.
	if value := strings.TrimSpace(os.Getenv(EnvClusterSecret)); value != "" {
		cfg.Cluster.Secret = value
	}
	if value := strings.TrimSpace(os.Getenv(EnvClusterName)); value != "" {
		cfg.Cluster.Name = value
	}
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
