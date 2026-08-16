package csghub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type CreateDatasetRequest struct {
	Namespace     string `json:"namespace,omitempty"`
	Name          string `json:"name"`
	Nickname      string `json:"nickname,omitempty"`
	Description   string `json:"description,omitempty"`
	Private       bool   `json:"private"`
	License       string `json:"license,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Type          int    `json:"type"`
}

func (c *Client) CreateDataset(ctx context.Context, request CreateDatasetRequest) (*Dataset, error) {
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		return nil, fmt.Errorf("dataset name is required")
	}
	if request.Nickname == "" {
		request.Nickname = request.Name
	}
	if request.License == "" {
		request.License = "other"
	}
	if request.DefaultBranch == "" {
		request.DefaultBranch = "main"
	}
	var response APIResponse[Dataset]
	if err := c.postJSON(ctx, "/api/v1/datasets", request, &response); err != nil {
		return nil, fmt.Errorf("creating dataset: %w", err)
	}
	return &response.Data, nil
}

func (c *Client) UploadDatasetFile(
	ctx context.Context,
	namespace, name, localPath, repositoryPath, branch, message string,
) error {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	repositoryPath = strings.TrimSpace(strings.ReplaceAll(repositoryPath, "\\", "/"))
	if namespace == "" || name == "" {
		return fmt.Errorf("dataset namespace and name are required")
	}
	if repositoryPath == "" || strings.HasPrefix(repositoryPath, "/") || strings.Contains(repositoryPath, "../") {
		return fmt.Errorf("invalid repository file path %q", repositoryPath)
	}
	if branch == "" {
		branch = "main"
	}
	if message == "" {
		message = "Upload " + repositoryPath
	}
	file, err := os.Open(filepath.Clean(localPath))
	if err != nil {
		return fmt.Errorf("opening dataset upload file: %w", err)
	}
	reader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	contentType := writer.FormDataContentType()
	go func() {
		defer file.Close()
		part, writeErr := writer.CreateFormFile("file", filepath.Base(repositoryPath))
		if writeErr == nil {
			_, writeErr = io.Copy(part, file)
		}
		for key, value := range map[string]string{
			"file_path": repositoryPath,
			"branch":    branch,
			"message":   message,
		} {
			if writeErr == nil {
				writeErr = writer.WriteField(key, value)
			}
		}
		if closeErr := writer.Close(); writeErr == nil {
			writeErr = closeErr
		}
		_ = pipeWriter.CloseWithError(writeErr)
	}()

	apiPath := fmt.Sprintf(
		"/api/v1/datasets/%s/%s/upload_file",
		url.PathEscape(namespace),
		url.PathEscape(name),
	)
	request, err := c.newRequest(ctx, http.MethodPost, apiPath, reader)
	if err != nil {
		_ = reader.Close()
		return err
	}
	request.Header.Set("Content-Type", contentType)
	response, err := c.do(request)
	if err != nil {
		_ = reader.Close()
		return fmt.Errorf("uploading dataset file %s: %w", repositoryPath, err)
	}
	defer response.Body.Close()
	var result APIResponse[json.RawMessage]
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil && err != io.EOF {
		return fmt.Errorf("decoding dataset upload response: %w", err)
	}
	return nil
}
