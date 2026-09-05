package api

import (
	"encoding/json"
	"strings"
	"time"
)

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
	// ArtifactSource selects the model registry. Empty keeps the historical OpenCSG behavior.
	ArtifactSource string `json:"artifact_source,omitempty"`
	// Revision selects a branch, tag, or commit when supported by the registry.
	Revision string `json:"revision,omitempty"`
	// Quant selects a GGUF weight variant when multiple quantizations exist (e.g. Q4_K_M). Ignored for non-GGUF models.
	Quant string `json:"quant,omitempty"`
	// Quants selects one or more GGUF weight variants. When set, it takes precedence over Quant.
	Quants []string `json:"quants,omitempty"`
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
	Model       string              `json:"model"`
	Stream      *bool               `json:"stream,omitempty"`
	KeepAlive   string              `json:"keep_alive,omitempty"`
	NumCtx      int                 `json:"num_ctx,omitempty"`
	NumParallel int                 `json:"num_parallel,omitempty"`
	NGPULayers  *int                `json:"n_gpu_layers,omitempty"`
	CacheTypeK  string              `json:"cache_type_k,omitempty"`
	CacheTypeV  string              `json:"cache_type_v,omitempty"`
	DType       string              `json:"dtype,omitempty"`
	Speculative *SpeculativeOptions `json:"speculative,omitempty"`
}

type ImageRuntimeInstallRequest struct {
	UpgradePackages bool `json:"upgrade_packages,omitempty"`
}

type ASRRuntimeInstallRequest struct {
	UpgradePackages bool `json:"upgrade_packages,omitempty"`
}

