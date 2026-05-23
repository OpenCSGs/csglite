package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const uploadProgressLogInterval = 5 * time.Second

// POST /api/models/upload -- import a local model archive, folder, or file set
func (s *Server) handleModelUpload(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
		return
	}
	log.Printf("MODEL UPLOAD: stream started content_length=%d", r.ContentLength)

	stagingParent := s.cfg.TempDir()
	if err := os.MkdirAll(stagingParent, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "creating upload staging parent: "+err.Error())
		return
	}
	staging, err := os.MkdirTemp(stagingParent, ".csghub-model-upload-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "creating upload staging dir: "+err.Error())
		return
	}
	defer os.RemoveAll(staging)

	source := filepath.Join(staging, "files")
	if err := os.MkdirAll(source, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "creating upload dir: "+err.Error())
		return
	}

	mode := "files"
	overwrite := false
	modelID := ""
	paths := []string{}
	fileCount := 0
	var totalBytes int64
	archivePath := ""
	kind := model.ImportSourceDirectory
	partIndex := 0

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("MODEL UPLOAD %s: stream read failed after %s: %v", uploadLogModelID(modelID), time.Since(start).Round(time.Millisecond), err)
			writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
			return
		}
		partIndex++
		partName := part.FormName()

		switch partName {
		case "model":
			modelID = strings.TrimSpace(readUploadField(part))
			log.Printf("MODEL UPLOAD %s: part=%d field=model value=%q elapsed=%s", uploadLogModelID(modelID), partIndex, modelID, time.Since(start).Round(time.Millisecond))
		case "mode":
			if value := strings.ToLower(strings.TrimSpace(readUploadField(part))); value != "" {
				mode = value
			}
			log.Printf("MODEL UPLOAD %s: part=%d field=mode value=%q elapsed=%s", uploadLogModelID(modelID), partIndex, mode, time.Since(start).Round(time.Millisecond))
		case "overwrite":
			overwrite = parseUploadBool(readUploadField(part))
			log.Printf("MODEL UPLOAD %s: part=%d field=overwrite value=%t elapsed=%s", uploadLogModelID(modelID), partIndex, overwrite, time.Since(start).Round(time.Millisecond))
		case "paths":
			value := readUploadField(part)
			paths = append(paths, value)
			log.Printf("MODEL UPLOAD %s: part=%d field=paths index=%d value=%q elapsed=%s", uploadLogModelID(modelID), partIndex, len(paths)-1, value, time.Since(start).Round(time.Millisecond))
		case "files":
			if mode == "archive" {
				if fileCount > 0 {
					writeError(w, http.StatusBadRequest, "archive upload requires exactly one file")
					return
				}
				relPath := safeUploadFileName(part.FileName())
				if relPath == "" {
					relPath = "model"
				}
				archivePath = filepath.Join(staging, relPath)
				if modelID == "" {
					modelID = deriveUploadModelID(part.FileName())
				}
				log.Printf("MODEL UPLOAD %s: part=%d file started index=%d mode=archive filename=%q target=%q elapsed=%s", uploadLogModelID(modelID), partIndex, fileCount, part.FileName(), archivePath, time.Since(start).Round(time.Millisecond))
				n, err := saveUploadPart(part, archivePath, uploadLogModelID(modelID), fileCount, part.FileName())
				if err != nil {
					log.Printf("MODEL UPLOAD %s: archive stream failed after %s: %v", uploadLogModelID(modelID), time.Since(start).Round(time.Millisecond), err)
					writeError(w, http.StatusInternalServerError, "saving uploaded archive: "+err.Error())
					return
				}
				log.Printf("MODEL UPLOAD %s: part=%d file complete index=%d filename=%q bytes=%d elapsed=%s", uploadLogModelID(modelID), partIndex, fileCount, part.FileName(), n, time.Since(start).Round(time.Millisecond))
				totalBytes += n
				kind = model.ImportSourceArchive
			} else if mode == "directory" || mode == "files" {
				relPath := ""
				if fileCount < len(paths) {
					relPath = paths[fileCount]
				}
				if relPath == "" {
					relPath = part.FileName()
				}
				cleanRel, err := cleanUploadRelativePath(relPath)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				if modelID == "" {
					modelID = deriveUploadModelID(part.FileName())
				}
				target := filepath.Join(source, filepath.FromSlash(cleanRel))
				log.Printf("MODEL UPLOAD %s: part=%d file started index=%d mode=%s filename=%q rel=%q target=%q elapsed=%s", uploadLogModelID(modelID), partIndex, fileCount, mode, part.FileName(), cleanRel, target, time.Since(start).Round(time.Millisecond))
				n, err := saveUploadPart(part, target, uploadLogModelID(modelID), fileCount, cleanRel)
				if err != nil {
					log.Printf("MODEL UPLOAD %s: file stream failed after %s file=%q: %v", uploadLogModelID(modelID), time.Since(start).Round(time.Millisecond), relPath, err)
					writeError(w, http.StatusInternalServerError, "saving uploaded file: "+err.Error())
					return
				}
				log.Printf("MODEL UPLOAD %s: part=%d file complete index=%d rel=%q bytes=%d elapsed=%s", uploadLogModelID(modelID), partIndex, fileCount, cleanRel, n, time.Since(start).Round(time.Millisecond))
				totalBytes += n
			} else {
				writeError(w, http.StatusBadRequest, "unsupported upload mode")
				return
			}
			fileCount++
		default:
			log.Printf("MODEL UPLOAD %s: part=%d ignored field=%q elapsed=%s", uploadLogModelID(modelID), partIndex, partName, time.Since(start).Round(time.Millisecond))
			_, _ = io.Copy(io.Discard, part)
		}
	}
	if fileCount == 0 {
		writeError(w, http.StatusBadRequest, "at least one file is required")
		return
	}
	if mode == "archive" {
		source = archivePath
	}
	log.Printf("MODEL UPLOAD %s: stream complete mode=%s overwrite=%t files=%d size=%d elapsed=%s", uploadLogModelID(modelID), mode, overwrite, fileCount, totalBytes, time.Since(start).Round(time.Millisecond))

	importStart := time.Now()
	log.Printf("MODEL UPLOAD %s: import started", modelID)
	opts := model.ImportOptions{
		ModelID:   modelID,
		Source:    source,
		Kind:      kind,
		Overwrite: overwrite,
	}
	var lm *model.LocalModel
	if kind == model.ImportSourceDirectory {
		lm, err = s.manager.ImportPreparedDirectory(opts)
	} else {
		lm, err = s.manager.Import(opts)
	}
	if err != nil {
		log.Printf("MODEL UPLOAD %s: import failed after %s: %v", modelID, time.Since(importStart).Round(time.Millisecond), err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("MODEL UPLOAD %s: import complete elapsed=%s total=%s", modelID, time.Since(importStart).Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
	lm, err = s.manager.GetWithFileEntries(lm.FullName())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, api.ModelUploadResponse{
		Status:  "success",
		Model:   lm.FullName(),
		Details: s.localModelInfo(lm),
		Files:   s.modelFileEntries(lm),
	})
}

func (s *Server) modelFileEntries(lm *model.LocalModel) []api.ModelFileEntry {
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
	return files
}

func saveUploadPart(src io.Reader, target, modelID string, fileIndex int, displayPath string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer dst.Close()
	progress := &uploadProgressWriter{
		writer:      dst,
		modelID:     modelID,
		fileIndex:   fileIndex,
		displayPath: displayPath,
		startedAt:   time.Now(),
		lastLogAt:   time.Now(),
	}
	n, err := io.Copy(progress, src)
	if err != nil {
		return n, err
	}
	return n, dst.Close()
}

type uploadProgressWriter struct {
	writer      io.Writer
	modelID     string
	fileIndex   int
	displayPath string
	startedAt   time.Time
	lastLogAt   time.Time
	written     int64
}

func (w *uploadProgressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.written += int64(n)
	now := time.Now()
	if now.Sub(w.lastLogAt) >= uploadProgressLogInterval {
		w.lastLogAt = now
		log.Printf("MODEL UPLOAD %s: file progress index=%d path=%q bytes=%d elapsed=%s", w.modelID, w.fileIndex, w.displayPath, w.written, now.Sub(w.startedAt).Round(time.Millisecond))
	}
	return n, err
}

func readUploadField(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return ""
	}
	return string(data)
}

