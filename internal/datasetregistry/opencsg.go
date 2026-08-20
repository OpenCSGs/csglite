package datasetregistry

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencsgs/csglite/internal/csghub"
	artifactregistry "github.com/opencsgs/csglite/internal/registry"
)

type openCSGRegistry struct {
	client *csghub.Client
	http   artifactregistry.Client
}

func NewOpenCSG(baseURL, token string) Registry {
	client := csghub.NewClient(baseURL, token)
	return &openCSGRegistry{
		client: client,
		http:   artifactregistry.NewClient(client.BaseURL(), token, artifactregistry.DefaultUserAgent),
	}
}

func (r *openCSGRegistry) Source() Source          { return SourceOpenCSG }
func (r *openCSGRegistry) DefaultRevision() string { return "" }

func (r *openCSGRegistry) ListDatasets(ctx context.Context, options ListOptions) ([]csghub.Dataset, int, error) {
	datasets, total, err := r.client.ListDatasets(ctx, csghub.DatasetListParams{
		Search: options.Search, Sort: options.Sort, Page: options.Page,
		PerPage: options.PerPage, Source: options.UpstreamSource,
	})
	for index := range datasets {
		datasets[index].ArtifactSource = string(SourceOpenCSG)
		datasets[index].Revision = datasets[index].DefaultBranch
	}
	return datasets, total, err
}

func (r *openCSGRegistry) GetDataset(ctx context.Context, repoID, revision string) (*csghub.Dataset, error) {
	if strings.TrimSpace(revision) != "" {
		return nil, fmt.Errorf("OpenCSG custom revisions are not supported by the local API")
	}
	namespace, name, err := artifactregistry.ParseRepoID(repoID)
	if err != nil {
		return nil, err
	}
	dataset, err := r.client.GetDataset(ctx, namespace, name)
	if err == nil {
		dataset.ArtifactSource = string(SourceOpenCSG)
		dataset.Revision = dataset.DefaultBranch
	}
	return dataset, err
}

func (r *openCSGRegistry) ListFiles(ctx context.Context, repoID, revision string) ([]csghub.RepoFile, string, error) {
	if strings.TrimSpace(revision) != "" {
		return nil, "", fmt.Errorf("OpenCSG custom revisions are not supported by the local API")
	}
	namespace, name, err := artifactregistry.ParseRepoID(repoID)
	if err != nil {
		return nil, "", err
	}
	files, err := r.client.GetDatasetTree(ctx, namespace, name)
	return files, "", err
}

func (r *openCSGRegistry) DownloadSnapshot(ctx context.Context, repoID, revision, destDir string, progress csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error) {
	files, resolved, err := r.ListFiles(ctx, repoID, revision)
	if err != nil {
		return nil, "", err
	}
	files, err = artifactregistry.DownloadSnapshot(ctx, r.http, files, destDir, func(file csghub.RepoFile) string {
		return r.http.BaseURL() + "/csg/datasets/" + artifactregistry.EscapeRepoID(repoID) + "/resolve/main/" + artifactregistry.EscapePath(file.Path)
	}, progress)
	return files, resolved, err
}