type ModelUploadStartRequest struct {
	Model     string `json:"model,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

type ModelUploadStartResponse struct {
	UploadID string `json:"upload_id"`
}

type LoadResponse struct {
	Status  string `json:"status"`
	Step    string `json:"step,omitempty"`
	Current int    `json:"current,omitempty"`
	Total   int    `json:"total,omitempty"`
}

// -- Response types --

type HealthResponse struct {
	Status       string   `json:"status"`
	Version      string   `json:"version,omitempty"`
	APIProtocol  string   `json:"api_protocol"`
	PID          int      `json:"pid"`
	InstanceID   string   `json:"instance_id,omitempty"`
	DesktopMode  bool     `json:"desktop_mode"`
	StorageDir   string   `json:"storage_dir,omitempty"`
	Capabilities []string `json:"capabilities"`
}

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

type LocalInferenceSupport struct {
	Supported           bool   `json:"supported"`
	Runtime             string `json:"runtime,omitempty"`
	Mode                string `json:"mode"`
	Architecture        string `json:"architecture,omitempty"`
	RuntimeArchitecture string `json:"runtime_architecture,omitempty"`
}

type ModelManifestResponse struct {
	Details        ModelInfo             `json:"details"`
	Files          []ModelFileEntry      `json:"files"`
	LocalInference LocalInferenceSupport `json:"local_inference"`
}

type ModelUploadResponse struct {
	Status  string           `json:"status"`
	Model   string           `json:"model"`
	Details ModelInfo        `json:"details"`
	Files   []ModelFileEntry `json:"files"`
}

type PullResponse struct {
	Status         string `json:"status"`
	Digest         string `json:"digest,omitempty"`
	Total          int64  `json:"total,omitempty"`
	Completed      int64  `json:"completed,omitempty"`
	TotalBytes     int64  `json:"total_bytes,omitempty"`
	CompletedBytes int64  `json:"completed_bytes,omitempty"`
}

type PullJobResponse struct {
	ID             string       `json:"id"`
	Status         string       `json:"status"`
	Kind           string       `json:"kind"`
	Name           string       `json:"name"`
	ArtifactSource string       `json:"artifact_source,omitempty"`
	Revision       string       `json:"revision,omitempty"`
	Quant          string       `json:"quant,omitempty"`
	Quants         []string     `json:"quants,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	CompletedAt    *time.Time   `json:"completed_at,omitempty"`
	Progress       PullResponse `json:"progress"`
	Error          string       `json:"error,omitempty"`
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
	// Step describes the current load/conversion step for models whose
	// status is "loading" (e.g. "Installing CPU PyTorch for model conversion").
	Step        string `json:"step,omitempty"`
	StepCurrent int    `json:"step_current,omitempty"`
	StepTotal   int    `json:"step_total,omitempty"`
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
	Name              string        `json:"name"`
	Model             string        `json:"model"`
	Size              int64         `json:"size"`
	Format            string        `json:"format"`
	ModifiedAt        time.Time     `json:"modified_at"`
	Label             string        `json:"label,omitempty"`
	DisplayName       string        `json:"display_name,omitempty"`
	Source            string        `json:"source,omitempty"`
	Origin            string        `json:"origin,omitempty"`
	Provider          string        `json:"provider,omitempty"`
	ArtifactSource    string        `json:"artifact_source,omitempty"`
	Repository        string        `json:"repository,omitempty"`
	RequestedRevision string        `json:"requested_revision,omitempty"`
	ResolvedRevision  string        `json:"resolved_revision,omitempty"`
	Category          string        `json:"category,omitempty"`
	PipelineTag       string        `json:"pipeline_tag,omitempty"`
	InputModalities   []string      `json:"input_modalities,omitempty"`
	OutputModalities  []string      `json:"output_modalities,omitempty"`
	HasMMProj         bool          `json:"has_mmproj,omitempty"`
	ContextWindow     int64         `json:"context_window,omitempty"`
	MaxModelLen       int64         `json:"max_model_len,omitempty"`
	Description       string        `json:"description,omitempty"`
	License           string        `json:"license,omitempty"`
	LLMType           string        `json:"llm_type,omitempty"`
	OwnedBy           string        `json:"owned_by,omitempty"`
	Pricing           *ModelPricing `json:"pricing,omitempty"`
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
	Temperature float64             `json:"temperature,omitempty"`
	TopP        float64             `json:"top_p,omitempty"`
	TopK        int                 `json:"top_k,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Seed        int                 `json:"seed,omitempty"`
	NumCtx      int                 `json:"num_ctx,omitempty"`
	NumParallel int                 `json:"num_parallel,omitempty"`
	NGPULayers  *int                `json:"n_gpu_layers,omitempty"`
	CacheTypeK  string              `json:"cache_type_k,omitempty"`
	CacheTypeV  string              `json:"cache_type_v,omitempty"`
	DType       string              `json:"dtype,omitempty"`
	Speculative *SpeculativeOptions `json:"speculative,omitempty"`
}

type SpeculativeOptions struct {
	Types      []string `json:"types,omitempty"`
	DraftModel string   `json:"draft_model,omitempty"`
	DraftNMax  int      `json:"draft_n_max,omitempty"`
	DraftNMin  int      `json:"draft_n_min,omitempty"`
	DraftPMin  *float64 `json:"draft_p_min,omitempty"`
}

// -- Dataset request types --

type DatasetPullRequest struct {
	Dataset string `json:"dataset"`
	// ArtifactSource selects the dataset registry. Empty keeps the historical OpenCSG behavior.
	ArtifactSource string `json:"artifact_source,omitempty"`
	// Revision selects a branch, tag, or commit when supported by the registry.
	Revision string `json:"revision,omitempty"`
}

type DatasetDeleteRequest struct {
	Dataset        string `json:"dataset"`
	ArtifactSource string `json:"artifact_source,omitempty"`
}

type DatasetShowRequest struct {
	Dataset        string `json:"dataset"`
	ArtifactSource string `json:"artifact_source,omitempty"`
}

// -- Dataset response types --

type DatasetInfo struct {
	Name              string    `json:"name"`
	Dataset           string    `json:"dataset"`
	Size              int64     `json:"size"`
	Files             int       `json:"files"`
	ModifiedAt        time.Time `json:"modified_at"`
	Origin            string    `json:"origin,omitempty"`
	Description       string    `json:"description,omitempty"`
	License           string    `json:"license,omitempty"`
	ArtifactSource    string    `json:"artifact_source,omitempty"`
	Repository        string    `json:"repository,omitempty"`
	RequestedRevision string    `json:"requested_revision,omitempty"`
	ResolvedRevision  string    `json:"resolved_revision,omitempty"`
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
	Dataset        string `json:"dataset"`
	Path           string `json:"path"`
	ArtifactSource string `json:"artifact_source,omitempty"`
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
	Version                  string                `json:"version"`
	StorageDir               string                `json:"storage_dir"`
	ModelDir                 string                `json:"model_dir"`
	DatasetDir               string                `json:"dataset_dir"`
	ServerURL                string                `json:"server_url"`
	AIGatewayURL             string                `json:"ai_gateway_url"`
	CloudProviderName        string                `json:"cloud_provider_name"`
	DefaultCloudProviderName string                `json:"default_cloud_provider_name"`
	DefaultServerURL         string                `json:"default_server_url"`
	DefaultAIGatewayURL      string                `json:"default_ai_gateway_url"`
	HuggingFaceEndpoint      string                `json:"huggingface_endpoint"`
	HuggingFaceTokenSet      bool                  `json:"huggingface_token_configured"`
	ModelScopeEndpoint       string                `json:"modelscope_endpoint"`
	ModelScopeTokenSet       bool                  `json:"modelscope_token_configured"`
	MarketplaceModelSource   string                `json:"marketplace_model_source"`
	MarketplaceDatasetSource string                `json:"marketplace_dataset_source"`
	Autostart                bool                  `json:"autostart"`
	DesktopMode              bool                  `json:"desktop_mode"`
	LocalAPIURL              string                `json:"local_api_url,omitempty"`
	WebSearch                WebSearchSettings     `json:"web_search"`
	Observability            ObservabilitySettings `json:"observability"`
	LlamaUseModelMaxCtx      bool                  `json:"llama_use_model_max_ctx"`
	HiddenNavItems           []string              `json:"hidden_nav_items"`
}

type SettingsUpdateRequest struct {
	StorageDir               string                 `json:"storage_dir,omitempty"`
	ModelDir                 string                 `json:"model_dir,omitempty"`
	DatasetDir               string                 `json:"dataset_dir,omitempty"`
	ServerURL                *string                `json:"server_url,omitempty"`
	AIGatewayURL             *string                `json:"ai_gateway_url,omitempty"`
	CloudProviderName        *string                `json:"cloud_provider_name,omitempty"`
	HuggingFaceEndpoint      *string                `json:"huggingface_endpoint,omitempty"`
	HuggingFaceToken         *string                `json:"huggingface_token,omitempty"`
	ModelScopeEndpoint       *string                `json:"modelscope_endpoint,omitempty"`
	ModelScopeToken          *string                `json:"modelscope_token,omitempty"`
	MarketplaceModelSource   *string                `json:"marketplace_model_source,omitempty"`
	MarketplaceDatasetSource *string                `json:"marketplace_dataset_source,omitempty"`
	Autostart                *bool                  `json:"autostart,omitempty"`
	WebSearch                *WebSearchSettings     `json:"web_search,omitempty"`
	Observability            *ObservabilitySettings `json:"observability,omitempty"`
	LlamaUseModelMaxCtx      *bool                  `json:"llama_use_model_max_ctx,omitempty"`
}

type ObservabilitySettings struct {
	// RetentionDays is 0 when records should be retained indefinitely.
	RetentionDays int `json:"retention_days"`
}

type ObservabilityRequest struct {
	ID                         string    `json:"id"`
	RequestID                  string    `json:"request_id,omitempty"`
	TraceID                    string    `json:"trace_id"`
	B3TraceID                  string    `json:"b3_trace_id,omitempty"`
	ThreadID                   string    `json:"thread_id,omitempty"`
	StartedAt                  time.Time `json:"started_at"`
	CompletedAt                time.Time `json:"completed_at"`
	Method                     string    `json:"method"`
	Path                       string    `json:"path"`
	Protocol                   string    `json:"protocol"`
	Status                     string    `json:"status"`
	StatusCode                 int       `json:"status_code"`
	Stream                     bool      `json:"stream"`
	Model                      string    `json:"model"`
	Source                     string    `json:"source,omitempty"`
	SourceType                 string    `json:"source_type,omitempty"`
	SourceName                 string    `json:"source_name,omitempty"`
	APIKeyID                   string    `json:"api_key_id,omitempty"`
	APIKeyName                 string    `json:"api_key_name,omitempty"`
	PoolID                     string    `json:"pool_id,omitempty"`
	PoolName                   string    `json:"pool_name,omitempty"`
	PoolModel                  string    `json:"pool_model,omitempty"`
	ActualMemberID             string    `json:"actual_member_id,omitempty"`
	MemberModel                string    `json:"member_model,omitempty"`
	PoolPolicy                 string    `json:"pool_policy,omitempty"`
	RouterProfileID            string    `json:"router_profile_id,omitempty"`
	RouterProfileVersion       int       `json:"router_profile_version,omitempty"`
	RouterProfileSchemaVersion int       `json:"router_profile_schema_version,omitempty"`
	RouterAlgorithm            string    `json:"router_algorithm,omitempty"`
	RoutingTextVersion         string    `json:"routing_text_version,omitempty"`
	RouterConfidence           float64   `json:"router_confidence,omitempty"`
	RouterMargin               float64   `json:"router_margin,omitempty"`
	RouterSimilarity           float64   `json:"router_similarity,omitempty"`
	SemanticRouted             bool      `json:"semantic_routed,omitempty"`
	SemanticCluster            int       `json:"semantic_cluster"`
	SemanticClusterID          string    `json:"semantic_cluster_id,omitempty"`
	SemanticDistance           float64   `json:"semantic_distance,omitempty"`
	SemanticOOD                bool      `json:"semantic_ood,omitempty"`
	SemanticFallback           bool      `json:"semantic_fallback,omitempty"`
	SemanticFallbackReason     string    `json:"semantic_fallback_reason,omitempty"`
	PriceInputPerMillion       float64   `json:"price_input_per_million"`
	PriceOutputPerMillion      float64   `json:"price_output_per_million"`
	EstimatedCost              float64   `json:"estimated_cost"`
	CostCurrency               string    `json:"cost_currency,omitempty"`
	CostKnown                  bool      `json:"cost_known"`
	FallbackCount              int64     `json:"fallback_count"`
	LimitedCount               int64     `json:"limited_count"`
	InputTokens                int64     `json:"input_tokens"`
	OutputTokens               int64     `json:"output_tokens"`
	TotalTokens                int64     `json:"total_tokens"`
	CacheReadInputTokens       int64     `json:"cache_read_input_tokens"`
	CacheCreationTokens        int64     `json:"cache_creation_input_tokens"`
	CacheEligibleTokens        int64     `json:"cache_eligible_input_tokens"`
	CacheHitRate               float64   `json:"cache_hit_rate"`
	DurationMS                 int64     `json:"duration_ms"`
	FirstTokenLatencyMS        int64     `json:"first_token_latency_ms"`
	ErrorMessage               string    `json:"error_message,omitempty"`
	RequestBody                string    `json:"request_body,omitempty"`
	ResponseBody               string    `json:"response_body,omitempty"`
	RequestBodyTruncated       bool      `json:"request_body_truncated"`
	ResponseBodyTruncated      bool      `json:"response_body_truncated"`
}

type ObservabilityRequestSummary struct {
	Requests       int64   `json:"requests"`
	Succeeded      int64   `json:"succeeded"`
	Failed         int64   `json:"failed"`
	TotalTokens    int64   `json:"total_tokens"`
	AverageLatency float64 `json:"average_latency_ms"`
}

type ObservabilityRequestListResponse struct {
	Items   []ObservabilityRequest      `json:"items"`
	Total   int64                       `json:"total"`
	Limit   int                         `json:"limit"`
	Offset  int                         `json:"offset"`
	Summary ObservabilityRequestSummary `json:"summary"`
}

type ObservabilityTrace struct {
	TraceID      string    `json:"trace_id"`
	ThreadID     string    `json:"thread_id,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at"`
	Status       string    `json:"status"`
	RequestCount int64     `json:"request_count"`
	Models       []string  `json:"models"`
	TotalTokens  int64     `json:"total_tokens"`
	DurationMS   int64     `json:"duration_ms"`
}

type ObservabilityTraceListResponse struct {
	Items  []ObservabilityTrace `json:"items"`
	Total  int64                `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

type ObservabilityTraceDetailResponse struct {
	Trace    ObservabilityTrace     `json:"trace"`
	Requests []ObservabilityRequest `json:"requests"`
}

type ObservabilityFacetValue struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Count int64  `json:"count"`
}

