package modelregistry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
)

type huggingFaceRegistry struct {
	http registryHTTPClient
}

func NewHuggingFace(baseURL, token string) Registry {
	return &huggingFaceRegistry{http: newRegistryHTTPClient(baseURL, token)}
}

func (r *huggingFaceRegistry) Source() Source          { return SourceHuggingFace }
func (r *huggingFaceRegistry) DefaultRevision() string { return "main" }

type huggingFaceModel struct {
	ID           string                 `json:"id"`
	ModelID      string                 `json:"modelId"`
	Author       string                 `json:"author"`
	Private      bool                   `json:"private"`
	Gated        any                    `json:"gated"`
	Likes        int                    `json:"likes"`
	Downloads    int                    `json:"downloads"`
	Tags         []string               `json:"tags"`
	PipelineTag  string                 `json:"pipeline_tag"`
	LibraryName  string                 `json:"library_name"`
	CreatedAt    time.Time              `json:"createdAt"`
	LastModified time.Time              `json:"lastModified"`
	SHA          string                 `json:"sha"`
	CardData     map[string]any         `json:"cardData"`
	Config       huggingFaceModelConfig `json:"config"`
	SafeTensors  struct {
		Total      int64            `json:"total"`
		Parameters map[string]int64 `json:"parameters"`
	} `json:"safetensors"`
}

