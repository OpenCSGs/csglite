package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	err = modelRegistryError(err)
	if err == nil {
		return headers, nil
	}
	wait, ok := rateLimitRetryDelay(err)
	if !ok {
		return headers, err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return headers, err
	case <-timer.C:
	}
	headers, err = c.core.GetJSON(ctx, requestURL, out)
	return headers, modelRegistryError(err)
}

// rateLimitRetryDelay reports whether a 429 should be retried once in place.
// Only short waits qualify; a long quota window must not block the request.
func rateLimitRetryDelay(err error) (time.Duration, bool) {
	var statusErr *registryHTTPError
	if !errors.As(err, &statusErr) || statusErr.statusCode != http.StatusTooManyRequests {
		return 0, false
	}
	if statusErr.retryAfter <= 0 || statusErr.retryAfter > registryRateLimitRetryCap {
		return 0, false
	}
	return statusErr.retryAfter, true
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
	retryAfter time.Duration
}

func (e *registryHTTPError) Error() string { return e.message }

// registryRateLimitRetryCap bounds the automatic retry after a rate limit
// response. Longer waits (the Hugging Face quota reports tens of seconds) are
// surfaced to the caller instead of blocking the request.
const registryRateLimitRetryCap = 3 * time.Second

func registryStatusError(statusCode int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	switch statusCode {
	case http.StatusUnauthorized:
		return &registryHTTPError{statusCode: statusCode, message: "registry authentication required (401); configure an access token in Settings"}
	case http.StatusForbidden:
		return &registryHTTPError{statusCode: statusCode, message: "registry access denied (403); configure an access token and accept any required model license"}
	case http.StatusTooManyRequests:
		retryAfter := registryRetryAfter(body)
		message := "registry rate limit reached (429); retry later"
		if retryAfter > 0 {
			seconds := int((retryAfter + time.Second - 1) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			message = fmt.Sprintf("registry rate limit reached (429); retry in about %ds", seconds)
		}
		return &registryHTTPError{statusCode: statusCode, message: message, retryAfter: retryAfter}
	default:
		return &registryHTTPError{statusCode: statusCode, message: fmt.Sprintf("registry API error %d: %s", statusCode, detail)}
	}
}

// registryRetryAfter reads the wait time from a rate limit response. Hugging
// Face reports it as {"error": "...", "retryAfter": 22}.
func registryRetryAfter(body []byte) time.Duration {
	var payload struct {
		RetryAfter float64 `json:"retryAfter"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.RetryAfter <= 0 {
		return 0
	}
	return time.Duration(payload.RetryAfter * float64(time.Second))
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

// HTTPStatusCode returns the HTTP status behind err, if it is a CSGHub HTTP
// error. Only the OpenCSG registry reports HTTP errors this way; the other
// sources wrap them in a private type, see RegistryStatus.
func HTTPStatusCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if httpErr, ok := csghub.AsHTTPError(err); ok {
		return httpErr.StatusCode, true
	}
	return 0, false
}

// RegistryStatus returns the HTTP status carried by the private error type the
// provider adapters use (Hugging Face, ModelScope).
func RegistryStatus(err error) (int, bool) {
	var statusErr *registryHTTPError
	if errors.As(err, &statusErr) {
		return statusErr.statusCode, true
	}
	return 0, false
}

// RetryAfter reports how long a rate limited registry asked the caller to wait
// before retrying.
func RetryAfter(err error) (time.Duration, bool) {
	var statusErr *registryHTTPError
	if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusTooManyRequests && statusErr.retryAfter > 0 {
		return statusErr.retryAfter, true
	}
	return 0, false
}

func safeRepositoryPath(value string) (string, error) {
	return artifactregistry.SafeRepositoryPath(value)
}

func regularFiles(files []csghub.RepoFile) []csghub.RepoFile {
	return artifactregistry.RegularFiles(files)
}
