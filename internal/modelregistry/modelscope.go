package modelregistry

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
)

type modelScopeRegistry struct {
	http registryHTTPClient
}

func NewModelScope(baseURL, token string) Registry {
	return &modelScopeRegistry{http: newRegistryHTTPClient(baseURL, token)}
}

func (r *modelScopeRegistry) Source() Source          { return SourceModelScope }
func (r *modelScopeRegistry) DefaultRevision() string { return "master" }

type modelScopeResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type modelScopeListData struct {
	Models     []modelScopeModel `json:"models"`
	TotalCount int               `json:"total_count"`
}

type modelScopeModel struct {
	ID           string    `json:"id"`
	DisplayName  string    `json:"display_name"`
	Description  string    `json:"description"`
	Downloads    int       `json:"downloads"`
	Likes        int       `json:"likes"`
	License      string    `json:"license"`
	Tasks        []string  `json:"tasks"`
	CreatedAt    time.Time `json:"created_at"`
	LastModified time.Time `json:"last_modified"`
	FileSize     int64     `json:"file_size"`
	Params       float64   `json:"params"`
	Tags         []string  `json:"tags"`
	Private      bool      `json:"private"`
	Gated        bool      `json:"gated"`
	Readme       string    `json:"readme"`
}

type modelScopeLegacyResponse[T any] struct {
	Success bool   `json:"Success"`
	Message string `json:"Message"`
	Data    T      `json:"Data"`
}

type modelScopeTreeData struct {
	Files []modelScopeFile `json:"Files"`
}

type modelScopeFile struct {
	Name   string `json:"Name"`
	Path   string `json:"Path"`
	Type   string `json:"Type"`
	Size   int64  `json:"Size"`
	Mode   string `json:"Mode"`
	SHA256 string `json:"Sha256"`
	IsLFS  bool   `json:"IsLFS"`
}

func (r *modelScopeRegistry) ListModels(ctx context.Context, options ListOptions) ([]csghub.Model, int, error) {
	if options.Page <= 0 {
		options.Page = 1
	}
	if options.PerPage <= 0 {
		options.PerPage = 16
	}
	if options.PerPage > 50 {
		options.PerPage = 50
	}
	query := url.Values{
		"page_number": {strconv.Itoa(options.Page)},
		"page_size":   {strconv.Itoa(options.PerPage)},
	}
	if options.Search != "" {
		query.Set("search", options.Search)
	}
	switch options.Sort {
	case "recently_update":
		query.Set("sort", "last_modified")
	case "most_download":
		query.Set("sort", "downloads")
	case "most_favorite":
		query.Set("sort", "likes")
	default:
		query.Set("sort", "default")
	}
	if options.Framework != "" {
		query.Set("filter.library", options.Framework)
	}
	if options.Task != "" {
		query.Set("filter.task", options.Task)
	}
	var payload modelScopeResponse[modelScopeListData]
	if _, err := r.http.getJSON(ctx, r.http.baseURL+"/openapi/v1/models?"+query.Encode(), &payload); err != nil {
		return nil, 0, fmt.Errorf("listing ModelScope models: %w", err)
	}
	if !payload.Success {
		return nil, 0, fmt.Errorf("listing ModelScope models: %s", payload.Message)
	}
	models := make([]csghub.Model, 0, len(payload.Data.Models))
	for _, item := range payload.Data.Models {
		model := normalizeModelScopeModel(item)
		model.Repository.HTTPCloneURL = r.http.baseURL + "/models/" + item.ID
		models = append(models, model)
	}
	return models, payload.Data.TotalCount, nil
}

func (r *modelScopeRegistry) GetModel(ctx context.Context, repoID, revision string) (*csghub.Model, error) {
	if _, _, err := ParseRepoID(repoID); err != nil {
		return nil, err
	}
	var payload modelScopeResponse[modelScopeModel]
	if _, err := r.http.getJSON(ctx, r.http.baseURL+"/openapi/v1/models/"+escapeRepoID(repoID), &payload); err != nil {
		return nil, fmt.Errorf("getting ModelScope model %s: %w", repoID, err)
	}
	if !payload.Success {
		return nil, fmt.Errorf("getting ModelScope model %s: %s", repoID, payload.Message)
	}
	model := normalizeModelScopeModel(payload.Data)
	model.Repository.HTTPCloneURL = r.http.baseURL + "/models/" + repoID
	model.Revision = ResolveRevision(revision, r.DefaultRevision())
	return &model, nil
}

