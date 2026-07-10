package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

type modelUploadSession struct {
	mu        sync.Mutex
	ID        string
	ModelID   string
	Mode      string
	Overwrite bool
	Root      string
	Source    string
	FileCount int
	Bytes     int64
	CreatedAt time.Time
	Completed map[string]bool
	Pending   map[string]int64
}

var modelUploadSessions = struct {
	sync.Mutex
	byID map[string]*modelUploadSession
}{byID: map[string]*modelUploadSession{}}

func (s *Server) handleModelUploadStart(w http.ResponseWriter, r *http.Request) {
	var req api.ModelUploadStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "files"
	}
	if mode != "archive" && mode != "directory" && mode != "files" {
		writeError(w, http.StatusBadRequest, "unsupported upload mode")
		return
	}
	uploadID, err := newUploadID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "creating upload id: "+err.Error())
		return
	}
	tmpDir := s.cfg.TempDir()
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "creating upload tmp dir: "+err.Error())
		return
	}
	root, err := os.MkdirTemp(tmpDir, ".csghub-model-upload-"+uploadID+"-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "creating upload staging dir: "+err.Error())
		return
	}
	sess := &modelUploadSession{
		ID:        uploadID,
		ModelID:   strings.TrimSpace(req.Model),
		Mode:      mode,
		Overwrite: req.Overwrite,
		Root:      root,
		Source:    filepath.Join(root, "files"),
		CreatedAt: time.Now(),
		Completed: map[string]bool{},
		Pending:   map[string]int64{},
	}
	if err := os.MkdirAll(sess.Source, 0o755); err != nil {
		_ = os.RemoveAll(root)
		writeError(w, http.StatusInternalServerError, "creating upload files dir: "+err.Error())
		return
	}
	modelUploadSessions.Lock()
	modelUploadSessions.byID[uploadID] = sess
	modelUploadSessions.Unlock()
	log.Printf("MODEL UPLOAD %s: session started mode=%s overwrite=%t root=%q", uploadID, mode, req.Overwrite, root)
	writeJSON(w, http.StatusOK, api.ModelUploadStartResponse{UploadID: uploadID})
}

func (s *Server) handleModelUploadFile(w http.ResponseWriter, r *http.Request) {
	sess := getModelUploadSession(r.PathValue("uploadID"))
	if sess == nil {
		writeError(w, http.StatusNotFound, "upload session not found")
		return
	}
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	fileName := strings.TrimSpace(r.URL.Query().Get("filename"))
	if relPath == "" {
		relPath = fileName
	}
	if fileName == "" {
		fileName = filepath.Base(relPath)
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	index := sess.FileCount
	if sess.ModelID == "" {
		sess.ModelID = deriveUploadModelID(fileName)
	}
	var target, pathKey string
	if sess.Mode == "archive" {
		if sess.FileCount > 0 {
			writeError(w, http.StatusBadRequest, "archive upload requires exactly one file")
			return
		}
		name := safeUploadFileName(fileName)
		if name == "" {
			name = "model"
		}
		target = filepath.Join(sess.Root, name)
		pathKey = name
		sess.Source = target
	} else {
		cleanRel, err := cleanUploadRelativePath(relPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		target = filepath.Join(sess.Source, filepath.FromSlash(cleanRel))
		pathKey = cleanRel
	}
	contentRange, chunked, err := parseUploadContentRange(r.Header.Get("Content-Range"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if sess.Completed[pathKey] {
		if chunked {
			currentSize, sizeErr := uploadTargetSize(target)
			if sizeErr != nil {
				writeError(w, http.StatusInternalServerError, "checking uploaded file: "+sizeErr.Error())
				return
			}
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error":           "file upload is already complete",
				"errorCode":       http.StatusConflict,
				"expected_offset": currentSize,
			})
			return
		}
		writeError(w, http.StatusConflict, "file upload is already complete")
		return
	}
	if chunked {
		s.handleModelUploadChunk(w, r, sess, index, relPath, pathKey, target, contentRange)
		return
	}
	start := time.Now()
	log.Printf("MODEL UPLOAD %s: raw file started index=%d model=%q path=%q size=%d target=%q", sess.ID, index, sess.ModelID, relPath, r.ContentLength, target)
	n, err := saveUploadPart(r.Body, target, uploadLogModelID(sess.ModelID), index, relPath)
	if err != nil {
		log.Printf("MODEL UPLOAD %s: raw file failed index=%d path=%q bytes=%d elapsed=%s: %v", sess.ID, index, relPath, n, time.Since(start).Round(time.Millisecond), err)
		writeError(w, http.StatusInternalServerError, "saving uploaded file: "+err.Error())
		return
	}
	sess.FileCount++
	sess.Bytes += n
	sess.Completed[pathKey] = true
	log.Printf("MODEL UPLOAD %s: raw file complete index=%d path=%q bytes=%d elapsed=%s total_bytes=%d", sess.ID, index, relPath, n, time.Since(start).Round(time.Millisecond), sess.Bytes)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "bytes": n, "next_offset": n, "complete": true})
}

