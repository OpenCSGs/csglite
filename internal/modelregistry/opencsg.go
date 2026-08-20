package modelregistry

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencsgs/csglite/internal/csghub"
)

type openCSGRegistry struct {
	client *csghub.Client
}

func NewOpenCSG(baseURL, token string) Registry {
	return &openCSGRegistry{client: csghub.NewClient(baseURL, token)}
}

func (r *openCSGRegistry) Source() Source { return SourceOpenCSG }

// OpenCSG's existing API resolves the repository default branch implicitly.
func (r *openCSGRegistry) DefaultRevision() string { return "" }

func (r *openCSGRegistry) ListModels(ctx context.Context, options ListOptions) ([]csghub.Model, int, error) {
	models, total, err := r.client.ListModels(ctx, csghub.ModelListParams{
		Search:         options.Search,
		Sort:           options.Sort,
		Page:           options.Page,
		PerPage:        options.PerPage,
		Source:         options.UpstreamSource,
		TagCategory:    firstNonEmptyCategory(options.Framework, options.Task),
		TagName:        firstNonEmpty(options.Task, options.Framework),
		ModelParamsMin: options.ModelParamsMin,
		ModelParamsMax: options.ModelParamsMax,
	})
	for i := range models {
		models[i].ArtifactSource = string(SourceOpenCSG)
		models[i].Revision = models[i].DefaultBranch
	}
	return models, total, err
}

func (r *openCSGRegistry) GetModel(ctx context.Context, repoID, revision string) (*csghub.Model, error) {
	if strings.TrimSpace(revision) != "" {
		return nil, fmt.Errorf("OpenCSG custom revisions are not supported by the local API")
	}
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return nil, err
	}
	model, err := r.client.GetModel(ctx, namespace, name)
	if err == nil {
		model.ArtifactSource = string(SourceOpenCSG)
		model.Revision = model.DefaultBranch
	}
	return model, err
}

func (r *openCSGRegistry) ListFiles(ctx context.Context, repoID, revision string) ([]csghub.RepoFile, string, error) {
	if strings.TrimSpace(revision) != "" {
		return nil, "", fmt.Errorf("OpenCSG custom revisions are not supported by the local API")
	}
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return nil, "", err
	}
	files, err := r.client.GetModelTree(ctx, namespace, name)
	return files, "", err
}

func (r *openCSGRegistry) ReadFile(ctx context.Context, repoID, revision, filePath string) (string, error) {
	if strings.TrimSpace(revision) != "" {
		return "", fmt.Errorf("OpenCSG custom revisions are not supported by the local API")
	}
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return "", err
	}
	return r.client.GetModelRawFile(ctx, namespace, name, filePath)
}

func (r *openCSGRegistry) DownloadSnapshot(ctx context.Context, repoID, revision, destDir string, quants []string, progress csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error) {
	if strings.TrimSpace(revision) != "" {
		return nil, "", fmt.Errorf("OpenCSG custom revisions are not supported by the local API")
	}
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return nil, "", err
	}
	files, err := r.client.SnapshotDownload(ctx, namespace, name, destDir, quants, progress)
	return files, "", err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyCategory(framework, task string) string {
	if strings.TrimSpace(task) != "" {
		return "task"
	}
	if strings.TrimSpace(framework) != "" {
		return "framework"
	}
	return ""
}
