package modelregistry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
)

type openCSGRegistry struct {
	client *csghub.Client
}

func NewOpenCSG(baseURL, token string) Registry {
	return &openCSGRegistry{client: csghub.NewClient(baseURL, token)}
}

func (r *openCSGRegistry) Source() Source { return SourceOpenCSG }

// FillRepoSizes resolves repository sizes in one batch call. The list payload
// already carries the default-branch size, so callers only pass models whose
// size is still unknown; the extras endpoint answers many ids at once.
func (r *openCSGRegistry) FillRepoSizes(ctx context.Context, models []csghub.Model) error {
	repoIDs := make([]int, 0, len(models))
	for _, item := range models {
		if item.RepositoryID > 0 {
			repoIDs = append(repoIDs, item.RepositoryID)
		}
	}
	if len(repoIDs) == 0 {
		return nil
	}
	extras, err := r.client.GetRepoExtras(ctx, repoIDs)
	if err != nil {
		return err
	}
	sizes := make(map[int]int64, len(extras))
	for _, item := range extras {
		sizes[item.RepoID] = item.Size
	}
	for i := range models {
		// The extras endpoint reports 0 for some large mirrors; a missing answer
		// must not clear a size another source already provided.
		if size := sizes[models[i].RepositoryID]; size > 0 {
			models[i].RepoSize = size
		}
	}

	// The extras endpoint reports 0 for some large repositories. Their detail
	// payload still carries parameter counts, which is the same estimate the
	// detail page applies, so fall back to it instead of leaving the size
	// unknown and letting the list disagree with the detail.
	const concurrency = 6
	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, concurrency)
	)
	for i := range models {
		if models[i].RepoSize > 0 || models[i].Metadata.ValidModelParams() > 0 ||
			strings.TrimSpace(models[i].Path) == "" {
			continue
		}
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			details, err := r.GetModel(ctx, models[i].Path, "")
			if err != nil || details == nil || details.Metadata.ValidModelParams() <= 0 {
				return
			}
			models[i].Metadata.ModelParams = details.Metadata.ValidModelParams()
			models[i].Metadata.ModelParamsValid = details.Metadata.ModelParamsValid
		}()
	}
	wg.Wait()
	return nil
}

// fillFileSizesPerModelTimeout bounds one repository's tree walk. The walk
// costs one request per directory, so a repository with a hundred directories
// can outlive the deadline of the whole batch and starve every model that was
// queued behind it. A per-model deadline lets a slow walk fail on its own and
// frees its slot for the next model.
var fillFileSizesPerModelTimeout = 1500 * time.Millisecond

// FillFileSizes sums the file tree for repositories whose extras entry reports
// no size. Large mirrors often have no storage record, and the detail page
// already sizes them from the tree, so the list must use the same source or it
// would keep promising models that do not fit. The recursive tree costs one
// request per directory, so callers bound how many models they pass.
//
// maxSize is the smallest repository size that the caller would reject. A walk
// stops as soon as the total passes it, so a huge repository costs a couple of
// requests instead of a full traversal, and the reported size is a lower bound
// that is still large enough to be rejected.
func (r *openCSGRegistry) FillFileSizes(ctx context.Context, models []csghub.Model, maxSize int64) error {
	const concurrency = 8
	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, concurrency)
		errMu    sync.Mutex
		firstErr error
	)
	for i := range models {
		if models[i].RepoSize > 0 || strings.TrimSpace(models[i].Path) == "" {
			continue
		}
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			modelCtx, cancel := context.WithTimeout(ctx, fillFileSizesPerModelTimeout)
			defer cancel()
			total, err := r.modelTreeSize(modelCtx, models[i].Path, maxSize)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			if total > 0 {
				models[i].RepoSize = total
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// modelTreeSize mirrors ListFiles but stops once the tree passes maxSize.
func (r *openCSGRegistry) modelTreeSize(ctx context.Context, repoID string, maxSize int64) (int64, error) {
	if err := r.checkRevision(ctx, repoID, ""); err != nil {
		return 0, err
	}
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return 0, err
	}
	return r.client.GetModelTreeSize(ctx, namespace, name, maxSize)
}

// OpenCSG's existing API resolves the repository default branch implicitly.
func (r *openCSGRegistry) DefaultRevision() string { return "" }

func (r *openCSGRegistry) ListModels(ctx context.Context, options ListOptions) ([]csghub.Model, int, error) {
	models, total, err := r.client.ListModels(ctx, csghub.ModelListParams{
		Search:         options.Search,
		Sort:           options.Sort,
		Page:           options.Page,
		PerPage:        options.PerPage,
		Source:         options.UpstreamSource,
		Tags:           openCSGListTags(options.Task, options.Framework),
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
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return nil, err
	}
	model, err := r.client.GetModel(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	// The upstream API follows the default branch implicitly. Deployments may
	// name that branch explicitly, but other branches/tags/commits are unsupported.
	if requested := strings.TrimSpace(revision); requested != "" && requested != strings.TrimSpace(model.DefaultBranch) {
		return nil, fmt.Errorf("OpenCSG custom revisions are not supported by the local API")
	}
	model.ArtifactSource = string(SourceOpenCSG)
	model.Revision = model.DefaultBranch
	return model, nil
}

func (r *openCSGRegistry) ListFiles(ctx context.Context, repoID, revision string) ([]csghub.RepoFile, string, error) {
	if err := r.checkRevision(ctx, repoID, revision); err != nil {
		return nil, "", err
	}
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return nil, "", err
	}
	files, err := r.client.GetModelTree(ctx, namespace, name)
	return files, "", err
}

func (r *openCSGRegistry) ReadFile(ctx context.Context, repoID, revision, filePath string) (string, error) {
	if err := r.checkRevision(ctx, repoID, revision); err != nil {
		return "", err
	}
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return "", err
	}
	return r.client.GetModelRawFile(ctx, namespace, name, filePath)
}

func (r *openCSGRegistry) DownloadSnapshot(ctx context.Context, repoID, revision, destDir string, quants []string, progress csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error) {
	if err := r.checkRevision(ctx, repoID, revision); err != nil {
		return nil, "", err
	}
	namespace, name, err := ParseRepoID(repoID)
	if err != nil {
		return nil, "", err
	}
	files, err := r.client.SnapshotDownload(ctx, namespace, name, destDir, quants, progress)
	return files, "", err
}

func (r *openCSGRegistry) checkRevision(ctx context.Context, repoID, revision string) error {
	if strings.TrimSpace(revision) == "" {
		return nil
	}
	_, err := r.GetModel(ctx, repoID, revision)
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// openCSGListTags filters by task and framework together. Sending only one of
// them made a "text-generation" + "GGUF" listing scan task results for GGUF
// locally, which stopped after a few pages of a much larger result set.
func openCSGListTags(task, framework string) []csghub.TagFilter {
	var tags []csghub.TagFilter
	if task = strings.TrimSpace(task); task != "" {
		tags = append(tags, csghub.TagFilter{Category: "task", Name: task})
	}
	if framework = strings.TrimSpace(framework); framework != "" {
		tags = append(tags, csghub.TagFilter{Category: "framework", Name: framework})
	}
	return tags
}
