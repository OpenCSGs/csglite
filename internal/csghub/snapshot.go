package csghub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/internal/ggufpick"
)

// SnapshotProgress reports progress for a multi-file download.
type SnapshotProgress struct {
	FileName          string
	FileIndex         int
	TotalFiles        int
	BytesCompleted    int64
	BytesTotal        int64
	BytesCompletedAll int64
	BytesTotalAll     int64
}

// SnapshotProgressFunc is called for each file progress update.
type SnapshotProgressFunc func(SnapshotProgress)

// SnapshotDownload downloads all files in a model repository, similar to
// pycsghub's snapshot_download.
func (c *Client) SnapshotDownload(ctx context.Context, namespace, name, destDir string, quant string, progress SnapshotProgressFunc) ([]RepoFile, error) {
	files, err := c.GetModelTree(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("fetching file tree: %w", err)
	}

	return c.downloadSnapshot(ctx, "models", namespace, name, destDir, quant, files, progress)
}

const maxConcurrentDownloads = 3

// DatasetSnapshotDownload downloads all files in a dataset repository.
// It uses the /csg/ endpoints which work without authentication for public datasets.
// Up to 3 files are downloaded concurrently.
func (c *Client) DatasetSnapshotDownload(ctx context.Context, namespace, name, destDir string, progress SnapshotProgressFunc) ([]RepoFile, error) {
	files, err := c.GetDatasetTree(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("fetching file tree: %w", err)
	}

	var downloadFiles []RepoFile
	for _, f := range files {
		if f.Type == "file" {
			downloadFiles = append(downloadFiles, f)
		}
	}

	if len(downloadFiles) == 0 {
		return nil, fmt.Errorf("no files found in dataset %s/%s", namespace, name)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, maxConcurrentDownloads)
	var mu sync.Mutex
	var firstErr error
	progressTracker := newSnapshotProgressTracker(downloadFiles)

	var wg sync.WaitGroup
	for i, f := range downloadFiles {
		wg.Add(1)
		go func(idx int, file RepoFile) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			mu.Lock()
			if firstErr != nil {
				mu.Unlock()
				return
			}
			mu.Unlock()

			destPath := filepath.Join(destDir, file.Path)

			fileProgress := func(downloaded, total int64) {
				if progress != nil {
					completedAll, totalAll := progressTracker.update(idx, downloaded, total)
					progress(SnapshotProgress{
						FileName:          file.Name,
						FileIndex:         idx,
						TotalFiles:        len(downloadFiles),
						BytesCompleted:    downloaded,
						BytesTotal:        total,
						BytesCompletedAll: completedAll,
						BytesTotalAll:     totalAll,
					})
				}
			}

			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("creating directory for %s: %w", file.Path, err)
					cancel()
				}
				mu.Unlock()
				return
			}

			var existingSize int64
			if info, err := os.Stat(destPath); err == nil {
				existingSize = info.Size()
			}

			downloadURL := fmt.Sprintf("%s/csg/datasets/%s/%s/resolve/main/%s",
				c.baseURL, namespace, name, file.Path)

			if err := c.downloadFromURL(ctx, downloadURL, destPath, existingSize, 0, fileProgress); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("downloading %s: %w", file.Path, err)
					cancel()
				}
				mu.Unlock()
			}
		}(i, f)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return downloadFiles, nil
}

