package api

import "time"

// -- Request types --

type GenerateRequest struct {
	Model   string        `json:"model"`
	Prompt  string        `json:"prompt"`
	Stream  *bool         `json:"stream,omitempty"`
	Options *ModelOptions `json:"options,omitempty"`
}

type ChatRequest struct {
	Model     string            `json:"model"`
	Source    string            `json:"source,omitempty"`
	Messages  []Message         `json:"messages"`
	Tools     []Tool            `json:"tools,omitempty"`
	Stream    *bool             `json:"stream,omitempty"`
	Options   *ModelOptions     `json:"options,omitempty"`
	WebSearch *WebSearchOptions `json:"web_search,omitempty"`
}

type PullRequest struct {
	Model string `json:"model"`
	// Quant selects a GGUF weight variant when multiple quantizations exist (e.g. Q4_K_M). Ignored for non-GGUF models.
	Quant string `json:"quant,omitempty"`
}

type DeleteRequest struct {
	Model string `json:"model"`
}

type ShowRequest struct {
	Model string `json:"model"`
}

type StopRequest struct {
	Model string `json:"model"`
}

type LoadRequest struct {
	Model       string `json:"model"`
	Stream      *bool  `json:"stream,omitempty"`
	KeepAlive   string `json:"keep_alive,omitempty"`
	NumCtx      int    `json:"num_ctx,omitempty"`
	NumParallel int    `json:"num_parallel,omitempty"`
	NGPULayers  *int   `json:"n_gpu_layers,omitempty"`
	CacheTypeK  string `json:"cache_type_k,omitempty"`
	CacheTypeV  string `json:"cache_type_v,omitempty"`
	DType       string `json:"dtype,omitempty"`
}

type LoadResponse struct {
	Status  string `json:"status"`
	Step    string `json:"step,omitempty"`
	Current int    `json:"current,omitempty"`
	Total   int    `json:"total,omitempty"`
}

// -- Response types --

