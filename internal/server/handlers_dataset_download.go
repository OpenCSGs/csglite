package server

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/pkg/api"
)

// GET /api/datasets/{namespace}/{name}/manifest -- local dataset manifest with file download URLs
func (s *Server) handleDatasetManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")

	datasetID := datasetIDFromPathValues(r)
	ld, err := s.datasetManager.GetWithFileEntries(datasetID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dataset %q not found", datasetID))
		return
	}

	files := make([]api.DatasetDownloadFile, 0, len(ld.FileEntries))
	for _, entry := range ld.FileEntries {
		files = append(files, api.DatasetDownloadFile{
			Path:        entry.Path,
			Size:        entry.Size,
			SHA256:      entry.SHA256,
			LFS:         entry.LFS,
			DownloadURL: buildDatasetFileDownloadURL(ld.Namespace, ld.Name, entry.Path),
		})
	}

	writeJSON(w, http.StatusOK, api.DatasetManifestResponse{
		Details: localDatasetInfo(ld),
		Files:   files,
	})
}

// GET /api/datasets/{namespace}/{name}/files/{path...} -- download a local dataset file
func (s *Server) handleDatasetFile(w http.ResponseWriter, r *http.Request) {
	datasetID := datasetIDFromPathValues(r)
	ld, err := s.datasetManager.GetWithFileEntries(datasetID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dataset %q not found", datasetID))
		return
	}

	datasetDir, err := s.datasetManager.DatasetPath(datasetID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dataset %q not found", datasetID))
		return
	}

	relPath, absPath, err := resolveDatasetDownloadPath(datasetDir, r.PathValue("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entry, ok := findLocalDatasetFile(ld.FileEntries, relPath)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q not found in dataset %q", relPath, datasetID))
		return
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q not found in dataset %q", relPath, datasetID))
		return
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q not found in dataset %q", relPath, datasetID))
		return
	}

	file, err := os.Open(absPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("opening %q: %v", relPath, err))
		return
	}
	defer file.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	if entry.SHA256 != "" {
		w.Header().Set("ETag", fmt.Sprintf("\"sha256:%s\"", entry.SHA256))
		w.Header().Set("X-Checksum-SHA256", entry.SHA256)
	}
	if contentType := mime.TypeByExtension(filepath.Ext(absPath)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	http.ServeContent(w, r, filepath.Base(absPath), info.ModTime(), file)
}

func datasetIDFromPathValues(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("namespace")) + "/" + strings.TrimSpace(r.PathValue("name"))
}

func buildDatasetFileDownloadURL(namespace, name, relPath string) string {
	segments := strings.Split(relPath, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return fmt.Sprintf(
		"/api/datasets/%s/%s/files/%s",
		url.PathEscape(namespace),
		url.PathEscape(name),
		strings.Join(segments, "/"),
	)
}

func resolveDatasetDownloadPath(datasetDir, rawPath string) (string, string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(rawPath), "\\", "/")
	if normalized == "" {
		return "", "", fmt.Errorf("invalid path")
	}

	target := filepath.Join(datasetDir, filepath.FromSlash(normalized))
	relPath, err := filepath.Rel(datasetDir, target)
	if err != nil {
		return "", "", fmt.Errorf("invalid path")
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("invalid path")
	}

	relPath = filepath.ToSlash(relPath)
	if relPath == "." || relPath == "" || relPath == "manifest.json" {
		return "", "", fmt.Errorf("invalid path")
	}

	return relPath, filepath.Join(datasetDir, filepath.FromSlash(relPath)), nil
}

func findLocalDatasetFile(entries []dataset.LocalDatasetFile, relPath string) (dataset.LocalDatasetFile, bool) {
	for _, entry := range entries {
		if entry.Path == relPath {
			return entry, true
		}
	}
	return dataset.LocalDatasetFile{}, false
}