func (c *Client) downloadSnapshot(ctx context.Context, repoType, namespace, name, destDir, quant string, files []RepoFile, progress SnapshotProgressFunc) ([]RepoFile, error) {
	var downloadFiles []RepoFile
	for _, f := range files {
		if f.Type == "file" {
			downloadFiles = append(downloadFiles, f)
		}
	}

	if repoType == "models" {
		var err error
		downloadFiles, err = filterGGUFMultiQuantDownload(downloadFiles, quant)
		if err != nil {
			return nil, err
		}
		downloadFiles = filterTransformersWeightDownload(downloadFiles)
	}

	if len(downloadFiles) == 0 {
		return nil, fmt.Errorf("no files found in %s/%s", namespace, name)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, maxConcurrentDownloads)
	var mu sync.Mutex
	var firstErr error
	progressTracker := newSnapshotProgressTracker(downloadFiles)

	var wg sync.WaitGroup
	for i, f := range downloadFiles {
		wg.Add(1)
		go func(idx int, file RepoFile) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			mu.Lock()
			if firstErr != nil {
				mu.Unlock()
				return
			}
			mu.Unlock()

			destPath := filepath.Join(destDir, file.Path)

			fileProgress := func(downloaded, total int64) {
				if progress != nil {
					completedAll, totalAll := progressTracker.update(idx, downloaded, total)
					progress(SnapshotProgress{
						FileName:          file.Name,
						FileIndex:         idx,
						TotalFiles:        len(downloadFiles),
						BytesCompleted:    downloaded,
						BytesTotal:        total,
						BytesCompletedAll: completedAll,
						BytesTotalAll:     totalAll,
					})
				}
			}

			if err := c.DownloadRepoFile(ctx, repoType, namespace, name, file.Path, destPath, file.LFS, file.Size, file.LFSSHA256, fileProgress); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("downloading %s: %w", file.Path, err)
					cancel()
				}
				mu.Unlock()
			}
		}(i, f)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return downloadFiles, nil
}

type snapshotProgressTracker struct {
	mu        sync.Mutex
	completed []int64
	totals    []int64
	totalAll  int64
}

func newSnapshotProgressTracker(files []RepoFile) *snapshotProgressTracker {
	tracker := &snapshotProgressTracker{
		completed: make([]int64, len(files)),
		totals:    make([]int64, len(files)),
	}
	for i, file := range files {
		if file.Size > 0 {
			tracker.totals[i] = file.Size
			tracker.totalAll += file.Size
		}
	}
	return tracker
}

