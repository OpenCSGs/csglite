package csghub

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListModels returns a paginated list of models.
func (c *Client) ListModels(ctx context.Context, params ModelListParams) ([]Model, int, error) {
	q := url.Values{}
	if params.Search != "" {
		q.Set("search", params.Search)
	}
	if params.Sort != "" {
		q.Set("sort", params.Sort)
	}
	if params.Page > 0 {
		q.Set("page", strconv.Itoa(params.Page))
	}
	if params.PerPage > 0 {
		q.Set("per", strconv.Itoa(params.PerPage))
	}
	if params.Source != "" {
		q.Set("source", params.Source)
	}
	if params.Framework != "" {
		q.Set("framework", params.Framework)
	}
	if params.TagCategory != "" {
		q.Set("tag_category", params.TagCategory)
	}
	if params.TagName != "" {
		q.Set("tag_name", params.TagName)
	}
	for _, tag := range params.Tags {
		if tag.Category == "" || tag.Name == "" {
			continue
		}
		// CSGHub pairs the i-th tag_category with the i-th tag_name.
		q.Add("tag_category", tag.Category)
		q.Add("tag_name", tag.Name)
	}
	if params.ModelParamsMin != "" {
		q.Set("model_params_min", params.ModelParamsMin)
	}
	if params.ModelParamsMax != "" {
		q.Set("model_params_max", params.ModelParamsMax)
	}

	path := "/api/v1/models"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var resp ListResponse[Model]
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, 0, fmt.Errorf("listing models: %w", err)
	}
	return resp.Data, resp.Total, nil
}

// GetModel returns details for a specific model.
func (c *Client) GetModel(ctx context.Context, namespace, name string) (*Model, error) {
	path := fmt.Sprintf("/api/v1/models/%s/%s", namespace, name)

	var resp APIResponse[Model]
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("getting model %s/%s: %w", namespace, name, err)
	}
	return &resp.Data, nil
}

// GetRepoExtras returns repository sizes for a batch of repository IDs.
func (c *Client) GetRepoExtras(ctx context.Context, repoIDs []int) ([]RepoExtraItem, error) {
	if len(repoIDs) == 0 {
		return []RepoExtraItem{}, nil
	}
	var resp APIResponse[[]RepoExtraItem]
	if err := c.postJSON(ctx, "/api/v1/repos/extra", map[string][]int{"repo_ids": repoIDs}, &resp); err != nil {
		return nil, fmt.Errorf("getting repository sizes: %w", err)
	}
	return resp.Data, nil
}

// GetModelRawFile returns the text content of a non-LFS model repository file.
func (c *Client) GetModelRawFile(ctx context.Context, namespace, name, filePath string) (string, error) {
	path := fmt.Sprintf("/api/v1/models/%s/%s/raw/%s", namespace, name, filePath)

	var resp APIResponse[string]
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return "", fmt.Errorf("getting raw file %s from %s/%s: %w", filePath, namespace, name, err)
	}
	return resp.Data, nil
}

// GetModelTree returns the file tree for a model repository.
func (c *Client) GetModelTree(ctx context.Context, namespace, name string) ([]RepoFile, error) {
	var out []RepoFile
	err := c.walkModelTree(ctx, namespace, name, func(entries []RepoFile) bool {
		out = append(out, entries...)
		return true
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetModelTreeSize sums the file sizes in the repository tree and stops as soon
// as the total exceeds stopAbove. Repositories with many directories cost one
// request each, so callers that only need to know whether a repository fits
// pass their size budget and get a lower bound without walking the whole tree.
// A stopAbove of zero disables the bound.
func (c *Client) GetModelTreeSize(ctx context.Context, namespace, name string, stopAbove int64) (int64, error) {
	var total int64
	err := c.walkModelTree(ctx, namespace, name, func(entries []RepoFile) bool {
		for _, entry := range entries {
			if entry.Type == "file" && entry.Size > 0 {
				total += entry.Size
			}
		}
		return stopAbove <= 0 || total <= stopAbove
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func (c *Client) getModelTreePage(ctx context.Context, namespace, name, treePath string) ([]RepoFile, error) {
	path := fmt.Sprintf("/api/v1/models/%s/%s/tree", namespace, name)
	if treePath != "" {
		q := url.Values{}
		q.Set("path", treePath)
		path += "?" + q.Encode()
	}

	var resp APIResponse[[]RepoFile]
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("getting file tree for %s/%s: %w", namespace, name, err)
	}
	return resp.Data, nil
}

// walkModelTree visits the repository tree breadth first. visit returns false to
// stop the walk early; the entries of the page that stopped it are still
// delivered.
func (c *Client) walkModelTree(ctx context.Context, namespace, name string, visit func([]RepoFile) bool) error {
	root, err := c.getModelTreePage(ctx, namespace, name, "")
	if err != nil {
		return err
	}

	seenDirs := make(map[string]struct{})
	queue := make([]string, 0)
	enqueue := func(entries []RepoFile) {
		for _, entry := range entries {
			if entry.Type != "dir" {
				continue
			}
			dirPath := entry.Path
			if dirPath == "" {
				dirPath = entry.Name
			}
			if dirPath == "" {
				continue
			}
			if _, ok := seenDirs[dirPath]; ok {
				continue
			}
			seenDirs[dirPath] = struct{}{}
			queue = append(queue, dirPath)
		}
	}

	if !visit(root) {
		return nil
	}
	enqueue(root)
	for len(queue) > 0 {
		dirPath := queue[0]
		queue = queue[1:]

		children, err := c.getModelTreePage(ctx, namespace, name, dirPath)
		if err != nil {
			return err
		}
		if !visit(children) {
			return nil
		}
		enqueue(children)
	}

	return nil
}

// SearchModels searches for models by keyword.
func (c *Client) SearchModels(ctx context.Context, query string, page, perPage int) ([]Model, int, error) {
	return c.ListModels(ctx, ModelListParams{
		Search:  query,
		Page:    page,
		PerPage: perPage,
	})
}
