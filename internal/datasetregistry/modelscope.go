package datasetregistry

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	artifactregistry "github.com/opencsgs/csglite/internal/registry"
)

type modelScopeRegistry struct {
	http artifactregistry.Client
}

func NewModelScope(baseURL, token string) Registry {
	return &modelScopeRegistry{http: artifactregistry.NewClient(baseURL, token, artifactregistry.DefaultUserAgent)}
}

func (r *modelScopeRegistry) Source() Source          { return SourceModelScope }
func (r *modelScopeRegistry) DefaultRevision() string { return "master" }

type modelScopeResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type modelScopeDatasetList struct {
	Datasets   []modelScopeDataset `json:"datasets"`
	TotalCount int                 `json:"total_count"`
}

type modelScopeDataset struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	DisplayName  string    `json:"display_name"`
	Description  string    `json:"description"`
	Downloads    int       `json:"downloads"`
	Likes        int       `json:"likes"`
	License      string    `json:"license"`
	Languages    []string  `json:"languages"`
	Tasks        []string  `json:"tasks"`
	Tags         []string  `json:"tags"`
	Private      bool      `json:"private"`
	Gated        bool      `json:"gated"`
	FileSize     int64     `json:"file_size"`
	CreatedAt    time.Time `json:"created_at"`
	LastModified time.Time `json:"last_modified"`
}

type modelScopeLegacyResponse[T any] struct {
	Success bool   `json:"Success"`
	Code    int    `json:"Code"`
	Message string `json:"Message"`
	Data    T      `json:"Data"`
}

func (r modelScopeLegacyResponse[T]) OK() bool {
	return r.Success || r.Code == 200 || (r.Code == 0 && strings.EqualFold(strings.TrimSpace(r.Message), "success"))
}

type modelScopeTreeData struct {
	Files []modelScopeFile `json:"Files"`
}

type modelScopeFile struct {
	Name     string `json:"Name"`
	Path     string `json:"Path"`
	Type     string `json:"Type"`
	Size     int64  `json:"Size"`
	Mode     string `json:"Mode"`
	SHA256   string `json:"Sha256"`
	Revision string `json:"Revision"`
	IsLFS    bool   `json:"IsLFS"`
}

func (r *modelScopeRegistry) ListDatasets(ctx context.Context, options ListOptions) ([]csghub.Dataset, int, error) {
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
	if options.Task != "" {
		query.Set("filter.task", options.Task)
	}
	if options.Language != "" {
		query.Set("filter.language", options.Language)
	}
	if options.License != "" {
		query.Set("filter.license", options.License)
	}
	var payload modelScopeResponse[modelScopeDatasetList]
	if _, err := r.http.GetJSON(ctx, r.http.BaseURL()+"/openapi/v1/datasets?"+query.Encode(), &payload); err != nil {
		return nil, 0, fmt.Errorf("listing ModelScope datasets: %w", err)
	}
	if !payload.Success {
		return nil, 0, fmt.Errorf("listing ModelScope datasets: %s", payload.Message)
	}
	datasets := make([]csghub.Dataset, 0, len(payload.Data.Datasets))
	for _, item := range payload.Data.Datasets {
		dataset := normalizeModelScopeDataset(item)
		dataset.Repository.HTTPCloneURL = r.http.BaseURL() + "/datasets/" + dataset.Path
		datasets = append(datasets, dataset)
	}
	return datasets, payload.Data.TotalCount, nil
}

func (r *modelScopeRegistry) GetDataset(ctx context.Context, repoID, revision string) (*csghub.Dataset, error) {
	if _, _, err := artifactregistry.ParseRepoID(repoID); err != nil {
		return nil, err
	}
	var payload modelScopeResponse[modelScopeDataset]
	if _, err := r.http.GetJSON(ctx, r.http.BaseURL()+"/openapi/v1/datasets/"+artifactregistry.EscapeRepoID(repoID), &payload); err != nil {
		return nil, fmt.Errorf("getting ModelScope dataset %s: %w", repoID, err)
	}
	if !payload.Success {
		return nil, fmt.Errorf("getting ModelScope dataset %s: %s", repoID, payload.Message)
	}
	dataset := normalizeModelScopeDataset(payload.Data)
	dataset.Repository.HTTPCloneURL = r.http.BaseURL() + "/datasets/" + repoID
	dataset.Revision = artifactregistry.ResolveRevision(revision, r.DefaultRevision())
	return &dataset, nil
}

