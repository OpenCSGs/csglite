package csghub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ProgressFunc is called during download with current and total bytes.
type ProgressFunc func(downloaded, total int64)

// DownloadFile downloads a single file from a model repository.
// For LFS files it tries the /resolve/ endpoint first, falling back to the
// Git LFS batch API if /resolve/ returns 404.
// For non-LFS files it uses the /raw/ endpoint (returns JSON with content).
func (c *Client) DownloadFile(ctx context.Context, namespace, name, filePath, destPath string, isLFS bool, size int64, lfsSHA256 string, progress ProgressFunc) error {
	return c.DownloadRepoFile(ctx, "models", namespace, name, filePath, destPath, isLFS, size, lfsSHA256, progress)
}

// DownloadRepoFile downloads a single file from any repository type (models, datasets, etc.).
func (c *Client) DownloadRepoFile(ctx context.Context, repoType, namespace, name, filePath, destPath string, isLFS bool, size int64, lfsSHA256 string, progress ProgressFunc) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if isLFS {
		return c.downloadLFSFile(ctx, repoType, namespace, name, filePath, destPath, size, lfsSHA256, progress)
	}
	return c.downloadRawFile(ctx, repoType, namespace, name, filePath, destPath, progress)
}

// downloadLFSFile tries /resolve/ first, then falls back to Git LFS batch API.
func (c *Client) downloadLFSFile(ctx context.Context, repoType, namespace, name, filePath, destPath string, totalSize int64, lfsSHA256 string, progress ProgressFunc) error {
	var existingSize int64
	if info, err := os.Stat(destPath); err == nil {
		existingSize = info.Size()
		if totalSize > 0 && existingSize > totalSize {
			if removeErr := os.Remove(destPath); removeErr != nil {
				return fmt.Errorf("removing oversized partial download %s: %w", destPath, removeErr)
			}
			existingSize = 0
		}
		if totalSize > 0 && existingSize == totalSize {
			if progress != nil {
				progress(totalSize, totalSize)
			}
			return nil
		}
	}

	downloadURL := fmt.Sprintf("%s/api/v1/%s/%s/%s/resolve/%s",
		c.baseURL, repoType, namespace, name, filePath)

	url, err := c.tryResolveURL(ctx, downloadURL)
	if err != nil && lfsSHA256 != "" {
		url, err = c.resolveLFSBatchURL(ctx, repoType, namespace, name, lfsSHA256, totalSize)
	}
	if err != nil {
		return err
	}

	return c.downloadFromURL(ctx, url, destPath, existingSize, totalSize, progress)
}

// tryResolveURL attempts a HEAD request on the /resolve/ endpoint.
// Returns the final URL (after redirects) on success.
func (c *Client) tryResolveURL(ctx context.Context, resolveURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolveURL, nil)
	if err != nil {
		return "", err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 0,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusTemporaryRedirect {
		return resp.Header.Get("Location"), nil
	}
	if resp.StatusCode == http.StatusOK {
		return resolveURL, nil
	}
	return "", fmt.Errorf("resolve returned %d", resp.StatusCode)
}

// resolveLFSBatchURL uses the Git LFS batch API to get the actual download URL.
func (c *Client) resolveLFSBatchURL(ctx context.Context, repoType, namespace, name, oid string, size int64) (string, error) {
	batchURL := fmt.Sprintf("%s/%s/%s/%s.git/info/lfs/objects/batch",
		c.baseURL, repoType, namespace, name)

	body := map[string]interface{}{
		"operation": "download",
		"transfers": []string{"basic"},
		"objects":   []map[string]interface{}{{"oid": oid, "size": size}},
	}
	payload, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("creating LFS batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("LFS batch request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("LFS batch failed %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Objects []struct {
			OID     string `json:"oid"`
			Actions struct {
				Download struct {
					Href string `json:"href"`
				} `json:"download"`
			} `json:"actions"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"objects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding LFS batch response: %w", err)
	}

	if len(result.Objects) == 0 {
		return "", fmt.Errorf("LFS batch returned no objects")
	}
	obj := result.Objects[0]
	if obj.Error != nil {
		return "", fmt.Errorf("LFS object error %d: %s", obj.Error.Code, obj.Error.Message)
	}
	if obj.Actions.Download.Href == "" {
		return "", fmt.Errorf("LFS batch returned no download URL")
	}
	return obj.Actions.Download.Href, nil
}

const maxRetries = 3

// downloadFromURL downloads a file from a direct URL with resume and retry support.
func (c *Client) downloadFromURL(ctx context.Context, url, destPath string, existingSize, totalSize int64, progress ProgressFunc) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			if info, err := os.Stat(destPath); err == nil {
				existingSize = info.Size()
			}
		}

		lastErr = c.doDownload(ctx, url, destPath, existingSize, totalSize, progress)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func (c *Client) doDownload(ctx context.Context, url, destPath string, existingSize, totalSize int64, progress ProgressFunc) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	client := c.DownloadHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		existingSize = 0
	case http.StatusPartialContent:
		// resume
	case http.StatusRequestedRangeNotSatisfiable:
		return nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download failed %d: %s", resp.StatusCode, string(body))
	}

	total := resp.ContentLength + existingSize
	if totalSize > 0 {
		total = totalSize
	}

	flags := os.O_WRONLY | os.O_CREATE
	if existingSize > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(destPath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	return copyWithProgress(ctx, f, resp.Body, existingSize, total, progress)
}

// downloadRawFile uses /raw/ endpoint which returns JSON {"msg":"OK","data":"<content>"}.
func (c *Client) downloadRawFile(ctx context.Context, repoType, namespace, name, filePath, destPath string, progress ProgressFunc) error {
	apiPath := fmt.Sprintf("/api/v1/%s/%s/%s/raw/%s", repoType, namespace, name, filePath)
	req, err := c.newRequest(ctx, http.MethodGet, apiPath, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("raw download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("raw download failed %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Msg  string `json:"msg"`
		Data string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if err := os.WriteFile(destPath, []byte(result.Data), 0o644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	if progress != nil {
		size := int64(len(result.Data))
		progress(size, size)
	}
	return nil
}

func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, startOffset, total int64, progress ProgressFunc) error {
	buf := make([]byte, 32*1024)
	downloaded := startOffset

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			if _, err := dst.Write(buf[:n]); err != nil {
				return fmt.Errorf("writing: %w", err)
			}
			downloaded += int64(n)
			if progress != nil {
				progress(downloaded, total)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				// 报告最终进度：确保完成时显示 100%
				if progress != nil && total > 0 {
					progress(total, total)
				}
				break
			}
			return fmt.Errorf("reading: %w", readErr)
		}
	}
	return nil
}