func (r *modelScopeRegistry) ListFiles(ctx context.Context, repoID, revision string) ([]csghub.RepoFile, string, error) {
	if _, _, err := ParseRepoID(repoID); err != nil {
		return nil, "", err
	}
	revision = ResolveRevision(revision, r.DefaultRevision())
	query := url.Values{"Revision": {revision}, "Recursive": {"true"}}
	var payload modelScopeLegacyResponse[modelScopeTreeData]
	endpoint := r.http.baseURL + "/api/v1/models/" + escapeRepoID(repoID) + "/repo/files?" + query.Encode()
	if _, err := r.http.getJSON(ctx, endpoint, &payload); err != nil {
		return nil, "", fmt.Errorf("listing ModelScope files for %s: %w", repoID, err)
	}
	if !payload.Success {
		return nil, "", fmt.Errorf("listing ModelScope files for %s: %s", repoID, payload.Message)
	}
	files := make([]csghub.RepoFile, 0, len(payload.Data.Files))
	for _, item := range payload.Data.Files {
		fileType := "file"
		if item.Type == "tree" {
			fileType = "dir"
		}
		files = append(files, csghub.RepoFile{
			Name:      item.Name,
			Type:      fileType,
			Size:      item.Size,
			Path:      item.Path,
			Mode:      item.Mode,
			SHA:       item.SHA256,
			LFS:       item.IsLFS,
			LFSSHA256: item.SHA256,
		})
	}
	return files, revision, nil
}

func (r *modelScopeRegistry) ReadFile(ctx context.Context, repoID, revision, filePath string) (string, error) {
	if _, _, err := ParseRepoID(repoID); err != nil {
		return "", err
	}
	return r.http.getText(ctx, r.downloadURL(repoID, ResolveRevision(revision, r.DefaultRevision()), filePath))
}

func (r *modelScopeRegistry) DownloadSnapshot(ctx context.Context, repoID, revision, destDir string, quants []string, progress csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error) {
	revision = ResolveRevision(revision, r.DefaultRevision())
	files, resolved, err := r.ListFiles(ctx, repoID, revision)
	if err != nil {
		return nil, "", err
	}
	files, err = downloadRegistrySnapshot(ctx, r.http, files, destDir, quants, func(file csghub.RepoFile) string {
		return r.downloadURL(repoID, revision, file.Path)
	}, progress)
	return files, resolved, err
}

func (r *modelScopeRegistry) downloadURL(repoID, revision, filePath string) string {
	query := url.Values{"Revision": {revision}, "FilePath": {filePath}}
	return r.http.baseURL + "/api/v1/models/" + escapeRepoID(repoID) + "/repo?" + query.Encode()
}

func normalizeModelScopeModel(item modelScopeModel) csghub.Model {
	_, name, _ := ParseRepoID(item.ID)
	tags := make([]csghub.Tag, 0, len(item.Tags)+len(item.Tasks))
	seenTags := make(map[string]struct{}, len(item.Tags)+len(item.Tasks))
	modelType := ""
	libraries := make([]string, 0, 2)
	seenLibraries := make(map[string]struct{})
	for _, value := range item.Tags {
		category := "tag"
		tagName := value
		if prefix, remainder, ok := strings.Cut(value, ":"); ok {
			tagName = remainder
			switch prefix {
			case "license":
				category = "license"
			case "library":
				category = "runtime_framework"
				if tagName == "gguf" || tagName == "safetensors" {
					category = "framework"
				}
				if _, exists := seenLibraries[tagName]; !exists {
					libraries = append(libraries, tagName)
					seenLibraries[tagName] = struct{}{}
				}
			case "task":
				category = "task"
			case "model_type":
				modelType = tagName
			}
		}
		tags = appendRegistryTag(tags, seenTags, tagName, category)
	}
	for _, task := range item.Tasks {
		tags = appendRegistryTag(tags, seenTags, task, "task")
	}
	return csghub.Model{
		Name:           name,
		Nickname:       item.DisplayName,
		Description:    firstNonEmpty(item.Description, summaryFromReadme(item.Readme)),
		Likes:          item.Likes,
		Downloads:      item.Downloads,
		Path:           item.ID,
		Private:        item.Private,
		Tags:           tags,
		DefaultBranch:  "master",
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.LastModified,
		License:        firstNonEmpty(item.License, prefixedTagValue(item.Tags, "license:")),
		Source:         string(SourceModelScope),
		ArtifactSource: string(SourceModelScope),
		Revision:       "master",
		RepoSize:       item.FileSize,
		Provider: &csghub.ModelProviderMetadata{
			ModelScope: &csghub.ModelScopeModelMetadata{
				DisplayName:  item.DisplayName,
				Tasks:        append([]string(nil), item.Tasks...),
				Libraries:    libraries,
				ModelType:    modelType,
				OriginalTags: append([]string(nil), item.Tags...),
				Gated:        item.Gated,
			},
		},
		Metadata: csghub.ModelMetadata{
			ModelParams: modelScopeParameterBillions(item.Params),
			ModelType:   modelType,
		},
	}
}

func modelScopeParameterBillions(value float64) float64 {
	if value <= 0 {
		return 0
	}
	// Current OpenAPI responses use raw parameter counts, while older/mirrored
	// endpoints may return the already-normalized value in billions.
	if value < 10_000 {
		return value
	}
	return value / 1_000_000_000
}

func parameterBillions(value int64) float64 {
	if value <= 0 {
		return 0
	}
	return float64(value) / 1_000_000_000
}