type ObservabilityFacetsResponse struct {
	Models []ObservabilityFacetValue `json:"models"`
	Routes []ObservabilityFacetValue `json:"routes"`
}

type DatasetExportRequest struct {
	TraceIDs        []string                  `json:"trace_ids,omitempty"`
	Filter          *DatasetExportTraceFilter `json:"filter,omitempty"`
	Format          string                    `json:"format"`
	RedactionPolicy string                    `json:"redaction_policy,omitempty"`
	Confirmed       bool                      `json:"confirmed,omitempty"`
	DatasetName     string                    `json:"dataset_name,omitempty"`
}

type DatasetExportTraceFilter struct {
	From   *time.Time `json:"from,omitempty"`
	To     *time.Time `json:"to,omitempty"`
	Status string     `json:"status,omitempty"`
	Model  string     `json:"model,omitempty"`
	Source string     `json:"source,omitempty"`
	Query  string     `json:"q,omitempty"`
}

type DatasetExportRisk struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type DatasetExportPreviewResponse struct {
	Selected int                 `json:"selected"`
	Exported int                 `json:"exported"`
	Excluded int                 `json:"excluded"`
	Degraded int                 `json:"degraded"`
	Risks    []DatasetExportRisk `json:"risks"`
	Sample   json.RawMessage     `json:"sample,omitempty"`
}

type DatasetExportFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type DatasetExportResponse struct {
	ID          string              `json:"id"`
	DatasetID   string              `json:"dataset_id"`
	Format      string              `json:"format"`
	CreatedAt   time.Time           `json:"created_at"`
	Selected    int                 `json:"selected"`
	Exported    int                 `json:"exported"`
	Excluded    int                 `json:"excluded"`
	Degraded    int                 `json:"degraded"`
	Risks       []DatasetExportRisk `json:"risks"`
	Files       []DatasetExportFile `json:"files"`
	DownloadURL string              `json:"download_url"`
}

