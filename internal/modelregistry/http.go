package modelregistry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/csghub"
	artifactregistry "github.com/opencsgs/csglite/internal/registry"
)

const registryUserAgent = "csghub-lite/model-registry"

type registryHTTPClient struct {
	baseURL string
	token   string
	core    artifactregistry.Client
}

func newRegistryHTTPClient(baseURL, token string) registryHTTPClient {
	return registryHTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		core:    artifactregistry.NewClient(baseURL, token, registryUserAgent),
	}
}

func (c registryHTTPClient) newRequest(ctx context.Context, method, requestURL string) (*http.Request, error) {
	return c.core.NewRequest(ctx, method, requestURL)
}

func (c registryHTTPClient) getJSON(ctx context.Context, requestURL string, out any) (http.Header, error) {
	headers, err := c.core.GetJSON(ctx, requestURL, out)
	return headers, modelRegistryError(err)
}

func (c registryHTTPClient) getText(ctx context.Context, requestURL string) (string, error) {
	text, err := c.core.GetText(ctx, requestURL)
	return text, modelRegistryError(err)
}

type snapshotDownloadURL func(csghub.RepoFile) string

func downloadRegistrySnapshot(
	ctx context.Context,
	client registryHTTPClient,
	files []csghub.RepoFile,
	destDir string,
	quants []string,
	urlFor snapshotDownloadURL,
	progress csghub.SnapshotProgressFunc,
) ([]csghub.RepoFile, error) {
	files, err := csghub.FilterModelSnapshotFiles(regularFiles(files), quants)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("model repository contains no downloadable files")
	}

	// Keep model registry behavior unchanged: model downloads validate size but
	// do not reinterpret provider object IDs as content hashes.
	downloadFiles := append([]csghub.RepoFile(nil), files...)
	for index := range downloadFiles {
		downloadFiles[index].SHA = ""
		downloadFiles[index].LFSSHA256 = ""
	}
	_, err = artifactregistry.DownloadSnapshotWithOptions(ctx, client.core, downloadFiles, destDir, func(file csghub.RepoFile) string {
		return urlFor(file)
	}, progress, modelDownloadOptions())
	return files, modelRegistryError(err)
}

func (c registryHTTPClient) download(ctx context.Context, requestURL, destPath string, expectedSize int64, progress csghub.ProgressFunc) error {
	return modelRegistryError(c.core.DownloadWithOptions(ctx, requestURL, destPath, expectedSize, "", progress, modelDownloadOptions()))
}

func (c registryHTTPClient) downloadOnce(ctx context.Context, requestURL, destPath string, expectedSize int64, progress csghub.ProgressFunc) error {
	return modelRegistryError(c.core.DownloadOnceWithOptions(ctx, requestURL, destPath, expectedSize, "", progress, modelDownloadOptions()))
}

func modelDownloadOptions() artifactregistry.DownloadOptions {
	return artifactregistry.DownloadOptions{
		ResumeInvalidDestination: true,
		AllowOKContentRange:      true,
		RetryIntegrity:           true,
	}
}

type registryHTTPError struct {
	statusCode int
	message    string
}

func (e *registryHTTPError) Error() string { return e.message }

func registryStatusError(statusCode int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	switch statusCode {
	case http.StatusUnauthorized:
		return &registryHTTPError{statusCode: statusCode, message: "registry authentication required (401); configure an access token in Settings"}
	case http.StatusForbidden:
		return &registryHTTPError{statusCode: statusCode, message: "registry access denied (403); configure an access token and accept any required model license"}
	default:
		return &registryHTTPError{statusCode: statusCode, message: fmt.Sprintf("registry API error %d: %s", statusCode, detail)}
	}
}

func modelRegistryError(err error) error {
	if err == nil {
		return nil
	}
	var statusErr *artifactregistry.HTTPError
	if errors.As(err, &statusErr) {
		return registryStatusError(statusErr.StatusCode, []byte(strings.TrimPrefix(statusErr.Message, fmt.Sprintf("registry API error %d: ", statusErr.StatusCode))))
	}
	return err
}

func safeRepositoryPath(value string) (string, error) {
	return artifactregistry.SafeRepositoryPath(value)
}

func regularFiles(files []csghub.RepoFile) []csghub.RepoFile {
	return artifactregistry.RegularFiles(files)
}