type uploadContentRange struct {
	start int64
	end   int64
	total int64
}

func (r uploadContentRange) size() int64 {
	return r.end - r.start + 1
}

func (r uploadContentRange) complete() bool {
	return r.end+1 == r.total
}

func parseUploadContentRange(raw string) (uploadContentRange, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uploadContentRange{}, false, nil
	}
	if !strings.HasPrefix(strings.ToLower(raw), "bytes ") {
		return uploadContentRange{}, true, fmt.Errorf("invalid Content-Range %q", raw)
	}
	value := strings.TrimSpace(raw[len("bytes "):])
	rangeAndTotal := strings.Split(value, "/")
	if len(rangeAndTotal) != 2 {
		return uploadContentRange{}, true, fmt.Errorf("invalid Content-Range %q", raw)
	}
	bounds := strings.Split(rangeAndTotal[0], "-")
	if len(bounds) != 2 {
		return uploadContentRange{}, true, fmt.Errorf("invalid Content-Range %q", raw)
	}
	start, err := strconv.ParseInt(bounds[0], 10, 64)
	if err != nil {
		return uploadContentRange{}, true, fmt.Errorf("invalid Content-Range %q", raw)
	}
	end, err := strconv.ParseInt(bounds[1], 10, 64)
	if err != nil {
		return uploadContentRange{}, true, fmt.Errorf("invalid Content-Range %q", raw)
	}
	total, err := strconv.ParseInt(rangeAndTotal[1], 10, 64)
	if err != nil || start < 0 || end < start || total <= 0 || end >= total {
		return uploadContentRange{}, true, fmt.Errorf("invalid Content-Range %q", raw)
	}
	return uploadContentRange{start: start, end: end, total: total}, true, nil
}

func (s *Server) handleModelUploadChunk(
	w http.ResponseWriter,
	r *http.Request,
	sess *modelUploadSession,
	index int,
	relPath, pathKey, target string,
	contentRange uploadContentRange,
) {
	if total, ok := sess.Pending[pathKey]; ok && total != contentRange.total {
		writeError(w, http.StatusConflict, "upload total size changed")
		return
	}
	currentSize, err := uploadTargetSize(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "checking uploaded file: "+err.Error())
		return
	}
	if currentSize != contentRange.start {
		writeJSON(w, http.StatusConflict, map[string]interface{}{
			"error":           "upload offset does not match the stored file",
			"errorCode":       http.StatusConflict,
			"expected_offset": currentSize,
		})
		return
	}
	if r.ContentLength >= 0 && r.ContentLength != contentRange.size() {
		writeError(w, http.StatusBadRequest, "Content-Range size does not match request body")
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "creating upload directory: "+err.Error())
		return
	}
	chunkFile, err := os.CreateTemp(sess.Root, ".upload-chunk-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "creating upload chunk: "+err.Error())
		return
	}
	chunkPath := chunkFile.Name()
	if err := chunkFile.Close(); err != nil {
		_ = os.Remove(chunkPath)
		writeError(w, http.StatusInternalServerError, "creating upload chunk: "+err.Error())
		return
	}
	defer os.Remove(chunkPath)

	start := time.Now()
	log.Printf("MODEL UPLOAD %s: chunk started index=%d model=%q path=%q range=%d-%d/%d", sess.ID, index, sess.ModelID, relPath, contentRange.start, contentRange.end, contentRange.total)
	n, err := saveUploadPart(io.LimitReader(r.Body, contentRange.size()+1), chunkPath, uploadLogModelID(sess.ModelID), index, relPath)
	if err != nil {
		log.Printf("MODEL UPLOAD %s: chunk stream failed index=%d path=%q bytes=%d elapsed=%s: %v", sess.ID, index, relPath, n, time.Since(start).Round(time.Millisecond), err)
		writeError(w, http.StatusInternalServerError, "saving upload chunk: "+err.Error())
		return
	}
	if n != contentRange.size() {
		writeError(w, http.StatusBadRequest, "Content-Range size does not match request body")
		return
	}
	if err := appendUploadChunk(chunkPath, target, contentRange.start); err != nil {
		writeError(w, http.StatusInternalServerError, "committing upload chunk: "+err.Error())
		return
	}

	sess.Bytes += n
	if contentRange.complete() {
		sess.FileCount++
		sess.Completed[pathKey] = true
		delete(sess.Pending, pathKey)
	} else {
		sess.Pending[pathKey] = contentRange.total
	}
	log.Printf("MODEL UPLOAD %s: chunk complete index=%d path=%q bytes=%d next_offset=%d complete=%t elapsed=%s total_bytes=%d", sess.ID, index, relPath, n, contentRange.end+1, contentRange.complete(), time.Since(start).Round(time.Millisecond), sess.Bytes)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"bytes":       n,
		"next_offset": contentRange.end + 1,
		"complete":    contentRange.complete(),
	})
}

