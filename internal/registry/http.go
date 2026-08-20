package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
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
	requestTimeout         = 30 * time.Second
	maxResponseBytes       = 16 << 20
	maxConcurrentDownloads = 3
	maxDownloadRetries     = 3
	DefaultUserAgent       = "csghub-lite/artifact-registry"
)

type Client struct {
	baseURL   string
	token     string
	userAgent string
	client    *http.Client
}

func NewClient(baseURL, token, userAgent string) Client {
	if strings.TrimSpace(userAgent) == "" {
		userAgent = DefaultUserAgent
	}
	return Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     strings.TrimSpace(token),
		userAgent: userAgent,
		client:    &http.Client{Timeout: requestTimeout},
	}
}

func (c Client) BaseURL() string { return c.baseURL }
func (c Client) Token() string   { return c.token }

func (c Client) NewRequest(ctx context.Context, method, requestURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

func (c Client) GetJSON(ctx context.Context, requestURL string, out any) (http.Header, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, requestURL)
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
		return nil, StatusError(resp.StatusCode, body)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return nil, fmt.Errorf("decoding registry response: %w", err)
	}
	return resp.Header.Clone(), nil
}

func (c Client) GetText(ctx context.Context, requestURL string) (string, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, requestURL)
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
		return "", StatusError(resp.StatusCode, body)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string { return e.Message }

func StatusError(statusCode int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	switch statusCode {
	case http.StatusUnauthorized:
		return &HTTPError{StatusCode: statusCode, Message: "registry authentication required (401); configure an access token in Settings"}
	case http.StatusForbidden:
		return &HTTPError{StatusCode: statusCode, Message: "registry access denied (403); configure an access token and accept any required artifact license"}
	default:
		return &HTTPError{StatusCode: statusCode, Message: fmt.Sprintf("registry API error %d: %s", statusCode, detail)}
	}
}

type IntegrityError struct{ Message string }

func (e *IntegrityError) Error() string { return e.Message }

type DownloadOptions struct {
	// ResumeInvalidDestination preserves the legacy model downloader behavior
	// of moving a wrong-sized final file into the partial path before resuming.
	ResumeInvalidDestination bool
	// AllowOKContentRange preserves compatibility with providers that returned
	// 200 plus Content-Range. Dataset adapters intentionally leave this false.
	AllowOKContentRange bool
	// RetryIntegrity preserves the legacy model downloader's retry policy for
	// truncated responses. Dataset checksum failures remain non-retryable.
	RetryIntegrity bool
}

func (c Client) Download(ctx context.Context, requestURL, destPath string, expectedSize int64, expectedSHA string, progress csghub.ProgressFunc) error {
	return c.DownloadWithOptions(ctx, requestURL, destPath, expectedSize, expectedSHA, progress, DownloadOptions{})
}