func (t *snapshotProgressTracker) update(index int, completed, total int64) (int64, int64) {
	if t == nil || index < 0 || index >= len(t.completed) {
		return 0, 0
	}
	if completed < 0 {
		completed = 0
	}
	if total < 0 {
		total = 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if total > 0 && total != t.totals[index] {
		t.totalAll += total - t.totals[index]
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
	if t.totalAll > 0 && completedAll > t.totalAll {
		completedAll = t.totalAll
	}
	return completedAll, t.totalAll
}

func repoFileBaseName(f RepoFile) string {
	if f.Name != "" {
		return f.Name
	}
	return filepath.Base(f.Path)
}

func distinctQuantLabels(entries []ggufpick.FileEntry) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, e := range entries {
		p := e.Path
		if p == "" {
			p = e.Name
		}
		if l := ggufpick.QuantLabelFromRepoPath(p); l != "" {
			if _, ok := seen[l]; !ok {
				seen[l] = struct{}{}
				out = append(out, l)
			}
		}
	}
	sort.Strings(out)
	return out
}

func entriesHaveQuantLabels(entries []ggufpick.FileEntry) bool {
	for _, e := range entries {
		p := e.Path
		if p == "" {
			p = e.Name
		}
		if ggufpick.QuantLabelFromRepoPath(p) != "" {
			return true
		}
	}
	return false
}

func filterGGUFMultiQuantDownload(files []RepoFile, quant string) ([]RepoFile, error) {
	var weights []RepoFile
	for _, f := range files {
		if ggufpick.IsWeightGGUF(repoFileBaseName(f)) {
			weights = append(weights, f)
		}
	}
	if len(weights) <= 1 {
		return files, nil
	}
	entries := make([]ggufpick.FileEntry, len(weights))
	for i, f := range weights {
		entries[i] = ggufpick.FileEntry{Path: f.Path, Name: repoFileBaseName(f), Size: f.Size}
	}

	quant = strings.TrimSpace(quant)
	var filtered []ggufpick.FileEntry
	if quant != "" {
		filtered = ggufpick.FilterWeightGGUFFilesByQuant(entries, quant)
		if len(filtered) == 0 {
			if !entriesHaveQuantLabels(entries) {
				filtered = ggufpick.FilterWeightGGUFFiles(entries)
			} else {
				return nil, fmt.Errorf("no GGUF weight files match quantization %q (available: %s)",
					quant, strings.Join(distinctQuantLabels(entries), ", "))
			}
		}
	} else {
		filtered = ggufpick.FilterWeightGGUFFiles(entries)
	}

	kept := make(map[string]struct{}, len(filtered))
	for _, e := range filtered {
		kept[e.Path] = struct{}{}
	}
	out := make([]RepoFile, 0, len(files)-len(weights)+len(filtered))
	for _, f := range files {
		if !ggufpick.IsWeightGGUF(repoFileBaseName(f)) {
			out = append(out, f)
			continue
		}
		if _, ok := kept[f.Path]; ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func filterTransformersWeightDownload(files []RepoFile) []RepoFile {
	if repoHasGGUFWeight(files) {
		return files
	}
	hasSafeTensors := false
	hasPreferredSafeTensors := false
	hasPyTorch := false
	hasRedundantTransformersWeight := false
	for _, f := range files {
		path := strings.ToLower(f.Path)
		name := strings.ToLower(repoFileBaseName(f))
		if strings.HasSuffix(path, ".safetensors") || strings.HasSuffix(name, ".safetensors") {
			hasSafeTensors = true
			if !isFP32SafeTensorsWeight(path, name) {
				hasPreferredSafeTensors = true
			}
		}
		if isPreferredPyTorchWeight(path, name) {
			hasPyTorch = true
		}
		if isRedundantTransformersWeight(path, name) {
			hasRedundantTransformersWeight = true
		}
	}
	if !hasSafeTensors && !hasPyTorch && !hasRedundantTransformersWeight {
		return files
	}

	out := make([]RepoFile, 0, len(files))
	for _, f := range files {
		path := strings.ToLower(f.Path)
		name := strings.ToLower(repoFileBaseName(f))
		if hasSafeTensors {
			if hasPreferredSafeTensors && isFP32SafeTensorsWeight(path, name) {
				continue
			}
			if isBinFile(path, name) {
				continue
			}
			if isNonSafeTensorsTransformersWeight(path, name) {
				continue
			}
			out = append(out, f)
			continue
		}
		if isRedundantTransformersWeight(path, name) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func repoHasGGUFWeight(files []RepoFile) bool {
	for _, f := range files {
		if ggufpick.IsWeightGGUF(repoFileBaseName(f)) {
			return true
		}
	}
	return false
}

func isPreferredPyTorchWeight(path, name string) bool {
	return name == "pytorch_model.bin" ||
		strings.HasPrefix(name, "pytorch_model-") && strings.HasSuffix(name, ".bin") ||
		strings.HasSuffix(path, "/pytorch_model.bin") ||
		strings.Contains(path, "/pytorch_model-") && strings.HasSuffix(path, ".bin")
}

func isNonSafeTensorsTransformersWeight(path, name string) bool {
	return isPreferredPyTorchWeight(path, name) ||
		isRedundantTransformersWeight(path, name)
}

func isBinFile(path, name string) bool {
	return strings.HasSuffix(name, ".bin") || strings.HasSuffix(path, ".bin")
}

func isRedundantTransformersWeight(path, name string) bool {
	return name == "flax_model.msgpack" ||
		name == "tf_model.h5" ||
		name == "model.ckpt.index" ||
		isFP32PyTorchWeight(path, name)
}

func isFP32PyTorchWeight(path, name string) bool {
	return (strings.HasPrefix(name, "pytorch") && strings.Contains(name, ".fp32") && strings.HasSuffix(name, ".bin")) ||
		strings.Contains(path, "/pytorch") && strings.Contains(path, ".fp32") && strings.HasSuffix(path, ".bin")
}

func isFP32SafeTensorsWeight(path, name string) bool {
	return strings.Contains(name, ".fp32") && strings.HasSuffix(name, ".safetensors") ||
		strings.Contains(path, ".fp32") && strings.HasSuffix(path, ".safetensors")
}

// ParseModelID splits a model identifier like "namespace/name" into parts.
func ParseModelID(modelID string) (namespace, name string, err error) {
	return ParseRepoID(modelID)
}

// ParseRepoID splits a repository identifier like "namespace/name" into parts.
func ParseRepoID(id string) (namespace, name string, err error) {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid ID %q: expected format namespace/name", id)
	}
	return parts[0], parts[1], nil
}