type DatasetExportJobResponse struct {
	ID        string                 `json:"id"`
	Status    string                 `json:"status"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Error     string                 `json:"error,omitempty"`
	Export    *DatasetExportResponse `json:"export,omitempty"`
}

type DatasetPublishRequest struct {
	Create        bool   `json:"create"`
	Name          string `json:"name"`
	Nickname      string `json:"nickname,omitempty"`
	Description   string `json:"description,omitempty"`
	Private       bool   `json:"private"`
	ConfirmPublic bool   `json:"confirm_public,omitempty"`
	License       string `json:"license,omitempty"`
}

type DatasetPublishResponse struct {
	DatasetID     string              `json:"dataset_id"`
	Revision      string              `json:"revision"`
	URL           string              `json:"url"`
	AgenticHubURL string              `json:"agentichub_url"`
	Files         []DatasetExportFile `json:"files"`
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
	Requests      int64 `json:"requests"`
	InputTokens   int64 `json:"input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	TotalTokens   int64 `json:"total_tokens"`
	LocalTokens   int64 `json:"local_tokens"`
	CloudTokens   int64 `json:"cloud_tokens"`
	PoolRequests  int64 `json:"pool_requests"`
	FallbackCount int64 `json:"fallback_count"`
	LimitedCount  int64 `json:"limited_count"`
}

