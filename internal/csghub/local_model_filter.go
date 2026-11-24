package csghub

import (
	"context"
	"fmt"
)

// LocalModelFilterRequest is the body of POST /api/v1/models/local_filter. The
// endpoint classifies the model catalog against the hardware and capabilities
// this client declares; the server owns no knowledge of local runtimes.
type LocalModelFilterRequest struct {
	ClusterSkipHardware bool                         `json:"cluster_skip_hardware"`
	Hardware            LocalModelFilterHardware     `json:"hardware"`
	Capabilities        LocalModelFilterCapabilities `json:"capabilities"`
	Filters             LocalModelFilterOptions      `json:"filters"`
	ExcludePaths        []string                     `json:"exclude_paths"`
	Boost               LocalModelFilterBoost        `json:"boost"`
	Page                int                          `json:"page"`
	Per                 int                          `json:"per"`
}

type LocalModelFilterHardware struct {
	OS            string                `json:"os"`
	Arch          string                `json:"arch"`
	FreeDiskBytes int64                 `json:"free_disk_bytes"`
	RAMBytes      int64                 `json:"ram_bytes"`
	GPUs          []LocalModelFilterGPU `json:"gpus"`
}

// LocalModelFilterGPU reports one physical GPU. Shared-memory GPUs (Apple Silicon,
// integrated graphics) report vram_bytes=0 with shared_memory=true so the
// server does not count the system memory twice.
type LocalModelFilterGPU struct {
	Vendor       string `json:"vendor"`
	VRAMBytes    int64  `json:"vram_bytes"`
	SharedMemory bool   `json:"shared_memory"`
}

// LocalModelFilterCapabilities mirrors the client's local inference rules. Every
// vocabulary below is owned by the client; the server only matches it.
type LocalModelFilterCapabilities struct {
	Formats                         []string                `json:"formats"`
	NativeFormats                   []string                `json:"native_formats"`
	Tasks                           []string                `json:"tasks"`
	Convert                         LocalModelFilterConvert `json:"convert"`
	TaskAliases                     map[string]string       `json:"task_aliases"`
	ImagePipelineSubstrings         []string                `json:"image_pipeline_substrings"`
	ImageClassFallbackSubstrings    []string                `json:"image_class_fallback_substrings"`
	ImageUnsupportedClassSubstrings []string                `json:"image_unsupported_class_substrings"`
	ASRNameFamilies                 []string                `json:"asr_name_families"`
	ASRArchSubstrings               []string                `json:"asr_arch_substrings"`
	TTSNameFamilies                 []string                `json:"tts_name_families"`
	TTSNameTokens                   []string                `json:"tts_name_tokens"`
	TTSTaskArchSubstrings           []string                `json:"tts_task_arch_substrings"`
	TTSBackendArchSubstrings        []string                `json:"tts_backend_arch_substrings"`
	TTSBackendNameSubstrings        []string                `json:"tts_backend_name_substrings"`
	DiffusionGGUFArchitectures      []string                `json:"diffusion_gguf_architectures"`
	AllowEmptyImageClass            *bool                   `json:"allow_empty_image_class,omitempty"`
	UnsupportedFormatTags           []string                `json:"unsupported_format_tags"`
	UnsupportedRuntimeTags          []string                `json:"unsupported_runtime_tags"`
	QuantSubstrings                 []string                `json:"quant_substrings"`
	QuantTokens                     []string                `json:"quant_tokens"`
	GGUFNonTextKeywords             []string                `json:"gguf_non_text_keywords"`
}

type LocalModelFilterConvert struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	Architectures  []string `json:"architectures"`
	DiskMultiplier int      `json:"disk_multiplier"`
}

type LocalModelFilterOptions struct {
	Search         string      `json:"search"`
	Sort           string      `json:"sort"`
	Tags           []TagFilter `json:"tags"`
	ModelParamsMin *float64    `json:"model_params_min,omitempty"`
	ModelParamsMax *float64    `json:"model_params_max,omitempty"`
}

type LocalModelFilterBoost struct {
	Orgs []string `json:"orgs"`
}

// LocalModelFilterResponse is the response of POST /api/v1/models/local_filter.
type LocalModelFilterResponse struct {
	Data                []Model                     `json:"data"`
	TotalConfirmed      int                         `json:"total_confirmed"`
	TotalExact          bool                        `json:"total_exact"`
	HasMore             bool                        `json:"has_more"`
	LocalCheck          []LocalModelFilterCandidate `json:"local_check"`
	LocalCheckTruncated bool                        `json:"local_check_truncated"`
	LocalCheckTimeout   bool                        `json:"local_check_timeout"`
	LocalCheckExhausted bool                        `json:"local_check_exhausted"`
	CandidatesQueried   bool                        `json:"candidates_queried"`
	PageLimitExceeded   bool                        `json:"page_limit_exceeded"`
	QueryTimeout        bool                        `json:"query_timeout"`
	Applied             LocalModelFilterApplied     `json:"applied"`
}

type LocalModelFilterApplied struct {
	DiskFreeBytes      int64   `json:"disk_free_bytes"`
	MaxDownloadBytes   int64   `json:"max_download_bytes"`
	DiskMultiplier     int     `json:"disk_multiplier"`
	MaxMemoryBytes     int64   `json:"max_memory_bytes"`
	ParamsCap          float64 `json:"params_cap"`
	ParamsMaxEffective float64 `json:"params_max_effective"`
	HardwareApplied    bool    `json:"hardware_applied"`
}

// LocalModelFilterCandidate is a model the server could not classify and the
// reason the metadata is incomplete.
type LocalModelFilterCandidate struct {
	Model  Model  `json:"model"`
	Reason string `json:"reason"`
}

// Candidate reason values. An unknown reason is handled like
// missing_architecture: judge locally and drop the model when that is not
// possible.
const (
	LocalModelFilterReasonMissingArchitecture = "missing_architecture"
	LocalModelFilterReasonMissingParams       = "missing_params"
	LocalModelFilterReasonMissingSize         = "missing_size"
	LocalModelFilterReasonMissingMemory       = "missing_memory"
	LocalModelFilterReasonDualFormat          = "dual_format"
	LocalModelFilterReasonGGUFMetadataMissing = "gguf_metadata_missing"
)

// ListLocalModelFilter asks the hub which models this machine can run.
func (c *Client) ListLocalModelFilter(ctx context.Context, req LocalModelFilterRequest) (*LocalModelFilterResponse, error) {
	var resp APIResponse[LocalModelFilterResponse]
	if err := c.postJSON(ctx, "/api/v1/models/local_filter", req, &resp); err != nil {
		return nil, fmt.Errorf("listing local model filter models: %w", err)
	}
	result := resp.Data
	markOpenCSGModel := func(model *Model) {
		model.ArtifactSource = "opencsg"
		if model.Revision == "" {
			model.Revision = model.DefaultBranch
		}
	}
	for i := range result.Data {
		markOpenCSGModel(&result.Data[i])
	}
	for i := range result.LocalCheck {
		markOpenCSGModel(&result.LocalCheck[i].Model)
	}
	return &result, nil
}
