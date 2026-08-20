package csghub

import "time"

// APIResponse wraps all CSGHub API responses.
type APIResponse[T any] struct {
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

// ListResponse wraps paginated list endpoints.
type ListResponse[T any] struct {
	Msg   string `json:"msg"`
	Data  []T    `json:"data"`
	Total int    `json:"total"`
}

// TokenDetail describes an access token and its owner.
type TokenDetail struct {
	Token       string    `json:"token"`
	TokenName   string    `json:"token_name"`
	Application string    `json:"application"`
	UserName    string    `json:"user_name"`
	UserUUID    string    `json:"user_uuid"`
	ExpireAt    time.Time `json:"expire_at"`
}

// User represents a user returned by the CSGHub API.
type User struct {
	ID        int      `json:"id"`
	Username  string   `json:"username"`
	Nickname  string   `json:"nickname"`
	Email     string   `json:"email"`
	UUID      string   `json:"uuid"`
	Avatar    string   `json:"avatar"`
	Roles     []string `json:"roles"`
	Phone     string   `json:"phone"`
	PhoneArea string   `json:"phone_area"`
}

// Model represents a model returned by the CSGHub API.
type Model struct {
	ID             int                    `json:"id"`
	Name           string                 `json:"name"`
	Nickname       string                 `json:"nickname"`
	Description    string                 `json:"description"`
	Likes          int                    `json:"likes"`
	Downloads      int                    `json:"downloads"`
	Path           string                 `json:"path"`
	RepositoryID   int                    `json:"repository_id"`
	Private        bool                   `json:"private"`
	Tags           []Tag                  `json:"tags"`
	Repository     Repository             `json:"repository"`
	DefaultBranch  string                 `json:"default_branch"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	License        string                 `json:"license"`
	Source         string                 `json:"source"`
	SyncStatus     string                 `json:"sync_status"`
	Metadata       ModelMetadata          `json:"metadata"`
	HFPath         string                 `json:"hf_path"`
	RepoSize       int64                  `json:"repo_size"`
	ArtifactSource string                 `json:"artifact_source,omitempty"`
	Revision       string                 `json:"revision,omitempty"`
	Provider       *ModelProviderMetadata `json:"provider,omitempty"`
}

// ModelProviderMetadata preserves source-native display fields without forcing
// Hugging Face or ModelScope payloads into the OpenCSG model shape.
type ModelProviderMetadata struct {
	HuggingFace *HuggingFaceModelMetadata `json:"huggingface,omitempty"`
	ModelScope  *ModelScopeModelMetadata  `json:"modelscope,omitempty"`
}

type HuggingFaceModelMetadata struct {
	Author       string   `json:"author,omitempty"`
	PipelineTag  string   `json:"pipeline_tag,omitempty"`
	LibraryName  string   `json:"library_name,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	BaseModels   []string `json:"base_models,omitempty"`
	OriginalTags []string `json:"original_tags,omitempty"`
	Gated        bool     `json:"gated,omitempty"`
	SHA          string   `json:"sha,omitempty"`
}

type ModelScopeModelMetadata struct {
	DisplayName  string   `json:"display_name,omitempty"`
	Tasks        []string `json:"tasks,omitempty"`
	Libraries    []string `json:"libraries,omitempty"`
	ModelType    string   `json:"model_type,omitempty"`
	OriginalTags []string `json:"original_tags,omitempty"`
	Gated        bool     `json:"gated,omitempty"`
}

type Tag struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Group    string `json:"group"`
	BuiltIn  bool   `json:"built_in"`
	ShowName string `json:"show_name"`
}

type Repository struct {
	HTTPCloneURL string `json:"http_clone_url"`
	SSHCloneURL  string `json:"ssh_clone_url"`
}

type RepoExtraItem struct {
	RepoID         int   `json:"repo_id"`
	Size           int64 `json:"size"`
	LastCommitSize int64 `json:"last_commit_size"`
}

type ModelMetadata struct {
	ModelParams       float64 `json:"model_params"`
	TensorType        string  `json:"tensor_type"`
	Architecture      string  `json:"architecture"`
	MiniGPUMemoryGB   float64 `json:"mini_gpu_memory_gb"`
	MiniGPUFinetuneGB float64 `json:"mini_gpu_finetune_gb"`
	ModelType         string  `json:"model_type"`
	ClassName         string  `json:"class_name"`
}

