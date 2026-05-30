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
	return c.getModelTreeRecursive(ctx, namespace, name, "")
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

func (c *Client) getModelTreeRecursive(ctx context.Context, namespace, name, treePath string) ([]RepoFile, error) {
	root, err := c.getModelTreePage(ctx, namespace, name, treePath)
	if err != nil {
		return nil, err
	}

	out := make([]RepoFile, 0, len(root))
	queue := make([]string, 0)
	seenDirs := make(map[string]struct{})
	for _, entry := range root {
		out = append(out, entry)
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

	for len(queue) > 0 {
		dirPath := queue[0]
		queue = queue[1:]

		children, err := c.getModelTreePage(ctx, namespace, name, dirPath)
		if err != nil {
			return nil, err
		}
		for _, entry := range children {
			out = append(out, entry)
			if entry.Type != "dir" {
				continue
			}
			childPath := entry.Path
			if childPath == "" {
				childPath = entry.Name
			}
			if childPath == "" {
				continue
			}
			if _, ok := seenDirs[childPath]; ok {
				continue
			}
			seenDirs[childPath] = struct{}{}
			queue = append(queue, childPath)
		}
	}

	return out, nil
}

// SearchModels searches for models by keyword.
func (c *Client) SearchModels(ctx context.Context, query string, page, perPage int) ([]Model, int, error) {
	return c.ListModels(ctx, ModelListParams{
		Search:  query,
		Page:    page,
		PerPage: perPage,
	})
}
