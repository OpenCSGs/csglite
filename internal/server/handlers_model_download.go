package server

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/localinference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

// GET /api/models/{namespace}/{name}/manifest -- local model manifest with file download URLs
func (s *Server) handleModelManifest(w http.ResponseWriter, r *http.Request) {
	s.handleModelManifestForID(w, modelIDFromPathValues(r))
}

// GET /api/models/{model}/manifest -- local model manifest resolved by public model ID.
func (s *Server) handleModelManifestByPublicID(w http.ResponseWriter, r *http.Request) {
	modelID, err := s.manager.ResolveLocalModelID(strings.TrimSpace(r.PathValue("model")))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.handleModelManifestForID(w, modelID)
}

func (s *Server) handleModelManifestForID(w http.ResponseWriter, modelID string) {
	w.Header().Set("Cache-Control", "no-cache")

	lm, err := s.manager.GetWithFileEntries(modelID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", modelID))
		return
	}

	files := make([]api.ModelFileEntry, 0, len(lm.FileEntries))
	for _, entry := range lm.FileEntries {
		files = append(files, api.ModelFileEntry{
			Path:        entry.Path,
			Size:        entry.Size,
			SHA256:      entry.SHA256,
			LFS:         entry.LFS,
			DownloadURL: buildModelFileDownloadURL(lm.Namespace, lm.Name, entry.Path),
		})
	}

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", modelID))
		return
	}

	writeJSON(w, http.StatusOK, api.ModelManifestResponse{
		Details:        s.localModelInfo(lm),
		Files:          files,
		LocalInference: localinference.FromLocalModel(lm, modelDir),
	})
}

// GET /api/models/{namespace}/{name}/files/{path...} -- download a local model file
func (s *Server) handleModelFile(w http.ResponseWriter, r *http.Request) {
	modelID := modelIDFromPathValues(r)
	lm, err := s.manager.GetWithFileEntries(modelID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", modelID))
		return
	}

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", modelID))
		return
	}

	relPath, absPath, err := resolveModelDownloadPath(modelDir, r.PathValue("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entry, ok := findLocalModelFile(lm.FileEntries, relPath)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q not found in model %q", relPath, modelID))
		return
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q not found in model %q", relPath, modelID))
		return
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, fmt.Sprintf("file %q not found in model %q", relPath, modelID))
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

func modelIDFromPathValues(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("namespace")) + "/" + strings.TrimSpace(r.PathValue("name"))
}

func buildModelFileDownloadURL(namespace, name, relPath string) string {
	segments := strings.Split(relPath, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return fmt.Sprintf(
		"/api/models/%s/%s/files/%s",
		url.PathEscape(namespace),
		url.PathEscape(name),
		strings.Join(segments, "/"),
	)
}

func resolveModelDownloadPath(modelDir, rawPath string) (string, string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(rawPath), "\\", "/")
	if normalized == "" {
		return "", "", fmt.Errorf("invalid path")
	}

	target := filepath.Join(modelDir, filepath.FromSlash(normalized))
	relPath, err := filepath.Rel(modelDir, target)
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

	return relPath, filepath.Join(modelDir, filepath.FromSlash(relPath)), nil
}

func findLocalModelFile(entries []model.LocalModelFile, relPath string) (model.LocalModelFile, bool) {
	for _, entry := range entries {
		if entry.Path == relPath {
			return entry, true
		}
	}
	return model.LocalModelFile{}, false
}