func uploadLogModelID(modelID string) string {
	if strings.TrimSpace(modelID) == "" {
		return "(pending)"
	}
	return modelID
}

func firstFormValue(values map[string][]string, key string) string {
	if list := values[key]; len(list) > 0 {
		return list[0]
	}
	return ""
}

func parseUploadBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func cleanUploadRelativePath(raw string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == "/" || cleaned == "" || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid upload path %q", raw)
	}
	return strings.TrimPrefix(cleaned, "./"), nil
}

func safeUploadFileName(raw string) string {
	name := filepath.Base(strings.TrimSpace(raw))
	name = strings.TrimSpace(name)
	if name == "." || name == string(os.PathSeparator) {
		return ""
	}
	return name
}

func deriveUploadModelID(filename string) string {
	name := safeUploadFileName(filename)
	for _, suffix := range []string{".tar.gz", ".tgz", ".zip", ".tar", ".gguf", ".safetensors", ".bin"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			name = name[:len(name)-len(suffix)]
			break
		}
	}
	name = strings.Trim(name, " ._-")
	if name == "" {
		name = "uploaded-model"
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	cleaned := strings.Trim(b.String(), "-._")
	if cleaned == "" {
		cleaned = "uploaded-model"
	}
	return "local/" + cleaned
}
