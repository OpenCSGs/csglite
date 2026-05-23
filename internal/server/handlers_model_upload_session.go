package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
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
	var target string
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
		sess.Source = target
	} else {
		cleanRel, err := cleanUploadRelativePath(relPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		target = filepath.Join(sess.Source, filepath.FromSlash(cleanRel))
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
	log.Printf("MODEL UPLOAD %s: raw file complete index=%d path=%q bytes=%d elapsed=%s total_bytes=%d", sess.ID, index, relPath, n, time.Since(start).Round(time.Millisecond), sess.Bytes)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "bytes": n})
}

func (s *Server) handleModelUploadComplete(w http.ResponseWriter, r *http.Request) {
	uploadID := r.PathValue("uploadID")
	sess := getModelUploadSession(uploadID)
	if sess == nil {
		writeError(w, http.StatusNotFound, "upload session not found")
		return
	}
	defer cleanupModelUploadSession(uploadID)

	sess.mu.Lock()
	if sess.FileCount == 0 {
		sess.mu.Unlock()
		writeError(w, http.StatusBadRequest, "at least one file is required")
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