type huggingFaceModelConfig struct {
	Architectures []string `json:"architectures"`
	ModelType     string   `json:"model_type"`
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

func (r *huggingFaceRegistry) ListModels(ctx context.Context, options ListOptions) ([]csghub.Model, int, error) {
	query := url.Values{}
	if options.Search != "" {
		query.Set("search", options.Search)
	}
	if options.PerPage <= 0 {
		options.PerPage = 16
	}
	if options.Page <= 0 {
		options.Page = 1
	}
	query.Set("limit", strconv.Itoa(options.PerPage))
	query.Set("skip", strconv.Itoa((options.Page-1)*options.PerPage))
	for _, field := range []string{
		"author", "cardData", "config", "createdAt", "downloads", "gated",
		"lastModified", "library_name", "likes", "pipeline_tag", "private",
		"safetensors", "sha", "tags",
	} {
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
	if options.Framework != "" {
		query.Add("filter", options.Framework)
	}
	if options.Task != "" {
		query.Set("pipeline_tag", options.Task)
	}

	var payload []huggingFaceModel
	headers, err := r.http.getJSON(ctx, r.http.baseURL+"/api/models?"+query.Encode(), &payload)
	if err != nil {
		return nil, 0, fmt.Errorf("listing Hugging Face models: %w", err)
	}
	models := make([]csghub.Model, 0, len(payload))
	for _, item := range payload {
		model := normalizeHuggingFaceModel(item)
		model.Repository.HTTPCloneURL = r.http.baseURL + "/" + model.Path
		models = append(models, model)
	}
	total := approximateTotal(headers, options.Page, options.PerPage, len(models))
	return models, total, nil
}

func (r *huggingFaceRegistry) GetModel(ctx context.Context, repoID, revision string) (*csghub.Model, error) {
	if _, _, err := ParseRepoID(repoID); err != nil {
		return nil, err
	}
	revision = ResolveRevision(revision, r.DefaultRevision())
	endpoint := r.http.baseURL + "/api/models/" + escapeRepoID(repoID) + "/revision/" + url.PathEscape(revision)
	var payload huggingFaceModel
	if _, err := r.http.getJSON(ctx, endpoint, &payload); err != nil {
		return nil, fmt.Errorf("getting Hugging Face model %s: %w", repoID, err)
	}
	model := normalizeHuggingFaceModel(payload)
	model.Repository.HTTPCloneURL = r.http.baseURL + "/" + repoID
	model.Revision = firstNonEmpty(payload.SHA, revision)
	if model.Description == "" {
		// Model cards are optional, so a missing README must not make model
		// metadata or downloads unavailable.
		if readme, readErr := r.ReadFile(ctx, repoID, revision, "README.md"); readErr == nil {
			model.Description = summaryFromReadme(readme)
		}
	}
	return &model, nil
}

func (r *huggingFaceRegistry) ListFiles(ctx context.Context, repoID, revision string) ([]csghub.RepoFile, string, error) {
	if _, _, err := ParseRepoID(repoID); err != nil {
		return nil, "", err
	}
	revision = ResolveRevision(revision, r.DefaultRevision())
	query := url.Values{"recursive": {"true"}, "expand": {"false"}}
	nextURL := r.http.baseURL + "/api/models/" + escapeRepoID(repoID) + "/tree/" + url.PathEscape(revision) + "?" + query.Encode()
	var files []csghub.RepoFile
	resolvedRevision := ""
	for nextURL != "" {
		var pageItems []huggingFaceTreeEntry
		headers, err := r.http.getJSON(ctx, nextURL, &pageItems)
		if err != nil {
			return nil, "", fmt.Errorf("listing Hugging Face files for %s: %w", repoID, err)
		}
		if resolvedRevision == "" {
			resolvedRevision = headers.Get("X-Repo-Commit")
		}
		for _, item := range pageItems {
			fileType := "file"
			if item.Type == "directory" {
				fileType = "dir"
			}
			file := csghub.RepoFile{
				Name: path.Base(item.Path),
				Type: fileType,
				Path: item.Path,
				Size: item.Size,
				SHA:  item.OID,
			}
			if item.LFS != nil {
				file.LFS = true
				file.Size = item.LFS.Size
				file.LFSSHA256 = item.LFS.OID
				file.LFSPointerSize = item.LFS.PointerSize
			}
			files = append(files, file)
		}
		nextURL = nextLink(headers.Get("Link"))
	}
	if resolvedRevision == "" {
		resolvedRevision = revision
	}
	return files, resolvedRevision, nil
}

func (r *huggingFaceRegistry) ReadFile(ctx context.Context, repoID, revision, filePath string) (string, error) {
	if _, _, err := ParseRepoID(repoID); err != nil {
		return "", err
	}
	revision = ResolveRevision(revision, r.DefaultRevision())
	endpoint := r.resolveURL(repoID, revision, filePath)
	return r.http.getText(ctx, endpoint)
}

func (r *huggingFaceRegistry) DownloadSnapshot(ctx context.Context, repoID, revision, destDir string, quants []string, progress csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error) {
	revision = ResolveRevision(revision, r.DefaultRevision())
	files, resolved, err := r.ListFiles(ctx, repoID, revision)
	if err != nil {
		return nil, "", err
	}
	files, err = downloadRegistrySnapshot(ctx, r.http, files, destDir, quants, func(file csghub.RepoFile) string {
		return r.resolveURL(repoID, revision, file.Path)
	}, progress)
	return files, resolved, err
}

func (r *huggingFaceRegistry) resolveURL(repoID, revision, filePath string) string {
	return r.http.baseURL + "/" + escapeRepoID(repoID) + "/resolve/" + url.PathEscape(revision) + "/" + escapePath(filePath)
}

func normalizeHuggingFaceModel(item huggingFaceModel) csghub.Model {
	repoID := firstNonEmpty(item.ModelID, item.ID)
	_, name, _ := ParseRepoID(repoID)
	tags := make([]csghub.Tag, 0, len(item.Tags)+3)
	seenTags := make(map[string]struct{}, len(item.Tags)+3)
	for _, value := range item.Tags {
		category := "tag"
		tagName := value
		lowerValue := strings.ToLower(value)
		switch lowerValue {
		case "gguf", "safetensors", "pytorch":
			category = "framework"
		}
		switch {
		case strings.HasPrefix(lowerValue, "license:"):
			category = "license"
			tagName = strings.TrimSpace(value[len("license:"):])
		case strings.EqualFold(value, item.PipelineTag):
			category = "task"
		case strings.EqualFold(value, item.LibraryName):
			category = "runtime_framework"
		}
		tags = appendRegistryTag(tags, seenTags, tagName, category)
	}
	if item.PipelineTag != "" {
		tags = appendRegistryTag(tags, seenTags, item.PipelineTag, "task")
	}
	if item.LibraryName != "" {
		tags = appendRegistryTag(tags, seenTags, item.LibraryName, "runtime_framework")
	}
	for _, value := range stringSliceValue(item.CardData["tags"]) {
		tags = appendRegistryTag(tags, seenTags, value, "tag")
	}
	license := firstNonEmpty(stringValue(item.CardData["license"]), prefixedTagValue(item.Tags, "license:"))
	architecture := ""
	if len(item.Config.Architectures) > 0 {
		architecture = item.Config.Architectures[0]
	}
	gated := gatedModel(item.Gated)
	return csghub.Model{
		Name:           name,
		Nickname:       firstNonEmpty(stringValue(item.CardData["model_name"]), stringValue(item.CardData["pretty_name"])),
		Path:           repoID,
		Description:    firstNonEmpty(stringValue(item.CardData["description"]), stringValue(item.CardData["summary"])),
		Likes:          item.Likes,
		Downloads:      item.Downloads,
		Private:        item.Private,
		Tags:           tags,
		DefaultBranch:  "main",
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.LastModified,
		License:        license,
		Source:         string(SourceHuggingFace),
		HFPath:         repoID,
		ArtifactSource: string(SourceHuggingFace),
		Revision:       firstNonEmpty(item.SHA, "main"),
		Provider: &csghub.ModelProviderMetadata{
			HuggingFace: &csghub.HuggingFaceModelMetadata{
				Author:       item.Author,
				PipelineTag:  item.PipelineTag,
				LibraryName:  item.LibraryName,
				Languages:    stringListValue(item.CardData["language"]),
				BaseModels:   stringListValue(item.CardData["base_model"]),
				OriginalTags: append([]string(nil), item.Tags...),
				Gated:        gated,
				SHA:          item.SHA,
			},
		},
		Metadata: csghub.ModelMetadata{
			Architecture: architecture,
			ModelType:    item.Config.ModelType,
			ModelParams:  parameterBillions(item.SafeTensors.Total),
			TensorType:   tensorTypes(item.SafeTensors.Parameters),
		},
	}
}

func approximateTotal(headers http.Header, page, perPage, count int) int {
	for _, name := range []string{"X-Total-Count", "X-Total"} {
		if total, err := strconv.Atoi(headers.Get(name)); err == nil && total >= 0 {
			return total
		}
	}
	offset := (page - 1) * perPage
	total := offset + count
	if count == perPage {
		total++
	}
	return total
}

func escapeRepoID(repoID string) string {
	namespace, name, _ := ParseRepoID(repoID)
	return url.PathEscape(namespace) + "/" + url.PathEscape(name)
}

func escapePath(value string) string {
	parts := strings.Split(strings.TrimPrefix(value, "/"), "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func nextLink(value string) string {
	for _, link := range strings.Split(value, ",") {
		sections := strings.Split(link, ";")
		if len(sections) < 2 || !strings.Contains(sections[1], `rel="next"`) {
			continue
		}
		return strings.Trim(strings.TrimSpace(sections[0]), "<>")
	}
	return ""
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if value := stringValue(item); value != "" {
				values = append(values, value)
			}
		}
		return values
	default:
		return nil
	}
}

func stringListValue(value any) []string {
	if single := stringValue(value); single != "" {
		return []string{single}
	}
	return stringSliceValue(value)
}

func gatedModel(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.TrimSpace(typed) != "" && !strings.EqualFold(strings.TrimSpace(typed), "false")
	default:
		return false
	}
}