type APIUsageRow struct {
	APIKeyID       string    `json:"api_key_id"`
	APIKeyName     string    `json:"api_key_name"`
	Model          string    `json:"model"`
	Source         string    `json:"source"`
	SourceType     string    `json:"source_type"`
	SourceName     string    `json:"source_name,omitempty"`
	PoolID         string    `json:"pool_id,omitempty"`
	PoolName       string    `json:"pool_name,omitempty"`
	PoolModel      string    `json:"pool_model,omitempty"`
	ActualMemberID string    `json:"actual_member_id,omitempty"`
	MemberModel    string    `json:"member_model,omitempty"`
	EstimatedCost  float64   `json:"estimated_cost,omitempty"`
	CostCurrency   string    `json:"cost_currency,omitempty"`
	CostKnown      bool      `json:"cost_known"`
	FallbackCount  int64     `json:"fallback_count,omitempty"`
	LimitedCount   int64     `json:"limited_count,omitempty"`
	Requests       int64     `json:"requests"`
	InputTokens    int64     `json:"input_tokens"`
	OutputTokens   int64     `json:"output_tokens"`
	TotalTokens    int64     `json:"total_tokens"`
	LastUsedAt     time.Time `json:"last_used_at"`
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

type APIUsagePoolMemberTotal struct {
	Source        string `json:"source"`
	SourceType    string `json:"source_type"`
	SourceName    string `json:"source_name,omitempty"`
	Model         string `json:"model"`
	Requests      int64  `json:"requests"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	TotalTokens   int64  `json:"total_tokens"`
	FallbackCount int64  `json:"fallback_count"`
	LimitedCount  int64  `json:"limited_count"`
}

type APIUsagePoolTotal struct {
	PoolID        string                    `json:"pool_id"`
	PoolName      string                    `json:"pool_name"`
	PoolModel     string                    `json:"pool_model"`
	Requests      int64                     `json:"requests"`
	InputTokens   int64                     `json:"input_tokens"`
	OutputTokens  int64                     `json:"output_tokens"`
	TotalTokens   int64                     `json:"total_tokens"`
	FallbackCount int64                     `json:"fallback_count"`
	LimitedCount  int64                     `json:"limited_count"`
	Members       []APIUsagePoolMemberTotal `json:"members"`
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
	TotalHistory int64                 `json:"total_history"`
	TotalSummary APIUsageTotalSummary  `json:"total_summary"`
	SourceTotals []APIUsageSourceTotal `json:"source_totals"`
	PoolTotals   []APIUsagePoolTotal   `json:"pool_totals"`
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
	AppID         string              `json:"app_id"`
	ModelID       string              `json:"model_id,omitempty"`
	Source        string              `json:"source,omitempty"`
	ProviderMode  string              `json:"provider_mode,omitempty"`
	ModelBindings []AIAppModelBinding `json:"model_bindings,omitempty"`
	WorkDir       string              `json:"work_dir,omitempty"`
}

type AIAppInstallRequest = AIAppActionRequest

type AIAppUninstallRequest = AIAppActionRequest

type AIAppOpenRequest = AIAppActionRequest

type AIAppProviderRequest = AIAppActionRequest

// AIAppPathRequest sets a manual install location for an AI app whose
// automatic detection missed the user's custom install path.
type AIAppPathRequest struct {
	AppID string `json:"app_id"`
	Path  string `json:"path"`
}

type AIAppInfo struct {
	ID                      string              `json:"id"`
	Installed               bool                `json:"installed"`
	Managed                 bool                `json:"managed"`
	Supported               bool                `json:"supported"`
	Disabled                bool                `json:"disabled"`
	Status                  string              `json:"status"`
	Phase                   string              `json:"phase,omitempty"`
	ProgressMode            string              `json:"progress_mode"`
	Progress                int                 `json:"progress,omitempty"`
	InstallPath             string              `json:"install_path,omitempty"`
	Version                 string              `json:"version,omitempty"`
	LatestVersion           string              `json:"latest_version,omitempty"`
	UpdateAvailable         bool                `json:"update_available,omitempty"`
	ModelID                 string              `json:"model_id,omitempty"`
	ModelSource             string              `json:"model_source,omitempty"`
	ProviderMode            string              `json:"provider_mode,omitempty"`
	ProviderGroup           string              `json:"provider_group,omitempty"`
	ProviderSwitchSupported bool                `json:"provider_switch_supported,omitempty"`
	ProviderDrifted         bool                `json:"provider_drifted,omitempty"`
	ModelBindings           []AIAppModelBinding `json:"model_bindings,omitempty"`
	ModelSlots              []AIAppModelSlot    `json:"model_slots,omitempty"`
	RuntimeSupported        bool                `json:"runtime_supported"`
	RuntimeRunning          bool                `json:"runtime_running"`
	RuntimeStatus           string              `json:"runtime_status,omitempty"`
	LogPath                 string              `json:"log_path,omitempty"`
	LastError               string              `json:"last_error,omitempty"`
	DisabledReason          string              `json:"disabled_reason,omitempty"`
	UpdatedAt               time.Time           `json:"updated_at"`
}

// AIAppModelBinding selects one model, including its source when model IDs are
// shared by multiple sources, for a task exposed by an AI app.
type AIAppModelBinding struct {
	Task    string `json:"task"`
	ModelID string `json:"model_id"`
	Source  string `json:"source"`
}

// AIAppModelSlot describes a model task supported by an AI app and its current
// saved or recommended binding.
type AIAppModelSlot struct {
	Task     string             `json:"task"`
	Required bool               `json:"required"`
	Binding  *AIAppModelBinding `json:"binding,omitempty"`
}

type AIAppsResponse struct {
	Apps []AIAppInfo `json:"apps"`
}

type AIAppOpenResponse struct {
	URL  string `json:"url,omitempty"`
	Mode string `json:"mode,omitempty"`
}

// -- OpenAI-compatible types --

type OpenAIChatRequest struct {
	Model              string                 `json:"model"`
	Source             string                 `json:"source,omitempty"`
	Messages           []Message              `json:"messages"`
	Tools              []Tool                 `json:"tools,omitempty"`
	ToolChoice         interface{}            `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool                  `json:"parallel_tool_calls,omitempty"`
	ChatTemplateKwargs map[string]interface{} `json:"chat_template_kwargs,omitempty"`
	StreamOptions      map[string]interface{} `json:"stream_options,omitempty"`
	Stream             *bool                  `json:"stream,omitempty"`
	Temperature        *float64               `json:"temperature,omitempty"`
	TopP               *float64               `json:"top_p,omitempty"`
	MaxTokens          *int                   `json:"max_tokens,omitempty"`
	NumCtx             *int                   `json:"num_ctx,omitempty"`
	NumParallel        *int                   `json:"num_parallel,omitempty"`
	NGPULayers         *int                   `json:"n_gpu_layers,omitempty"`
	CacheTypeK         *string                `json:"cache_type_k,omitempty"`
	CacheTypeV         *string                `json:"cache_type_v,omitempty"`
	DType              *string                `json:"dtype,omitempty"`
	Seed               *int                   `json:"seed,omitempty"`
	Stop               []string               `json:"stop,omitempty"`
}

type OpenAIEmbeddingsRequest struct {
	Model      string      `json:"model"`
	Source     string      `json:"source,omitempty"`
	Input      interface{} `json:"input"`
	NumCtx     *int        `json:"num_ctx,omitempty"`
	NGPULayers *int        `json:"n_gpu_layers,omitempty"`
	DType      *string     `json:"dtype,omitempty"`
}

type OpenAIImagesGenerationRequest struct {
	Model          string   `json:"model"`
	Prompt         string   `json:"prompt"`
	N              *int     `json:"n,omitempty"`
	Size           string   `json:"size,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Seed           *int     `json:"seed,omitempty"`
	NegativePrompt string   `json:"negative_prompt,omitempty"`
	Steps          *int     `json:"steps,omitempty"`
	CFGScale       *float64 `json:"cfg_scale,omitempty"`
	Source         string   `json:"source,omitempty"`
	Image          string   `json:"image,omitempty"`
	Images         []string `json:"images,omitempty"`
}

type OpenAIAudioTranscriptionRequest struct {
	Model          string   `json:"model"`
	FilePath       string   `json:"file_path"`
	Source         string   `json:"source,omitempty"`
	Language       string   `json:"language,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Stream         bool     `json:"stream,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	Hotwords       []string `json:"hotwords,omitempty"`
	ITN            *bool    `json:"itn,omitempty"`
}

type OpenAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

type OpenAIEmbeddingsResponse struct {
	Object string                  `json:"object"`
	Data   []OpenAIEmbeddingObject `json:"data"`
	Model  string                  `json:"model"`
	Usage  OpenAIUsage             `json:"usage"`
}

type OpenAIImagesGenerationResponse struct {
	Created int64         `json:"created"`
	Data    []OpenAIImage `json:"data"`
}

type OpenAIAudioTranscriptionResponse struct {
	Text     string                 `json:"text"`
	Task     string                 `json:"task,omitempty"`
	Language string                 `json:"language,omitempty"`
	Duration *float64               `json:"duration,omitempty"`
	Segments []OpenAIAudioSegment   `json:"segments,omitempty"`
	Backend  string                 `json:"backend,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type OpenAIAudioSegment struct {
	ID    int     `json:"id,omitempty"`
	Start float64 `json:"start,omitempty"`
	End   float64 `json:"end,omitempty"`
	Text  string  `json:"text"`
}

type ImageGenerationJobResponse struct {
	ID          string                          `json:"id"`
	Status      string                          `json:"status"`
	CreatedAt   time.Time                       `json:"created_at"`
	UpdatedAt   time.Time                       `json:"updated_at"`
	CompletedAt *time.Time                      `json:"completed_at,omitempty"`
	Request     OpenAIImagesGenerationRequest   `json:"request"`
	Result      *OpenAIImagesGenerationResponse `json:"result,omitempty"`
	Error       string                          `json:"error,omitempty"`
}

type ImageGenerationJobListResponse struct {
	Jobs []ImageGenerationJobResponse `json:"jobs"`
}

type OpenAIImage struct {
	B64JSON       string `json:"b64_json,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type OpenAIEmbeddingObject struct {
	Object    string      `json:"object"`
	Embedding interface{} `json:"embedding"`
	Index     int         `json:"index"`
}

type OpenAIChoice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	FinishReason *string  `json:"finish_reason"`
}

type OpenAIUsage struct {
	PromptTokens        int                        `json:"prompt_tokens"`
	CompletionTokens    int                        `json:"completion_tokens"`
	TotalTokens         int                        `json:"total_tokens"`
	PromptTokensDetails *OpenAIPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
	CachedTokens        int                        `json:"cached_tokens,omitempty"`
}

type OpenAIPromptTokensDetails struct {
	CachedTokens      int `json:"cached_tokens"`
	WriteCachedTokens int `json:"write_cached_tokens,omitempty"`
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
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	BaseURL  string           `json:"base_url"`
	APIKey   string           `json:"api_key,omitempty"`
	Provider string           `json:"provider,omitempty"` // e.g., "openai", "anthropic", "deepseek"
	Enabled  bool             `json:"enabled"`
	Headers  []ProviderHeader `json:"headers,omitempty"`
}

type ProviderHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
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
	Name     string           `json:"name"`
	BaseURL  string           `json:"base_url"`
	APIKey   string           `json:"api_key"`
	Provider string           `json:"provider,omitempty"`
	Enabled  *bool            `json:"enabled,omitempty"`
	Headers  []ProviderHeader `json:"headers,omitempty"`
}

type ThirdPartyProviderValidateRequest struct {
	ID       string           `json:"id,omitempty"`
	Name     string           `json:"name,omitempty"`
	BaseURL  string           `json:"base_url"`
	APIKey   string           `json:"api_key,omitempty"`
	Provider string           `json:"provider,omitempty"`
	Enabled  *bool            `json:"enabled,omitempty"`
	Headers  []ProviderHeader `json:"headers,omitempty"`
	Probe    bool             `json:"probe,omitempty"`
}

type ThirdPartyProviderValidateResponse struct {
	Valid      bool `json:"valid"`
	ModelCount int  `json:"model_count"`
}

type ThirdPartyProviderUpdateRequest struct {
	Name     string           `json:"name,omitempty"`
	BaseURL  string           `json:"base_url,omitempty"`
	APIKey   string           `json:"api_key,omitempty"`
	Provider string           `json:"provider,omitempty"`
	Enabled  *bool            `json:"enabled,omitempty"`
	Headers  []ProviderHeader `json:"headers,omitempty"`
}

// ProviderPool exposes one public model ID and selects one of its members.
type ProviderPool struct {
	ID                      string               `json:"id"`
	Name                    string               `json:"name"`
	Model                   string               `json:"model"`
	Enabled                 bool                 `json:"enabled"`
	Policy                  string               `json:"policy"`
	PolicyAvailable         bool                 `json:"policy_available"`
	PolicyUnavailableReason string               `json:"policy_unavailable_reason,omitempty"`
	Members                 []ProviderPoolMember `json:"members"`
}

type ProviderPoolMember struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	Model         string `json:"model"`
	Priority      int    `json:"priority,omitempty"`
	Weight        int    `json:"weight,omitempty"`
	RequestsPM    int    `json:"requests_per_minute,omitempty"`
	TokensPM      int    `json:"tokens_per_minute,omitempty"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"`
}

type ProviderPoolsResponse struct {
	Pools []ProviderPool `json:"pools"`
}

type ProviderPoolCreateRequest struct {
	Name    string               `json:"name"`
	Model   string               `json:"model"`
	Enabled *bool                `json:"enabled,omitempty"`
	Policy  string               `json:"policy,omitempty"`
	Members []ProviderPoolMember `json:"members"`
}

type ProviderPoolUpdateRequest struct {
	Name    *string               `json:"name,omitempty"`
	Model   *string               `json:"model,omitempty"`
	Enabled *bool                 `json:"enabled,omitempty"`
	Policy  *string               `json:"policy,omitempty"`
	Members *[]ProviderPoolMember `json:"members,omitempty"`
}

type ProviderPoolPolicyCapability struct {
	Type         string `json:"type"`
	Experimental bool   `json:"experimental"`
	Available    bool   `json:"available"`
	Reason       string `json:"reason,omitempty"`
}

type ProviderPoolPolicyCapabilitiesResponse struct {
	Policies []ProviderPoolPolicyCapability `json:"policies"`
}