// RepoFile represents a file in a model repository.
type RepoFile struct {
	Name            string     `json:"name"`
	Type            string     `json:"type"` // "file" or "dir"
	Size            int64      `json:"size"`
	Path            string     `json:"path"`
	Mode            string     `json:"mode"`
	SHA             string     `json:"sha"`
	LFS             bool       `json:"lfs"`
	LFSSHA256       string     `json:"lfs_sha256"`
	LFSPointerSize  int64      `json:"lfs_pointer_size"`
	LFSRelativePath string     `json:"lfs_relative_path"`
	LastCommitSHA   string     `json:"last_commit_sha"`
	Commit          FileCommit `json:"commit"`
}

type FileCommit struct {
	ID             string    `json:"id"`
	CommitterName  string    `json:"committer_name"`
	CommitterEmail string    `json:"committer_email"`
	CommitterDate  time.Time `json:"committer_date"`
	Message        string    `json:"message"`
}

// ModelListParams holds parameters for listing models.
type ModelListParams struct {
	Search         string
	Sort           string
	Page           int
	PerPage        int
	Source         string
	Framework      string
	TagCategory    string
	TagName        string
	ModelParamsMin string
	ModelParamsMax string
}

// Dataset represents a dataset returned by the CSGHub API.
type Dataset struct {
	ID             int                      `json:"id"`
	Name           string                   `json:"name"`
	Nickname       string                   `json:"nickname"`
	Description    string                   `json:"description"`
	Likes          int                      `json:"likes"`
	Downloads      int                      `json:"downloads"`
	Path           string                   `json:"path"`
	RepositoryID   int                      `json:"repository_id"`
	Private        bool                     `json:"private"`
	Tags           []Tag                    `json:"tags"`
	Repository     Repository               `json:"repository"`
	DefaultBranch  string                   `json:"default_branch"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
	License        string                   `json:"license"`
	Source         string                   `json:"source"`
	SyncStatus     string                   `json:"sync_status"`
	HFPath         string                   `json:"hf_path"`
	ArtifactSource string                   `json:"artifact_source,omitempty"`
	Revision       string                   `json:"revision,omitempty"`
	RepoSize       int64                    `json:"repo_size,omitempty"`
	FileCount      int                      `json:"file_count,omitempty"`
	Provider       *DatasetProviderMetadata `json:"provider,omitempty"`
}

type DatasetProviderMetadata struct {
	HuggingFace *HuggingFaceDatasetMetadata `json:"huggingface,omitempty"`
	ModelScope  *ModelScopeDatasetMetadata  `json:"modelscope,omitempty"`
}

type HuggingFaceDatasetMetadata struct {
	Author         string   `json:"author,omitempty"`
	Languages      []string `json:"languages,omitempty"`
	TaskCategories []string `json:"task_categories,omitempty"`
	PrettyName     string   `json:"pretty_name,omitempty"`
	OriginalTags   []string `json:"original_tags,omitempty"`
	Gated          bool     `json:"gated,omitempty"`
	SHA            string   `json:"sha,omitempty"`
}

type ModelScopeDatasetMetadata struct {
	DisplayName  string   `json:"display_name,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	Tasks        []string `json:"tasks,omitempty"`
	OriginalTags []string `json:"original_tags,omitempty"`
	Gated        bool     `json:"gated,omitempty"`
}

// DatasetListParams holds parameters for listing datasets.
type DatasetListParams struct {
	Search  string
	Sort    string
	Page    int
	PerPage int
	Source  string
}

// RepoSibling represents a file entry from the /csg/api/ endpoint.
type RepoSibling struct {
	RFilename string `json:"rfilename"`
}

// RepoInfoResponse is the response from the /csg/api/ repo info endpoint.
type RepoInfoResponse struct {
	SHA      string        `json:"sha"`
	ID       string        `json:"id"`
	Private  bool          `json:"private"`
	Siblings []RepoSibling `json:"siblings"`
}