type GenerateResponse struct {
	Model     string    `json:"model"`
	Response  string    `json:"response"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

type ChatResponse struct {
	Model     string    `json:"model"`
	Message   *Message  `json:"message,omitempty"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

type WebSearchOptions struct {
	Enabled bool   `json:"enabled,omitempty"`
	Query   string `json:"query,omitempty"`
}

type WebSearchSettings struct {
	Enabled        bool     `json:"enabled"`
	MaxResults     int      `json:"max_results"`
	Language       string   `json:"language,omitempty"`
	Providers      []string `json:"providers,omitempty"`
	SafeSearch     int      `json:"safe_search"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type WebSearchResult struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Snippet     string  `json:"snippet,omitempty"`
	Engine      string  `json:"engine,omitempty"`
	Category    string  `json:"category,omitempty"`
	Score       float64 `json:"score,omitempty"`
	PublishedAt string  `json:"published_at,omitempty"`
}

type TagsResponse struct {
	Models []ModelInfo `json:"models"`
}

type PipelineTagsResponse struct {
	PipelineTags []PipelineTagGroup `json:"pipeline_tags"`
}

type PipelineTagGroup struct {
	Category string   `json:"category"`
	Label    string   `json:"label"`
	Tags     []string `json:"tags"`
}

type LocalModelSearchResponse struct {
	Query       string      `json:"query,omitempty"`
	Format      string      `json:"format,omitempty"`
	PipelineTag string      `json:"pipeline_tag,omitempty"`
	Limit       int         `json:"limit"`
	Offset      int         `json:"offset"`
	Total       int         `json:"total"`
	HasMore     bool        `json:"has_more"`
	Models      []ModelInfo `json:"models"`
}

type ShowResponse struct {
	ModelFile string    `json:"modelfile"`
	Details   ModelInfo `json:"details"`
}

type ModelFileEntry struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256,omitempty"`
	LFS         bool   `json:"lfs,omitempty"`
	DownloadURL string `json:"download_url"`
}

type ModelManifestResponse struct {
	Details ModelInfo        `json:"details"`
	Files   []ModelFileEntry `json:"files"`
}

type PullResponse struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

type PsResponse struct {
	Models []RunningModel `json:"models"`
}

type RunningModel struct {
	Name      string    `json:"name"`
	Model     string    `json:"model"`
	Size      int64     `json:"size"`
	Format    string    `json:"format"`
	Status    string    `json:"status,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// -- Shared types --

type Message struct {
	Role             string       `json:"role"`
	Content          interface{}  `json:"content"`
	Thinking         string       `json:"thinking,omitempty"`
	ReasoningContent string       `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall   `json:"tool_calls,omitempty"`
	ToolName         string       `json:"tool_name,omitempty"`
	ToolCallID       string       `json:"tool_call_id,omitempty"`
	Meta             *MessageMeta `json:"meta,omitempty"`
}

type MessageMeta struct {
	Tokens     int               `json:"tokens,omitempty"`
	Speed      float64           `json:"speed,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`
	Estimated  bool              `json:"estimated,omitempty"`
	Sources    []WebSearchResult `json:"sources,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Index       *int        `json:"index,omitempty"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
	Arguments   interface{} `json:"arguments,omitempty"`
}

type ModelInfo struct {
	Name             string        `json:"name"`
	Model            string        `json:"model"`
	Size             int64         `json:"size"`
	Format           string        `json:"format"`
	ModifiedAt       time.Time     `json:"modified_at"`
	Label            string        `json:"label,omitempty"`
	DisplayName      string        `json:"display_name,omitempty"`
	Source           string        `json:"source,omitempty"`
	Provider         string        `json:"provider,omitempty"`
	PipelineTag      string        `json:"pipeline_tag,omitempty"`
	InputModalities  []string      `json:"input_modalities,omitempty"`
	OutputModalities []string      `json:"output_modalities,omitempty"`
	HasMMProj        bool          `json:"has_mmproj,omitempty"`
	ContextWindow    int64         `json:"context_window,omitempty"`
	Description      string        `json:"description,omitempty"`
	License          string        `json:"license,omitempty"`
	LLMType          string        `json:"llm_type,omitempty"`
	OwnedBy          string        `json:"owned_by,omitempty"`
	Pricing          *ModelPricing `json:"pricing,omitempty"`
}

type ModelPricing struct {
	InputTokenPrice  *ModelTokenPrice `json:"input_token_price,omitempty"`
	OutputTokenPrice *ModelTokenPrice `json:"output_token_price,omitempty"`
}

type ModelTokenPrice struct {
	Currency        string  `json:"currency,omitempty"`
	PricePerMillion float64 `json:"price_per_million"`
}

type ModelOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	TopK        int     `json:"top_k,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Seed        int     `json:"seed,omitempty"`
	NumCtx      int     `json:"num_ctx,omitempty"`
	NumParallel int     `json:"num_parallel,omitempty"`
	NGPULayers  *int    `json:"n_gpu_layers,omitempty"`
	CacheTypeK  string  `json:"cache_type_k,omitempty"`
	CacheTypeV  string  `json:"cache_type_v,omitempty"`
	DType       string  `json:"dtype,omitempty"`
}

// -- Dataset request types --

type DatasetPullRequest struct {
	Dataset string `json:"dataset"`
}

type DatasetDeleteRequest struct {
	Dataset string `json:"dataset"`
}

type DatasetShowRequest struct {
	Dataset string `json:"dataset"`
}

// -- Dataset response types --

type DatasetInfo struct {
	Name        string    `json:"name"`
	Dataset     string    `json:"dataset"`
	Size        int64     `json:"size"`
	Files       int       `json:"files"`
	ModifiedAt  time.Time `json:"modified_at"`
	Description string    `json:"description,omitempty"`
	License     string    `json:"license,omitempty"`
}

type DatasetTagsResponse struct {
	Datasets []DatasetInfo `json:"datasets"`
}

type DatasetSearchResponse struct {
	Query    string        `json:"query"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
	Total    int           `json:"total"`
	HasMore  bool          `json:"has_more"`
	Datasets []DatasetInfo `json:"datasets"`
}

type DatasetShowResponse struct {
	Details DatasetInfo `json:"details"`
	Files   []string    `json:"files,omitempty"`
}

type DatasetFilesRequest struct {
	Dataset string `json:"dataset"`
	Path    string `json:"path"`
}

type DatasetFileEntry struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	IsDir      bool      `json:"is_dir"`
	ModifiedAt time.Time `json:"modified_at"`
}