type ProviderPoolRouterSuggestion struct {
	ID                  string    `json:"id"`
	Reason              string    `json:"reason"`
	QualifiedQueryCount int       `json:"qualified_query_count"`
	NewQueryCount       int       `json:"new_query_count"`
	MemberCompatible    bool      `json:"member_compatible"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ProviderPoolRouterEvaluationRequest struct {
	EvaluationMode        string  `json:"evaluation_mode,omitempty"`
	BaseProfileID         string  `json:"base_profile_id,omitempty"`
	JudgeModel            string  `json:"judge_model"`
	MaxQueries            int     `json:"max_queries"`
	Repeats               int     `json:"repeats"`
	MaxOutputTokens       int     `json:"max_output_tokens"`
	RequestTimeoutSeconds int     `json:"request_timeout_seconds"`
	BudgetCurrency        string  `json:"budget_currency"`
	BudgetAmount          float64 `json:"budget_amount"`
	AllowUnknownPricing   bool    `json:"allow_unknown_pricing"`
}

type ProviderPoolRouterEvaluationLimits struct {
	MaxQueries               int `json:"max_queries"`
	MaxRepeats               int `json:"max_repeats"`
	MaxOutputTokens          int `json:"max_output_tokens"`
	MaxRequestTimeoutSeconds int `json:"max_request_timeout_seconds"`
}

type ProviderPoolRouterTarget struct {
	Source string `json:"source"`
	Model  string `json:"model"`
}

type ProviderPoolRouterEvaluationPreview struct {
	EvaluationMode                string                             `json:"evaluation_mode"`
	EligibleSnapshotCount         int                                `json:"eligible_snapshot_count"`
	SelectedSnapshotCount         int                                `json:"selected_snapshot_count"`
	DirectCandidateCalls          int                                `json:"direct_candidate_calls"`
	JudgeCalls                    int                                `json:"judge_calls"`
	MaxJudgeCalls                 int                                `json:"max_judge_calls"`
	MaxTotalCalls                 int                                `json:"max_total_calls"`
	JudgePromptTokens             int64                              `json:"judge_prompt_tokens"`
	MaxJudgeTokenExposure         int64                              `json:"max_judge_token_exposure"`
	MaxTokenExposure              int64                              `json:"max_token_exposure"`
	KnownJudgeEstimatedCost       float64                            `json:"known_judge_estimated_cost"`
	KnownEstimatedCost            float64                            `json:"known_estimated_cost"`
	Currency                      string                             `json:"currency"`
	UnknownPriceMembers           []ProviderPoolRouterTarget         `json:"unknown_price_members"`
	JudgePriceKnown               bool                               `json:"judge_price_known"`
	RequiresUnknownPricingConsent bool                               `json:"requires_unknown_pricing_consent"`
	Limits                        ProviderPoolRouterEvaluationLimits `json:"limits"`
}

type ProviderPoolRouterEvaluationJob struct {
	ID                      string     `json:"id"`
	EvaluationMode          string     `json:"evaluation_mode"`
	BaseProfileID           string     `json:"base_profile_id,omitempty"`
	MemberCompatible        bool       `json:"member_compatible"`
	JudgeModel              string     `json:"judge_model"`
	MaxQueries              int        `json:"max_queries"`
	Repeats                 int        `json:"repeats"`
	MaxOutputTokens         int        `json:"max_output_tokens"`
	RequestTimeoutSeconds   int        `json:"request_timeout_seconds"`
	BudgetCurrency          string     `json:"budget_currency"`
	BudgetAmount            float64    `json:"budget_amount"`
	AllowUnknownPricing     bool       `json:"allow_unknown_pricing"`
	DirectCandidateCalls    int        `json:"direct_candidate_calls,omitempty"`
	JudgeCalls              int        `json:"judge_calls,omitempty"`
	MaxJudgeCalls           int        `json:"max_judge_calls,omitempty"`
	JudgePromptTokens       int64      `json:"judge_prompt_tokens,omitempty"`
	MaxJudgeTokenExposure   int64      `json:"max_judge_token_exposure,omitempty"`
	MaxTokenExposure        int64      `json:"max_token_exposure,omitempty"`
	KnownJudgeEstimatedCost float64    `json:"known_judge_estimated_cost,omitempty"`
	KnownEstimatedCost      float64    `json:"known_estimated_cost,omitempty"`
	EstimateCurrency        string     `json:"estimate_currency,omitempty"`
	UnknownPricing          bool       `json:"unknown_pricing,omitempty"`
	Current                 int        `json:"current"`
	Total                   int        `json:"total"`
	Phase                   string     `json:"phase,omitempty"`
	CancellationRequested   bool       `json:"cancellation_requested"`
	Status                  string     `json:"status"`
	Error                   string     `json:"error,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	StartedAt               *time.Time `json:"started_at,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

type ProviderPoolRouterEvaluationJobsResponse struct {
	Items  []ProviderPoolRouterEvaluationJob `json:"items"`
	Limit  int                               `json:"limit"`
	Offset int                               `json:"offset"`
}

type ProviderPoolRouterBaselineResult struct {
	Quality float64 `json:"quality"`
	Cost    float64 `json:"cost"`
}

type ProviderPoolRouterBaselines struct {
	BestSingleModel ProviderPoolRouterBaselineResult `json:"best_single_model"`
	CheapestModel   ProviderPoolRouterBaselineResult `json:"cheapest_model"`
	RandomModel     ProviderPoolRouterBaselineResult `json:"random_model"`
	OracleModel     ProviderPoolRouterBaselineResult `json:"oracle_model"`
}

type ProviderPoolRouterMetrics struct {
	QueryCount              int            `json:"query_count"`
	CellCount               int            `json:"cell_count"`
	TrialCount              int            `json:"trial_count"`
	Repeats                 int            `json:"repeats"`
	ResponseOutcomes        map[string]int `json:"response_outcomes"`
	WinRate                 float64        `json:"win_rate"`
	Spend                   float64        `json:"spend"`
	TotalCost               float64        `json:"total_cost"`
	Currency                string         `json:"currency,omitempty"`
	CostUnit                string         `json:"cost_unit"`
	MonetarySpendKnown      bool           `json:"monetary_spend_known"`
	UnknownMonetarySpend    bool           `json:"unknown_monetary_spend"`
	TrainQueryCount         int            `json:"train_query_count"`
	HeldOutQueryCount       int            `json:"held_out_query_count"`
	CVFoldCount             int            `json:"cv_fold_count"`
	TrainUtility            float64        `json:"train_utility"`
	TrainQuality            float64        `json:"train_quality"`
	TrainCost               float64        `json:"train_cost_score"`
	HeldOutUtility          float64        `json:"held_out_utility"`
	HeldOutQuality          float64        `json:"held_out_quality"`
	HeldOutCost             float64        `json:"held_out_cost_score"`
	AllClustersOneMember    bool           `json:"all_clusters_one_member"`
	SemanticDifferentiation bool           `json:"semantic_differentiation"`
	Baselines               ProviderPoolRouterBaselines `json:"baselines"`
}

type ProviderPoolRouterDistanceQuantiles struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type ProviderPoolRouterCluster struct {
	ID               string                              `json:"id"`
	TargetMemberID   string                              `json:"target_member_id"`
	Target           ProviderPoolRouterTarget            `json:"target"`
	SampleCount      int                                 `json:"sample_count"`
	DistanceQuantile ProviderPoolRouterDistanceQuantiles `json:"distance_quantiles"`
	OODThreshold     float64                             `json:"ood_threshold"`
}

type ProviderPoolRouterCandidateDistribution struct {
	MemberID     string                   `json:"member_id"`
	Target       ProviderPoolRouterTarget `json:"target"`
	ClusterCount int                      `json:"cluster_count"`
	SampleCount  int                      `json:"sample_count"`
	Fraction     float64                  `json:"fraction,omitempty"`
}

type ProviderPoolRouterPairwiseMetrics struct {
	Count            int     `json:"count"`
	LogLoss          float64 `json:"log_loss"`
	Brier            float64 `json:"brier"`
	TopClassAccuracy float64 `json:"top_class_accuracy"`
	ECE              float64 `json:"ece"`
}

type ProviderPoolRouterQualityCost struct {
	Quality   float64 `json:"quality"`
	Cost      float64 `json:"cost,omitempty"`
	CostKnown bool    `json:"cost_known"`
	Currency  string  `json:"currency,omitempty"`
}

type ProviderPoolRouterThresholds struct {
	MinimumConfidence float64 `json:"minimum_confidence"`
	MinimumMargin     float64 `json:"minimum_margin"`
	MinimumSimilarity float64 `json:"minimum_similarity"`
	QualitySlack      float64 `json:"quality_slack"`
}

// ProviderPoolRouterV2Summary intentionally exposes only bounded diagnostics.
// Learner samples, embeddings, forest nodes, prompts, and responses stay private.
type ProviderPoolRouterV2Summary struct {
	ProfileAlgorithm       string                            `json:"profile_algorithm"`
	ModelType              string                            `json:"model_type"`
	ModelFallbackReason    string                            `json:"model_fallback_reason,omitempty"`
	SampleCount            int                               `json:"sample_count"`
	QueryGroupCount        int                               `json:"query_group_count"`
	RoundCount             int                               `json:"round_count"`
	CVFoldCount            int                               `json:"cv_fold_count"`
	TargetQualityRetention float64                           `json:"target_quality_retention"`
	ConfidenceLevel        float64                           `json:"confidence_level"`
	Baseline               ProviderPoolRouterQualityCost     `json:"baseline_best_single_model"`
	Routed                 ProviderPoolRouterQualityCost     `json:"routed"`
	PointRetention         float64                           `json:"point_retention"`
	ConservativeRetention  float64                           `json:"conservative_retention"`
	RetentionLowerBound    float64                           `json:"retention_lower_bound"`
	Savings                float64                           `json:"savings,omitempty"`
	SavingsFraction        float64                           `json:"savings_fraction,omitempty"`
	SavingsKnown           bool                              `json:"savings_known"`
	Coverage               float64                           `json:"coverage"`
	FallbackRate           float64                           `json:"fallback_rate"`
	LowConfidenceRate      float64                           `json:"low_confidence_rate"`
	OODRate                float64                           `json:"ood_rate"`
	PairwiseMetrics        ProviderPoolRouterPairwiseMetrics `json:"pairwise_metrics"`
	Thresholds             ProviderPoolRouterThresholds      `json:"thresholds"`
	OptimizeKnownCost      bool                              `json:"optimize_known_cost"`
	QualityFeasible        bool                              `json:"quality_feasible"`
	KnownCostFeasible      bool                              `json:"known_cost_feasible"`
	InsufficientEvidence   bool                              `json:"insufficient_evidence"`
	CollapsedMemberID      string                            `json:"collapsed_member_id,omitempty"`
	Warnings               []string                          `json:"warnings,omitempty"`
}

type ProviderPoolRouterProfile struct {
	ID                      string                                    `json:"id"`
	Version                 int                                       `json:"version"`
	SchemaVersion           int                                       `json:"schema_version"`
	RouterAlgorithm         string                                    `json:"router_algorithm,omitempty"`
	MemberCompatible        bool                                      `json:"member_compatible"`
	MemberFingerprintDrift  bool                                      `json:"member_fingerprint_drift"`
	Active                  bool                                      `json:"active"`
	CreatedAt               time.Time                                 `json:"created_at"`
	CreatedBy               string                                    `json:"created_by,omitempty"`
	SourceJobID             string                                    `json:"source_job_id,omitempty"`
	Description             string                                    `json:"description,omitempty"`
	GeneratedAt             time.Time                                 `json:"generated_at"`
	Distance                string                                    `json:"distance"`
	CostUnit                string                                    `json:"cost_unit"`
	FallbackMemberID        string                                    `json:"fallback_member_id"`
	Metrics                 ProviderPoolRouterMetrics                 `json:"metrics"`
	Clusters                []ProviderPoolRouterCluster               `json:"clusters,omitempty"`
	CandidateDistribution   []ProviderPoolRouterCandidateDistribution `json:"candidate_distribution,omitempty"`
	ActivationAllowed       bool                                      `json:"activation_allowed"`
	ActivationBlockedReason string                                    `json:"activation_blocked_reason,omitempty"`
	ValidationState         string                                    `json:"validation_state,omitempty"`
	Feasible                bool                                      `json:"feasible"`
	CollapsedSingleMember   bool                                      `json:"collapsed_single_member"`
	CollapsedQualityPassed  bool                                      `json:"collapsed_quality_passed"`
	V2                      *ProviderPoolRouterV2Summary              `json:"v2,omitempty"`
}

type ProviderPoolRouterProfilesResponse struct {
	Items  []ProviderPoolRouterProfile `json:"items"`
	Limit  int                         `json:"limit"`
	Offset int                         `json:"offset"`
}

type ProviderPoolRouterActivationRequest struct {
	Actor                    string `json:"actor"`
	Reason                   string `json:"reason"`
	ExpectedCurrentProfileID string `json:"expected_current_profile_id,omitempty"`
}

type ProviderPoolRouterActivation struct {
	ID            int64     `json:"id"`
	FromProfileID string    `json:"from_profile_id,omitempty"`
	ToProfileID   string    `json:"to_profile_id"`
	Action        string    `json:"action"`
	Reason        string    `json:"reason"`
	Actor         string    `json:"actor"`
	CreatedAt     time.Time `json:"created_at"`
}

type ProviderPoolRouterStatus struct {
	QualifiedQueryCount     int                              `json:"qualified_query_count"`
	NewQueryCount           int                              `json:"new_query_count"`
	PendingSuggestion       *ProviderPoolRouterSuggestion    `json:"pending_suggestion,omitempty"`
	RunningJob              *ProviderPoolRouterEvaluationJob `json:"running_job,omitempty"`
	LatestJob               *ProviderPoolRouterEvaluationJob `json:"latest_job,omitempty"`
	ActiveProfile           *ProviderPoolRouterProfile       `json:"active_profile,omitempty"`
	LatestCandidateProfile  *ProviderPoolRouterProfile       `json:"latest_candidate_profile,omitempty"`
	RollbackTargetProfile   *ProviderPoolRouterProfile       `json:"rollback_target_profile,omitempty"`
	CurrentProfileID        string                           `json:"current_profile_id,omitempty"`
	SemanticDifferentiation bool                             `json:"semantic_differentiation"`
}

type ProviderTagModelRequest struct {
	Model       string `json:"model"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type ProviderTagModelSelection struct {
	Model       string `json:"model"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type ProviderTagModelsReplaceRequest struct {
	Models []ProviderTagModelSelection `json:"models"`
}

type ProviderTagModelUpdateRequest struct {
	Model       *string `json:"model,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (s *ProviderTagModelSelection) UnmarshalJSON(data []byte) error {
	var model string
	if err := json.Unmarshal(data, &model); err == nil {
		s.Model = strings.TrimSpace(model)
		s.DisplayName = ""
		s.Description = ""
		return nil
	}
	type alias ProviderTagModelSelection
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	s.Model = strings.TrimSpace(decoded.Model)
	s.DisplayName = strings.TrimSpace(decoded.DisplayName)
	s.Description = strings.TrimSpace(decoded.Description)
	return nil
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