func uploadTargetSize(target string) (int64, error) {
	info, err := os.Stat(target)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("upload target is not a regular file")
	}
	return info.Size(), nil
}

func appendUploadChunk(chunkPath, target string, offset int64) (retErr error) {
	currentSize, err := uploadTargetSize(target)
	if err != nil {
		return err
	}
	if currentSize != offset {
		return fmt.Errorf("upload offset changed: got %d, want %d", currentSize, offset)
	}
	src, err := os.Open(chunkPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dst.Close(); retErr == nil {
			retErr = closeErr
		}
		if retErr != nil {
			_ = os.Truncate(target, offset)
		}
	}()
	if _, err := dst.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

func (s *Server) handleModelUploadComplete(w http.ResponseWriter, r *http.Request) {
	uploadID := r.PathValue("uploadID")
	sess := getModelUploadSession(uploadID)
	if sess == nil {
		writeError(w, http.StatusNotFound, "upload session not found")
		return
	}

	sess.mu.Lock()
	if sess.FileCount == 0 {
		sess.mu.Unlock()
		writeError(w, http.StatusBadRequest, "at least one file is required")
		return
	}
	if len(sess.Pending) > 0 {
		sess.mu.Unlock()
		writeError(w, http.StatusBadRequest, "all upload chunks must be completed before import")
		return
	}
	opts := model.ImportOptions{
		ModelID:   sess.ModelID,
		Source:    sess.Source,
		Kind:      model.ImportSourceDirectory,
		Overwrite: sess.Overwrite,
	}
	if sess.Mode == "archive" {
		opts.Kind = model.ImportSourceArchive
	}
	sess.mu.Unlock()
	defer cleanupModelUploadSession(uploadID)

	start := time.Now()
	log.Printf("MODEL UPLOAD %s: complete import started model=%q mode=%s files=%d bytes=%d", uploadID, opts.ModelID, sess.Mode, sess.FileCount, sess.Bytes)
	var (
		lm  *model.LocalModel
		err error
	)
	if opts.Kind == model.ImportSourceDirectory {
		lm, err = s.manager.ImportPreparedDirectory(opts)
	} else {
		lm, err = s.manager.Import(opts)
	}
	if err != nil {
		log.Printf("MODEL UPLOAD %s: complete import failed elapsed=%s: %v", uploadID, time.Since(start).Round(time.Millisecond), err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lm, err = s.manager.GetWithFileEntries(lm.FullName())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("MODEL UPLOAD %s: complete import done model=%s elapsed=%s", uploadID, lm.FullName(), time.Since(start).Round(time.Millisecond))
	writeJSON(w, http.StatusOK, api.ModelUploadResponse{
		Status:  "success",
		Model:   lm.FullName(),
		Details: s.localModelInfo(lm),
		Files:   s.modelFileEntries(lm),
	})
}

func (s *Server) handleModelUploadCancel(w http.ResponseWriter, r *http.Request) {
	cleanupModelUploadSession(r.PathValue("uploadID"))
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func getModelUploadSession(uploadID string) *modelUploadSession {
	modelUploadSessions.Lock()
	defer modelUploadSessions.Unlock()
	return modelUploadSessions.byID[uploadID]
}

func cleanupModelUploadSession(uploadID string) {
	modelUploadSessions.Lock()
	sess := modelUploadSessions.byID[uploadID]
	delete(modelUploadSessions.byID, uploadID)
	modelUploadSessions.Unlock()
	if sess != nil {
		_ = os.RemoveAll(sess.Root)
		log.Printf("MODEL UPLOAD %s: session cleaned root=%q", uploadID, sess.Root)
	}
}

func newUploadID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
