package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
)

const (
	registryRequestTimeout   = 30 * time.Second
	maxRegistryResponseBytes = 16 << 20
	maxConcurrentDownloads   = 3
	maxDownloadRetries       = 3
	registryUserAgent        = "csghub-lite/model-registry"
)

type registryHTTPClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newRegistryHTTPClient(baseURL, token string) registryHTTPClient {
	return registryHTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		client:  &http.Client{Timeout: registryRequestTimeout},
	}
}

func (c registryHTTPClient) newRequest(ctx context.Context, method, requestURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", registryUserAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

func (c registryHTTPClient) getJSON(ctx context.Context, requestURL string, out any) (http.Header, error) {
	req, err := c.newRequest(ctx, http.MethodGet, requestURL)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, registryStatusError(resp.StatusCode, body)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRegistryResponseBytes)).Decode(out); err != nil {
		return nil, fmt.Errorf("decoding registry response: %w", err)
	}
	return resp.Header.Clone(), nil
}

func (c registryHTTPClient) getText(ctx context.Context, requestURL string) (string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, requestURL)
	if err != nil {
		return "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", registryStatusError(resp.StatusCode, body)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryResponseBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tracker := newRegistryProgressTracker(files)
	sem := make(chan struct{}, maxConcurrentDownloads)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for index, file := range files {
		wg.Add(1)
		go func(index int, file csghub.RepoFile) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			relative, err := safeRepositoryPath(file.Path)
			if err != nil {
				setFirstDownloadError(&mu, &firstErr, cancel, err)
				return
			}
			destPath := filepath.Join(destDir, relative)
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				setFirstDownloadError(&mu, &firstErr, cancel, fmt.Errorf("creating directory for %s: %w", file.Path, err))
				return
			}
			fileProgress := func(completed, total int64) {
				if progress == nil {
					return
				}
				completedAll, totalAll := tracker.update(index, completed, total)
				progress(csghub.SnapshotProgress{
					FileName:          file.Name,
					FileIndex:         index,
					TotalFiles:        len(files),
					BytesCompleted:    completed,
					BytesTotal:        total,
					BytesCompletedAll: completedAll,
					BytesTotalAll:     totalAll,
				})
			}
			if err := client.download(ctx, urlFor(file), destPath, file.Size, fileProgress); err != nil {
				setFirstDownloadError(&mu, &firstErr, cancel, fmt.Errorf("downloading %s: %w", file.Path, err))
			}
		}(index, file)
	}
	wg.Wait()
	return files, firstErr
}

