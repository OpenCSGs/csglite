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

type huggingFaceRegistry struct {
	http artifactregistry.Client
}

func NewHuggingFace(baseURL, token string) Registry {
	return &huggingFaceRegistry{http: artifactregistry.NewClient(baseURL, token, artifactregistry.DefaultUserAgent)}
}

func (r *huggingFaceRegistry) Source() Source          { return SourceHuggingFace }
func (r *huggingFaceRegistry) DefaultRevision() string { return "main" }

type huggingFaceDataset struct {
	ID           string         `json:"id"`
	Author       string         `json:"author"`
	Private      bool           `json:"private"`
	Gated        any            `json:"gated"`
	Likes        int            `json:"likes"`
	Downloads    int            `json:"downloads"`
	Tags         []string       `json:"tags"`
	CreatedAt    time.Time      `json:"createdAt"`
	LastModified time.Time      `json:"lastModified"`
	SHA          string         `json:"sha"`
	CardData     map[string]any `json:"cardData"`
}

type huggingFaceTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	OID  string `json:"oid"`
	LFS  *struct {
		OID         string `json:"oid"`
		Size        int64  `json:"size"`
		PointerSize int64  `json:"pointerSize"`
	} `json:"lfs"`
}

func (r *huggingFaceRegistry) ListDatasets(ctx context.Context, options ListOptions) ([]csghub.Dataset, int, error) {
	if options.Page <= 0 {
		options.Page = 1
	}
	if options.PerPage <= 0 {
		options.PerPage = 16
	}
	query := url.Values{
		"limit": {strconv.Itoa(options.PerPage)},
		"skip":  {strconv.Itoa((options.Page - 1) * options.PerPage)},
	}
	if options.Search != "" {
		query.Set("search", options.Search)
	}
	for _, field := range []string{"author", "cardData", "createdAt", "downloads", "gated", "lastModified", "likes", "private", "sha", "tags"} {
		query.Add("expand", field)
	}
	switch options.Sort {
	case "recently_update":
		query.Set("sort", "lastModified")
	case "most_download":
		query.Set("sort", "downloads")
	case "most_favorite":
		query.Set("sort", "likes")
	default:
		query.Set("sort", "trendingScore")
	}
	query.Set("direction", "-1")
	for _, filter := range []struct {
		prefix string
		value  string
	}{
		{prefix: "task_categories:", value: options.Task},
		{prefix: "language:", value: options.Language},
		{prefix: "license:", value: options.License},
	} {
		if value := huggingFaceDatasetFilter(filter.prefix, filter.value); value != "" {
			query.Add("filter", value)
		}
	}
	var payload []huggingFaceDataset
	headers, err := r.http.GetJSON(ctx, r.http.BaseURL()+"/api/datasets?"+query.Encode(), &payload)
	if err != nil {
		return nil, 0, fmt.Errorf("listing Hugging Face datasets: %w", err)
	}
	datasets := make([]csghub.Dataset, 0, len(payload))
	for _, item := range payload {
		dataset := normalizeHuggingFaceDataset(item)
		dataset.Repository.HTTPCloneURL = r.http.BaseURL() + "/datasets/" + dataset.Path
		datasets = append(datasets, dataset)
	}
	return datasets, artifactregistry.ApproximateTotal(headers, options.Page, options.PerPage, len(datasets)), nil
}

func huggingFaceDatasetFilter(prefix, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, ":") {
		return value
	}
	return prefix + value
}

func (r *huggingFaceRegistry) GetDataset(ctx context.Context, repoID, revision string) (*csghub.Dataset, error) {
	if _, _, err := artifactregistry.ParseRepoID(repoID); err != nil {
		return nil, err
	}
	revision = artifactregistry.ResolveRevision(revision, r.DefaultRevision())
	endpoint := r.http.BaseURL() + "/api/datasets/" + artifactregistry.EscapeRepoID(repoID) + "/revision/" + url.PathEscape(revision)
	var payload huggingFaceDataset
	if _, err := r.http.GetJSON(ctx, endpoint, &payload); err != nil {
		return nil, fmt.Errorf("getting Hugging Face dataset %s: %w", repoID, err)
	}
	dataset := normalizeHuggingFaceDataset(payload)
	dataset.Repository.HTTPCloneURL = r.http.BaseURL() + "/datasets/" + repoID
	dataset.Revision = firstNonEmpty(payload.SHA, revision)
	return &dataset, nil
}