func (r *modelScopeRegistry) ListFiles(ctx context.Context, repoID, revision string) ([]csghub.RepoFile, string, error) {
	if _, _, err := artifactregistry.ParseRepoID(repoID); err != nil {
		return nil, "", err
	}
	revision = artifactregistry.ResolveRevision(revision, r.DefaultRevision())
	query := url.Values{"Revision": {revision}, "Recursive": {"true"}}
	endpoint := r.http.BaseURL() + "/api/v1/datasets/" + artifactregistry.EscapeRepoID(repoID) + "/repo/tree?" + query.Encode()
	var payload modelScopeLegacyResponse[modelScopeTreeData]
	if _, err := r.http.GetJSON(ctx, endpoint, &payload); err != nil {
		return nil, "", fmt.Errorf("listing ModelScope files for %s: %w", repoID, err)
	}
	if !payload.OK() {
		return nil, "", fmt.Errorf("listing ModelScope files for %s: %s", repoID, payload.Message)
	}
	files := make([]csghub.RepoFile, 0, len(payload.Data.Files))
	resolved := revision
	for _, item := range payload.Data.Files {
		fileType := "file"
		if item.Type == "tree" || item.Type == "directory" {
			fileType = "dir"
		}
		filePath := item.Path
		if filePath == "" {
			filePath = item.Name
		}
		files = append(files, csghub.RepoFile{
			Name: firstNonEmpty(item.Name, path.Base(filePath)), Type: fileType,
			Size: item.Size, Path: filePath, Mode: item.Mode, SHA: item.SHA256,
			LFS: item.IsLFS, LFSSHA256: item.SHA256,
		})
		if item.Revision != "" {
			resolved = item.Revision
		}
	}
	return files, resolved, nil
}

func (r *modelScopeRegistry) DownloadSnapshot(ctx context.Context, repoID, revision, destDir string, progress csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error) {
	revision = artifactregistry.ResolveRevision(revision, r.DefaultRevision())
	files, resolved, err := r.ListFiles(ctx, repoID, revision)
	if err != nil {
		return nil, "", err
	}
	files, err = artifactregistry.DownloadSnapshot(ctx, r.http, files, destDir, func(file csghub.RepoFile) string {
		query := url.Values{"Revision": {revision}, "FilePath": {file.Path}}
		return r.http.BaseURL() + "/api/v1/datasets/" + artifactregistry.EscapeRepoID(repoID) + "/repo?" + query.Encode()
	}, progress)
	return files, resolved, err
}

func normalizeModelScopeDataset(item modelScopeDataset) csghub.Dataset {
	repoID := firstNonEmpty(item.ID, item.Name)
	_, name, _ := artifactregistry.ParseRepoID(repoID)
	tags := make([]csghub.Tag, 0, len(item.Tags)+len(item.Tasks)+len(item.Languages))
	seen := make(map[string]struct{}, cap(tags))
	for _, value := range item.Tags {
		category, tagName := "tag", value
		if prefix, remainder, ok := strings.Cut(value, ":"); ok {
			tagName = remainder
			switch strings.ToLower(prefix) {
			case "license":
				category = "license"
			case "language":
				category = "language"
			case "task":
				category = "task"
			}
		}
		tags = appendTag(tags, seen, tagName, category)
	}
	for _, task := range item.Tasks {
		tags = appendTag(tags, seen, task, "task")
	}
	for _, language := range item.Languages {
		tags = appendTag(tags, seen, language, "language")
	}
	return csghub.Dataset{
		Name: name, Nickname: item.DisplayName, Description: item.Description,
		Likes: item.Likes, Downloads: item.Downloads, Path: repoID, Private: item.Private,
		Tags: tags, DefaultBranch: "master", CreatedAt: item.CreatedAt, UpdatedAt: item.LastModified,
		License: firstNonEmpty(item.License, prefixedValue(item.Tags, "license:")),
		Source:  string(SourceModelScope), ArtifactSource: string(SourceModelScope),
		Revision: "master", RepoSize: item.FileSize,
		Provider: &csghub.DatasetProviderMetadata{ModelScope: &csghub.ModelScopeDatasetMetadata{
			DisplayName: item.DisplayName, Languages: append([]string(nil), item.Languages...),
			Tasks: append([]string(nil), item.Tasks...), OriginalTags: append([]string(nil), item.Tags...),
			Gated: item.Gated,
		}},
	}
}