func (c registryHTTPClient) download(ctx context.Context, requestURL, destPath string, expectedSize int64, progress csghub.ProgressFunc) error {
	var lastErr error
	for attempt := 0; attempt <= maxDownloadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<(attempt-1)) * time.Second):
			}
		}
		lastErr = c.downloadOnce(ctx, requestURL, destPath, expectedSize, progress)
		if lastErr == nil {
			return nil
		}
		if !retryableRegistryError(lastErr) {
			return lastErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func (c registryHTTPClient) downloadOnce(ctx context.Context, requestURL, destPath string, expectedSize int64, progress csghub.ProgressFunc) error {
	partialPath := destPath + ".part"
	if info, err := os.Stat(destPath); err == nil {
		if expectedSize <= 0 || info.Size() == expectedSize {
			if progress != nil {
				progress(info.Size(), expectedSize)
			}
			return nil
		}
		if _, partialErr := os.Stat(partialPath); os.IsNotExist(partialErr) {
			if renameErr := os.Rename(destPath, partialPath); renameErr != nil {
				return fmt.Errorf("preparing partial download: %w", renameErr)
			}
		} else if removeErr := os.Remove(destPath); removeErr != nil {
			return fmt.Errorf("removing invalid completed file: %w", removeErr)
		}
	}
	var existingSize int64
	if info, err := os.Stat(partialPath); err == nil {
		existingSize = info.Size()
		if expectedSize > 0 && existingSize == expectedSize {
			if err := os.Rename(partialPath, destPath); err != nil {
				return fmt.Errorf("finalizing downloaded file: %w", err)
			}
			if progress != nil {
				progress(expectedSize, expectedSize)
			}
			return nil
		}
		if expectedSize > 0 && existingSize > expectedSize {
			existingSize = 0
		}
	}
	req, err := c.newRequest(ctx, http.MethodGet, requestURL)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	appendResponse := resp.StatusCode == http.StatusPartialContent
	if appendResponse && !contentRangeStartsAt(resp.Header.Get("Content-Range"), existingSize) {
		return fmt.Errorf("invalid partial response range %q for offset %d", resp.Header.Get("Content-Range"), existingSize)
	}
	if resp.StatusCode == http.StatusOK && existingSize > 0 {
		appendResponse = contentRangeStartsAt(resp.Header.Get("Content-Range"), existingSize)
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && expectedSize > 0 && existingSize == expectedSize {
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return registryStatusError(resp.StatusCode, body)
	}
	if !appendResponse {
		existingSize = 0
	}
	flags := os.O_CREATE | os.O_WRONLY
	if appendResponse {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(partialPath, flags, 0o644)
	if err != nil {
		return err
	}

	total := expectedSize
	if total <= 0 && resp.ContentLength >= 0 {
		total = existingSize + resp.ContentLength
	}
	if err := copyRegistryDownload(ctx, file, resp.Body, existingSize, total, progress); err != nil {
		_ = file.Close()
		return err
	}
	if expectedSize > 0 {
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("checking downloaded file size: %w", err)
		}
		if info.Size() != expectedSize {
			_ = file.Close()
			return fmt.Errorf("incomplete download: received %d bytes, expected %d", info.Size(), expectedSize)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing downloaded file: %w", err)
	}
	if err := os.Rename(partialPath, destPath); err != nil {
		return fmt.Errorf("finalizing downloaded file: %w", err)
	}
	return nil
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

func retryableRegistryError(err error) bool {
	var statusErr *registryHTTPError
	if !errors.As(err, &statusErr) {
		return true
	}
	return statusErr.statusCode == http.StatusRequestTimeout ||
		statusErr.statusCode == http.StatusTooManyRequests ||
		statusErr.statusCode >= 500
}

func copyRegistryDownload(ctx context.Context, dst io.Writer, src io.Reader, completed, total int64, progress csghub.ProgressFunc) error {
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			if _, err := dst.Write(buffer[:n]); err != nil {
				return err
			}
			completed += int64(n)
			if progress != nil {
				progress(completed, total)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func safeRepositoryPath(value string) (string, error) {
	value = filepath.FromSlash(strings.TrimSpace(value))
	clean := filepath.Clean(value)
	if value == "" || clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe repository file path %q", value)
	}
	return clean, nil
}

func regularFiles(files []csghub.RepoFile) []csghub.RepoFile {
	out := make([]csghub.RepoFile, 0, len(files))
	for _, file := range files {
		if file.Type == "file" && strings.TrimSpace(file.Path) != "" {
			out = append(out, file)
		}
	}
	return out
}

func setFirstDownloadError(mu *sync.Mutex, target *error, cancel context.CancelFunc, err error) {
	mu.Lock()
	defer mu.Unlock()
	if *target == nil {
		*target = err
		cancel()
	}
}

func contentRangeStartsAt(value string, expected int64) bool {
	value = strings.TrimSpace(strings.TrimPrefix(value, "bytes "))
	start, _, ok := strings.Cut(value, "-")
	if !ok {
		return false
	}
	parsed, err := strconv.ParseInt(start, 10, 64)
	return err == nil && parsed == expected
}

type registryProgressTracker struct {
	mu        sync.Mutex
	completed []int64
	totals    []int64
	total     int64
}

func newRegistryProgressTracker(files []csghub.RepoFile) *registryProgressTracker {
	tracker := &registryProgressTracker{
		completed: make([]int64, len(files)),
		totals:    make([]int64, len(files)),
	}
	for index, file := range files {
		if file.Size > 0 {
			tracker.totals[index] = file.Size
			tracker.total += file.Size
		}
	}
	return tracker
}

func (t *registryProgressTracker) update(index int, completed, total int64) (int64, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if total > 0 && total != t.totals[index] {
		t.total += total - t.totals[index]
		t.totals[index] = total
	}
	if t.totals[index] > 0 && completed > t.totals[index] {
		completed = t.totals[index]
	}
	t.completed[index] = completed
	var completedAll int64
	for _, value := range t.completed {
		completedAll += value
	}
	return completedAll, t.total
}