func (r *huggingFaceRegistry) ListFiles(ctx context.Context, repoID, revision string) ([]csghub.RepoFile, string, error) {
	if _, _, err := artifactregistry.ParseRepoID(repoID); err != nil {
		return nil, "", err
	}
	revision = artifactregistry.ResolveRevision(revision, r.DefaultRevision())
	query := url.Values{"recursive": {"true"}, "expand": {"false"}}
	nextURL := r.http.BaseURL() + "/api/datasets/" + artifactregistry.EscapeRepoID(repoID) + "/tree/" + url.PathEscape(revision) + "?" + query.Encode()
	var files []csghub.RepoFile
	var resolvedRevision string
	for nextURL != "" {
		var payload []huggingFaceTreeEntry
		headers, err := r.http.GetJSON(ctx, nextURL, &payload)
		if err != nil {
			return nil, "", fmt.Errorf("listing Hugging Face files for %s: %w", repoID, err)
		}
		if resolvedRevision == "" {
			resolvedRevision = headers.Get("X-Repo-Commit")
		}
		for _, item := range payload {
			fileType := "file"
			if item.Type == "directory" {
				fileType = "dir"
			}
			file := csghub.RepoFile{Name: path.Base(item.Path), Type: fileType, Path: item.Path, Size: item.Size, SHA: item.OID}
			if item.LFS != nil {
				file.LFS = true
				file.Size = item.LFS.Size
				file.LFSSHA256 = strings.TrimPrefix(item.LFS.OID, "sha256:")
				file.LFSPointerSize = item.LFS.PointerSize
			}
			files = append(files, file)
		}
		nextURL = artifactregistry.NextLink(headers.Get("Link"))
	}
	if resolvedRevision == "" {
		resolvedRevision = revision
	}
	return files, resolvedRevision, nil
}

func (r *huggingFaceRegistry) DownloadSnapshot(ctx context.Context, repoID, revision, destDir string, progress csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error) {
	revision = artifactregistry.ResolveRevision(revision, r.DefaultRevision())
	files, resolved, err := r.ListFiles(ctx, repoID, revision)
	if err != nil {
		return nil, "", err
	}
	files, err = artifactregistry.DownloadSnapshot(ctx, r.http, files, destDir, func(file csghub.RepoFile) string {
		return r.http.BaseURL() + "/datasets/" + artifactregistry.EscapeRepoID(repoID) + "/resolve/" + url.PathEscape(revision) + "/" + artifactregistry.EscapePath(file.Path)
	}, progress)
	return files, resolved, err
}

func normalizeHuggingFaceDataset(item huggingFaceDataset) csghub.Dataset {
	_, name, _ := artifactregistry.ParseRepoID(item.ID)
	tags := make([]csghub.Tag, 0, len(item.Tags))
	seen := make(map[string]struct{}, len(item.Tags))
	for _, value := range item.Tags {
		category, tagName := "tag", value
		lower := strings.ToLower(value)
		switch {
		case strings.HasPrefix(lower, "license:"):
			category, tagName = "license", strings.TrimSpace(value[len("license:"):])
		case strings.HasPrefix(lower, "language:"):
			category, tagName = "language", strings.TrimSpace(value[len("language:"):])
		case strings.HasPrefix(lower, "task_categories:"):
			category, tagName = "task", strings.TrimSpace(value[len("task_categories:"):])
		case strings.HasPrefix(lower, "size_categories:"):
			category, tagName = "size", strings.TrimSpace(value[len("size_categories:"):])
		}
		tags = appendTag(tags, seen, tagName, category)
	}
	languages := append(stringSlice(item.CardData["language"]), stringSlice(item.CardData["languages"])...)
	tasks := stringSlice(item.CardData["task_categories"])
	for _, language := range languages {
		tags = appendTag(tags, seen, language, "language")
	}
	for _, task := range tasks {
		tags = appendTag(tags, seen, task, "task")
	}
	return csghub.Dataset{
		Name: name, Nickname: stringValue(item.CardData["pretty_name"]),
		Description: firstNonEmpty(stringValue(item.CardData["description"]), stringValue(item.CardData["summary"])),
		Likes:       item.Likes, Downloads: item.Downloads, Path: item.ID, Private: item.Private,
		Tags: tags, DefaultBranch: "main", CreatedAt: item.CreatedAt, UpdatedAt: item.LastModified,
		License: firstNonEmpty(stringValue(item.CardData["license"]), prefixedValue(item.Tags, "license:")),
		Source:  string(SourceHuggingFace), HFPath: item.ID, ArtifactSource: string(SourceHuggingFace),
		Revision: firstNonEmpty(item.SHA, "main"),
		Provider: &csghub.DatasetProviderMetadata{HuggingFace: &csghub.HuggingFaceDatasetMetadata{
			Author: item.Author, Languages: languages, TaskCategories: tasks,
			PrettyName: stringValue(item.CardData["pretty_name"]), OriginalTags: append([]string(nil), item.Tags...),
			Gated: gated(item.Gated), SHA: item.SHA,
		}},
	}
}