func (c Client) DownloadWithOptions(ctx context.Context, requestURL, destPath string, expectedSize int64, expectedSHA string, progress csghub.ProgressFunc, options DownloadOptions) error {
	var lastErr error
	for attempt := 0; attempt <= maxDownloadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<(attempt-1)) * time.Second):
			}
		}
		lastErr = c.DownloadOnceWithOptions(ctx, requestURL, destPath, expectedSize, expectedSHA, progress, options)
		if lastErr == nil {
			return nil
		}
		if !retryable(lastErr, options) {
			return lastErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

// DownloadOnce writes to destPath+".part" and only renames after validating
// all available integrity metadata. A partial file is resumed only after a
// standards-compliant 206 response with the requested Content-Range.
func (c Client) DownloadOnce(ctx context.Context, requestURL, destPath string, expectedSize int64, expectedSHA string, progress csghub.ProgressFunc) error {
	return c.DownloadOnceWithOptions(ctx, requestURL, destPath, expectedSize, expectedSHA, progress, DownloadOptions{})
}

func (c Client) DownloadOnceWithOptions(ctx context.Context, requestURL, destPath string, expectedSize int64, expectedSHA string, progress csghub.ProgressFunc, options DownloadOptions) error {
	partialPath := destPath + ".part"
	if valid, size := validFile(destPath, expectedSize, expectedSHA); valid {
		if progress != nil {
			progress(size, expectedSize)
		}
		return nil
	}
	if _, err := os.Stat(destPath); err == nil {
		if options.ResumeInvalidDestination {
			if _, partialErr := os.Stat(partialPath); os.IsNotExist(partialErr) {
				if err := os.Rename(destPath, partialPath); err != nil {
					return fmt.Errorf("preparing partial download: %w", err)
				}
			} else if err := os.Remove(destPath); err != nil {
				return fmt.Errorf("removing invalid completed file: %w", err)
			}
		} else if err := os.Remove(destPath); err != nil {
			return fmt.Errorf("removing invalid completed file: %w", err)
		}
	}

	var existingSize int64
	if info, err := os.Stat(partialPath); err == nil {
		existingSize = info.Size()
		if expectedSize > 0 && existingSize == expectedSize {
			if err := validateFile(partialPath, expectedSize, expectedSHA); err == nil {
				return finalizePartial(partialPath, destPath, expectedSize, progress)
			}
			existingSize = 0
		} else if expectedSize > 0 && existingSize > expectedSize {
			existingSize = 0
		}
	}

	req, err := c.NewRequest(ctx, http.MethodGet, requestURL)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	appendResponse := existingSize > 0 &&
		resp.StatusCode == http.StatusPartialContent &&
		contentRangeStartsAt(resp.Header.Get("Content-Range"), existingSize)
	if options.AllowOKContentRange && existingSize > 0 && resp.StatusCode == http.StatusOK {
		appendResponse = contentRangeStartsAt(resp.Header.Get("Content-Range"), existingSize)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return StatusError(resp.StatusCode, body)
	}
	if resp.StatusCode == http.StatusPartialContent && !appendResponse && existingSize > 0 {
		return fmt.Errorf("invalid partial response range %q for offset %d", resp.Header.Get("Content-Range"), existingSize)
	}
	if !appendResponse {
		existingSize = 0
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendResponse {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	file, err := os.OpenFile(partialPath, flags, 0o644)
	if err != nil {
		return err
	}
	total := expectedSize
	if total <= 0 && resp.ContentLength >= 0 {
		total = existingSize + resp.ContentLength
	}
	if err := copyDownload(ctx, file, resp.Body, existingSize, total, progress); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing downloaded file: %w", err)
	}
	if err := validateFile(partialPath, expectedSize, expectedSHA); err != nil {
		return err
	}
	return finalizePartial(partialPath, destPath, expectedSize, progress)
}

func DownloadSnapshot(
	ctx context.Context,
	client Client,
	files []csghub.RepoFile,
	destDir string,
	urlFor func(csghub.RepoFile) string,
	progress csghub.SnapshotProgressFunc,
) ([]csghub.RepoFile, error) {
	return DownloadSnapshotWithOptions(ctx, client, files, destDir, urlFor, progress, DownloadOptions{})
}

func DownloadSnapshotWithOptions(
	ctx context.Context,
	client Client,
	files []csghub.RepoFile,
	destDir string,
	urlFor func(csghub.RepoFile) string,
	progress csghub.SnapshotProgressFunc,
	options DownloadOptions,
) ([]csghub.RepoFile, error) {
	files = RegularFiles(files)
	if len(files) == 0 {
		return nil, errors.New("repository contains no downloadable files")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tracker := newProgressTracker(files)
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
			relative, err := SafeRepositoryPath(file.Path)
			if err != nil {
				setFirstError(&mu, &firstErr, cancel, err)
				return
			}
			destPath := filepath.Join(destDir, relative)
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				setFirstError(&mu, &firstErr, cancel, fmt.Errorf("creating directory for %s: %w", file.Path, err))
				return
			}
			fileProgress := func(completed, total int64) {
				if progress == nil {
					return
				}
				completedAll, totalAll := tracker.update(index, completed, total)
				progress(csghub.SnapshotProgress{
					FileName: file.Name, FileIndex: index, TotalFiles: len(files),
					BytesCompleted: completed, BytesTotal: total,
					BytesCompletedAll: completedAll, BytesTotalAll: totalAll,
				})
			}
			expectedSHA := file.SHA
			if file.LFSSHA256 != "" {
				expectedSHA = file.LFSSHA256
			}
			if err := client.DownloadWithOptions(ctx, urlFor(file), destPath, file.Size, expectedSHA, fileProgress, options); err != nil {
				setFirstError(&mu, &firstErr, cancel, fmt.Errorf("downloading %s: %w", file.Path, err))
			}
		}(index, file)
	}
	wg.Wait()
	return files, firstErr
}