type DatasetFilesResponse struct {
	Dataset string             `json:"dataset"`
	Path    string             `json:"path"`
	Entries []DatasetFileEntry `json:"entries"`
}

type DatasetDownloadFile struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256,omitempty"`
	LFS         bool   `json:"lfs,omitempty"`
	DownloadURL string `json:"download_url"`
}

type DatasetManifestResponse struct {
	Details DatasetInfo           `json:"details"`
	Files   []DatasetDownloadFile `json:"files"`
}

type DatasetPullResponse struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

type SettingsResponse struct {
	Version    string            `json:"version"`
	StorageDir string            `json:"storage_dir"`
	ModelDir   string            `json:"model_dir"`
	DatasetDir string            `json:"dataset_dir"`
	Autostart  bool              `json:"autostart"`
	WebSearch  WebSearchSettings `json:"web_search"`
}

type SettingsUpdateRequest struct {
	StorageDir string             `json:"storage_dir,omitempty"`
	ModelDir   string             `json:"model_dir,omitempty"`
	DatasetDir string             `json:"dataset_dir,omitempty"`
	Autostart  *bool              `json:"autostart,omitempty"`
	WebSearch  *WebSearchSettings `json:"web_search,omitempty"`
}

type APIKeyInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type APIKeysResponse struct {
	AuthEnabled bool         `json:"auth_enabled"`
	Keys        []APIKeyInfo `json:"keys"`
}

type APIKeyCreateRequest struct {
	Name string `json:"name,omitempty"`
}

type APIKeyCreateResponse struct {
	Key    APIKeyInfo `json:"key"`
	APIKey string     `json:"api_key"`
}

type APIKeySettingsUpdateRequest struct {
	AuthEnabled bool `json:"auth_enabled"`
}

type APIUsageTotals struct {
	Requests     int64 `json:"requests"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type APIUsageRow struct {
	APIKeyID     string    `json:"api_key_id"`
	APIKeyName   string    `json:"api_key_name"`
	Model        string    `json:"model"`
	Source       string    `json:"source"`
	SourceType   string    `json:"source_type"`
	SourceName   string    `json:"source_name,omitempty"`
	Requests     int64     `json:"requests"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	TotalTokens  int64     `json:"total_tokens"`
	LastUsedAt   time.Time `json:"last_used_at"`
}

type APIUsageSourceTotal struct {
	Source       string `json:"source"`
	SourceType   string `json:"source_type"`
	SourceName   string `json:"source_name,omitempty"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

type APIUsageSummarySeries struct {
	Name string  `json:"name"`
	Type string  `json:"type"`
	Data []int64 `json:"data"`
}

type APIUsageTotalSummary struct {
	XAxis  []string                `json:"xAxis"`
	Series []APIUsageSummarySeries `json:"series"`
}

type APIUsageResponse struct {
	Period       string                `json:"period"`
	From         *time.Time            `json:"from,omitempty"`
	Totals       APIUsageTotals        `json:"totals"`
	TotalSummary APIUsageTotalSummary  `json:"total_summary"`
	SourceTotals []APIUsageSourceTotal `json:"source_totals"`
	Rows         []APIUsageRow         `json:"rows"`
}

type DirectoryBrowseRequest struct {
	Path string `json:"path,omitempty"`
}

type DirectoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type DirectoryBrowseResponse struct {
	CurrentPath string           `json:"current_path"`
	ParentPath  string           `json:"parent_path,omitempty"`
	HomePath    string           `json:"home_path,omitempty"`
	Roots       []string         `json:"roots"`
	Entries     []DirectoryEntry `json:"entries"`
}

// -- AI Apps request/response types --

type AIAppActionRequest struct {
	AppID   string `json:"app_id"`
	ModelID string `json:"model_id,omitempty"`
	Source  string `json:"source,omitempty"`
	WorkDir string `json:"work_dir,omitempty"`
}

type AIAppInstallRequest = AIAppActionRequest

type AIAppUninstallRequest = AIAppActionRequest

type AIAppOpenRequest = AIAppActionRequest

type AIAppInfo struct {
	ID               string    `json:"id"`
	Installed        bool      `json:"installed"`
	Managed          bool      `json:"managed"`
	Supported        bool      `json:"supported"`
	Disabled         bool      `json:"disabled"`
	Status           string    `json:"status"`
	Phase            string    `json:"phase,omitempty"`
	ProgressMode     string    `json:"progress_mode"`
	Progress         int       `json:"progress,omitempty"`
	InstallPath      string    `json:"install_path,omitempty"`
	Version          string    `json:"version,omitempty"`
	LatestVersion    string    `json:"latest_version,omitempty"`
	UpdateAvailable  bool      `json:"update_available,omitempty"`
	ModelID          string    `json:"model_id,omitempty"`
	RuntimeSupported bool      `json:"runtime_supported"`
	RuntimeRunning   bool      `json:"runtime_running"`
	RuntimeStatus    string    `json:"runtime_status,omitempty"`
	LogPath          string    `json:"log_path,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	DisabledReason   string    `json:"disabled_reason,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type AIAppsResponse struct {
	Apps []AIAppInfo `json:"apps"`
}

type AIAppOpenResponse struct {
	URL string `json:"url"`
}

// -- OpenAI-compatible types --

type OpenAIChatRequest struct {
	Model             string      `json:"model"`
	Source            string      `json:"source,omitempty"`
	Messages          []Message   `json:"messages"`
	Tools             []Tool      `json:"tools,omitempty"`
	ToolChoice        interface{} `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitempty"`
	Stream            *bool       `json:"stream,omitempty"`
	Temperature       *float64    `json:"temperature,omitempty"`
	TopP              *float64    `json:"top_p,omitempty"`
	MaxTokens         *int        `json:"max_tokens,omitempty"`
	NumCtx            *int        `json:"num_ctx,omitempty"`
	NumParallel       *int        `json:"num_parallel,omitempty"`
	NGPULayers        *int        `json:"n_gpu_layers,omitempty"`
	CacheTypeK        *string     `json:"cache_type_k,omitempty"`
	CacheTypeV        *string     `json:"cache_type_v,omitempty"`
	DType             *string     `json:"dtype,omitempty"`
	Seed              *int        `json:"seed,omitempty"`
	Stop              []string    `json:"stop,omitempty"`
}

type OpenAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

type OpenAIChoice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	FinishReason *string  `json:"finish_reason"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIModelList struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// -- Upgrade types --

type UpgradeCheckResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseNotes    string `json:"release_notes,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
}

type UpgradeProgressResponse struct {
	Status   string `json:"status"`   // "checking", "downloading", "extracting", "installing", "completed", "error"
	Progress int    `json:"progress"` // 0-100
	Message  string `json:"message"`
	Version  string `json:"version,omitempty"`
}

// -- Third-party Provider types --

type ThirdPartyProvider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key,omitempty"`
	Provider string `json:"provider,omitempty"` // e.g., "openai", "anthropic", "deepseek"
	Enabled  bool   `json:"enabled"`
}

type ThirdPartyProvidersResponse struct {
	Providers []ThirdPartyProvider `json:"providers"`
}

type ModelProvidersResponse struct {
	Providers []ProviderInfo `json:"providers"`
}

type ProviderInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source,omitempty"`
	ModelCount int    `json:"model_count"`
}

type ThirdPartyProviderCreateRequest struct {
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Provider string `json:"provider,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

type ThirdPartyProviderValidateRequest struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key,omitempty"`
	Provider string `json:"provider,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

type ThirdPartyProviderValidateResponse struct {
	Valid      bool `json:"valid"`
	ModelCount int  `json:"model_count"`
}

type ThirdPartyProviderUpdateRequest struct {
	Name     string `json:"name,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Provider string `json:"provider,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

type ProviderTagModelRequest struct {
	Model string `json:"model"`
}

type ProviderTagModelsReplaceRequest struct {
	Models []string `json:"models"`
}

// -- Conversation history types --

type Conversation struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	Model     string                `json:"model,omitempty"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
	Messages  []Message             `json:"messages"`
	Settings  *ConversationSettings `json:"settings,omitempty"`
}

type ConversationMeta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	MsgCount  int       `json:"msg_count"`
}

type ConversationSettings struct {
	NumCtx      int `json:"num_ctx,omitempty"`
	NumParallel int `json:"num_parallel,omitempty"`
}

type ConversationsListResponse struct {
	Conversations []ConversationMeta `json:"conversations"`
}