func SafeRepositoryPath(value string) (string, error) {
	value = filepath.FromSlash(strings.TrimSpace(value))
	clean := filepath.Clean(value)
	if value == "" || clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe repository file path %q", value)
	}
	return clean, nil
}

func RegularFiles(files []csghub.RepoFile) []csghub.RepoFile {
	out := make([]csghub.RepoFile, 0, len(files))
	for _, file := range files {
		if file.Type == "file" && strings.TrimSpace(file.Path) != "" {
			out = append(out, file)
		}
	}
	return out
}

func ApproximateTotal(headers http.Header, page, perPage, count int) int {
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

func validFile(path string, expectedSize int64, expectedSHA string) (bool, int64) {
	info, err := os.Stat(path)
	if err != nil {
		return false, 0
	}
	return validateFile(path, expectedSize, expectedSHA) == nil, info.Size()
}

func validateFile(path string, expectedSize int64, expectedSHA string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return &IntegrityError{Message: fmt.Sprintf("incomplete download: received %d bytes, expected %d", info.Size(), expectedSize)}
	}
	expectedSHA = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expectedSHA)), "sha256:")
	var hasher hash.Hash
	switch len(expectedSHA) {
	case 64:
		hasher = sha256.New()
	case 0:
		return nil
	default:
		// Git blob OIDs (notably Hugging Face's 40-character tree OID) hash
		// the Git object envelope, not just the downloaded bytes.
		return nil
	}
	if _, err := hex.DecodeString(expectedSHA); err != nil {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != expectedSHA {
		return &IntegrityError{Message: fmt.Sprintf("download checksum mismatch: received %s, expected %s", actual, expectedSHA)}
	}
	return nil
}

func finalizePartial(partialPath, destPath string, expectedSize int64, progress csghub.ProgressFunc) error {
	if err := os.Rename(partialPath, destPath); err != nil {
		return fmt.Errorf("finalizing downloaded file: %w", err)
	}
	if progress != nil {
		info, _ := os.Stat(destPath)
		if info != nil {
			progress(info.Size(), expectedSize)
		}
	}
	return nil
}

func retryable(err error, options DownloadOptions) bool {
	var integrityErr *IntegrityError
	if errors.As(err, &integrityErr) {
		return options.RetryIntegrity
	}
	var statusErr *HTTPError
	if !errors.As(err, &statusErr) {
		return true
	}
	return statusErr.StatusCode == http.StatusRequestTimeout ||
		statusErr.StatusCode == http.StatusTooManyRequests ||
		statusErr.StatusCode >= 500
}

func copyDownload(ctx context.Context, dst io.Writer, src io.Reader, completed, total int64, progress csghub.ProgressFunc) error {
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

func contentRangeStartsAt(value string, expected int64) bool {
	value = strings.TrimSpace(strings.TrimPrefix(value, "bytes "))
	start, _, ok := strings.Cut(value, "-")
	if !ok {
		return false
	}
	parsed, err := strconv.ParseInt(start, 10, 64)
	return err == nil && parsed == expected
}

func setFirstError(mu *sync.Mutex, target *error, cancel context.CancelFunc, err error) {
	mu.Lock()
	defer mu.Unlock()
	if *target == nil {
		*target = err
		cancel()
	}
}

type progressTracker struct {
	mu        sync.Mutex
	completed []int64
	totals    []int64
	total     int64
}

func newProgressTracker(files []csghub.RepoFile) *progressTracker {
	tracker := &progressTracker{completed: make([]int64, len(files)), totals: make([]int64, len(files))}
	for index, file := range files {
		if file.Size > 0 {
			tracker.totals[index] = file.Size
			tracker.total += file.Size
		}
	}
	return tracker
}

func (t *progressTracker) update(index int, completed, total int64) (int64, int64) {
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
