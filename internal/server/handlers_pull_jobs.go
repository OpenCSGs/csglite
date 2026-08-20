package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/datasetregistry"
	"github.com/opencsgs/csglite/internal/modelregistry"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	maxConcurrentPullJobs = 3

	pullJobQueued    = "queued"
	pullJobRunning   = "running"
	pullJobSucceeded = "succeeded"
	pullJobFailed    = "failed"
	pullJobCancelled = "cancelled"
)

type pullJob struct {
	mu          sync.Mutex
	id          string
	kind        string
	name        string
	source      string
	revision    string
	quant       string
	quants      []string
	status      string
	createdAt   time.Time
	updatedAt   time.Time
	completedAt *time.Time
	progress    api.PullResponse
	err         string
	ctx         context.Context
	cancel      context.CancelFunc
}

type pullJobStore struct {
	mu        sync.Mutex
	jobs      map[string]*pullJob
	activeKey map[string]string
	queue     []string
}

func newPullJobStore() *pullJobStore {
	return &pullJobStore{
		jobs:      map[string]*pullJob{},
		activeKey: map[string]string{},
		queue:     []string{},
	}
}

func (s *Server) handlePullJobCreate(w http.ResponseWriter, r *http.Request) {
	var req api.PullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	source, err := modelregistry.NormalizeSource(req.ArtifactSource)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	job, err := s.createPullJob("model", req.Model, string(source), strings.TrimSpace(req.Revision), normalizePullQuants(req.Quant, req.Quants))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, pullJobResponse(job))
}

func (s *Server) handleDatasetPullJobCreate(w http.ResponseWriter, r *http.Request) {
	var req api.DatasetPullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Dataset = strings.TrimSpace(req.Dataset)
	if req.Dataset == "" {
		writeError(w, http.StatusBadRequest, "dataset is required")
		return
	}
	datasetID, source, err := remoteDatasetPullSpec(req.Dataset, req.ArtifactSource)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	job, err := s.createPullJob("dataset", datasetID, string(source), strings.TrimSpace(req.Revision), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, pullJobResponse(job))
}

func (s *Server) handlePartialDatasetPullDelete(w http.ResponseWriter, r *http.Request) {
	var req api.DatasetPullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	datasetID, source, err := remoteDatasetPullSpec(req.Dataset, req.ArtifactSource)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.pullJobs.hasActiveSourceRepo("dataset", datasetID, string(source)) {
		writeError(w, http.StatusConflict, "cancel the active download before clearing partial files")
		return
	}
	removedPath, err := s.datasetManager.RemovePartial(datasetID, string(source))
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared", "path": removedPath})
}

func (s *pullJobStore) hasActiveSourceRepo(kind, name, source string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, jobID := range s.activeKey {
		job := s.jobs[jobID]
		if job != nil && job.kind == kind && job.name == name && job.source == source {
			return true
		}
	}
	return false
}

func (s *Server) handlePullJobGet(w http.ResponseWriter, r *http.Request) {
	job := s.pullJobs.get(r.PathValue("jobID"))
	if job == nil {
		writeError(w, http.StatusNotFound, "pull job not found")
		return
	}
	writeJSON(w, http.StatusOK, pullJobResponse(job))
}

func (s *Server) handlePullJobCancel(w http.ResponseWriter, r *http.Request) {
	job := s.pullJobs.get(r.PathValue("jobID"))
	if job == nil {
		writeError(w, http.StatusNotFound, "pull job not found")
		return
	}
	job.mu.Lock()
	status := job.status
	if job.cancel != nil && job.status != pullJobSucceeded && job.status != pullJobFailed && job.status != pullJobCancelled {
		job.cancel()
		job.status = pullJobCancelled
		now := time.Now()
		job.updatedAt = now
		job.completedAt = &now
	}
	job.mu.Unlock()
	s.pullJobs.clearActive(job)
	if status == pullJobQueued {
		s.startQueuedPullJobs()
	}
	writeJSON(w, http.StatusOK, pullJobResponse(job))
}

func (s *Server) handlePartialModelPullDelete(w http.ResponseWriter, r *http.Request) {
	var req api.PullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	source, err := modelregistry.NormalizeSource(req.ArtifactSource)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.pullJobs.hasActiveModel(req.Model, string(source), strings.TrimSpace(req.Revision)) {
		writeError(w, http.StatusConflict, "cancel the active download before clearing partial files")
		return
	}
	removedPath, err := s.manager.RemovePartial(req.Model, string(source))
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "cleared",
		"path":   removedPath,
	})
}

func (s *pullJobStore) hasActiveModel(name, source, revision string) bool {
	return s.hasActive("model", name, source, revision)
}

func (s *pullJobStore) hasActive(kind, name, source, revision string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, jobID := range s.activeKey {
		job := s.jobs[jobID]
		if job != nil && job.kind == kind && job.name == name &&
			job.source == source && job.revision == revision {
			return true
		}
	}
	return false
}

func (s *Server) createPullJob(kind, name, source, revision string, quants []string) (*pullJob, error) {
	quants = normalizePullQuants("", quants)
	quant := firstPullQuant(quants)
	if existing := s.pullJobs.getActive(kind, name, source, revision, quants); existing != nil {
		return existing, nil
	}
	id, err := newPullJobID()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	job := &pullJob{
		id:        id,
		kind:      kind,
		name:      name,
		source:    source,
		revision:  revision,
		quant:     quant,
		quants:    quants,
		status:    pullJobQueued,
		createdAt: now,
		updatedAt: now,
		progress: api.PullResponse{
			Status: fmt.Sprintf("pulling %s", name),
		},
		ctx:    ctx,
		cancel: cancel,
	}
	if existing := s.pullJobs.addIfAbsent(job); existing != job {
		cancel()
		return existing, nil
	}
	log.Printf("PULL JOB %s: queued kind=%s name=%q source=%q revision=%q", id, kind, name, source, revision)
	s.startQueuedPullJobs()
	return job, nil
}

func (s *Server) startQueuedPullJobs() {
	for {
		job := s.pullJobs.claimNextQueued(maxConcurrentPullJobs)
		if job == nil {
			return
		}
		go s.runPullJob(job)
	}
}

func (s *Server) runPullJob(job *pullJob) {
	ctx := job.ctx
	log.Printf("PULL JOB %s: started kind=%s name=%q", job.id, job.kind, job.name)

	lastProgressLog := time.Time{}
	progress := func(p csghub.SnapshotProgress) {
		resp := api.PullResponse{
			Status:         fmt.Sprintf("downloading %s", p.FileName),
			Digest:         p.FileName,
			Total:          p.BytesTotal,
			Completed:      p.BytesCompleted,
			TotalBytes:     p.BytesTotalAll,
			CompletedBytes: p.BytesCompletedAll,
		}
		job.setProgress(resp)
		if time.Since(lastProgressLog) >= 5*time.Second || (p.BytesTotal > 0 && p.BytesCompleted >= p.BytesTotal) {
			log.Printf("PULL JOB %s: pulling file=%s completed=%d total=%d", job.id, p.FileName, p.BytesCompleted, p.BytesTotal)
			lastProgressLog = time.Now()
		}
	}

	var err error
	switch job.kind {
	case "model":
		_, err = s.manager.PullFrom(ctx, job.name, job.source, job.revision, job.quants, progress)
	case "dataset":
		_, err = s.datasetManager.PullFrom(ctx, job.name, job.source, job.revision, progress)
	default:
		err = fmt.Errorf("unsupported pull job kind %q", job.kind)
	}

	s.pullJobs.clearActive(job)
	defer s.startQueuedPullJobs()
	if err == nil {
		job.setSucceeded()
		log.Printf("PULL JOB %s: succeeded kind=%s name=%q", job.id, job.kind, job.name)
		return
	}
	if ctx.Err() != nil {
		job.setCancelled()
		log.Printf("PULL JOB %s: cancelled kind=%s name=%q", job.id, job.kind, job.name)
		return
	}
	job.setFailed(err)
	s.reportModelDownloadFailure(job, err)
	log.Printf("PULL JOB %s: failed kind=%s name=%q error=%v", job.id, job.kind, job.name, err)
}

type modelDownloadFailureEventExtension struct {
	ReportFrom string `json:"report_from"`
	Error      string `json:"error,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	Quant      string `json:"quant,omitempty"`
	Version    string `json:"version,omitempty"`
}

func (s *Server) reportModelDownloadFailure(job *pullJob, pullErr error) {
	if s == nil || job == nil || job.kind != "model" {
		return
	}
	source, err := modelregistry.NormalizeSource(job.source)
	if err != nil || source != modelregistry.SourceOpenCSG {
		return
	}
	modelID := strings.TrimSpace(job.name)
	if modelID == "" {
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(s.cfg.ServerURL), "/")
	if baseURL == "" {
		return
	}

	ext, err := json.Marshal(modelDownloadFailureEventExtension{
		ReportFrom: "csglite",
		Error:      errorString(pullErr),
		JobID:      job.id,
		Quant:      strings.TrimSpace(job.quant),
		Version:    strings.TrimSpace(s.version),
	})
	if err != nil {
		log.Printf("PULL JOB %s: failed to encode model download failure event: %v", job.id, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eventSvc := cloud.NewService(baseURL)
	err = eventSvc.ReportClientEvents(ctx, []cloud.ClientEvent{{
		Module:    "csghub-lite",
		ID:        "model_download_failed",
		Value:     modelID,
		Extension: string(ext),
	}})
	if err != nil {
		log.Printf("PULL JOB %s: failed to report model download failure event: %v", job.id, err)
		return
	}
	log.Printf("PULL JOB %s: reported model download failure event model=%q", job.id, modelID)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func normalizePullQuants(legacy string, values []string) []string {
	source := values
	if source == nil {
		source = []string{legacy}
	}
	seen := make(map[string]struct{}, len(source))
	out := make([]string, 0, len(source))
	for _, value := range source {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstPullQuant(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (s *pullJobStore) add(job *pullJob) {
	_ = s.addIfAbsent(job)
}

func (s *pullJobStore) addIfAbsent(job *pullJob) *pullJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pullJobActiveKey(job.kind, job.name, job.source, job.revision, job.quants)
	if id := s.activeKey[key]; id != "" {
		existing := s.jobs[id]
		if existing != nil {
			existing.mu.Lock()
			status := existing.status
			existing.mu.Unlock()
			if status == pullJobQueued || status == pullJobRunning {
				return existing
			}
		}
		delete(s.activeKey, key)
	}
	s.jobs[job.id] = job
	s.activeKey[key] = job.id
	s.queue = append(s.queue, job.id)
	return job
}

func (s *pullJobStore) get(id string) *pullJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

func (s *pullJobStore) getActive(kind, name, source, revision string, quants []string) *pullJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pullJobActiveKey(kind, name, source, revision, quants)
	id := s.activeKey[key]
	if id == "" {
		return nil
	}
	job := s.jobs[id]
	if job == nil {
		delete(s.activeKey, key)
		return nil
	}
	job.mu.Lock()
	status := job.status
	job.mu.Unlock()
	if status == pullJobQueued || status == pullJobRunning {
		return job
	}
	delete(s.activeKey, key)
	return nil
}

func (s *pullJobStore) clearActive(job *pullJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pullJobActiveKey(job.kind, job.name, job.source, job.revision, job.quants)
	if s.activeKey[key] == job.id {
		delete(s.activeKey, key)
	}
}

func (s *pullJobStore) claimNextQueued(maxRunning int) *pullJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runningCountLocked() >= maxRunning {
		return nil
	}
	for len(s.queue) > 0 {
		id := s.queue[0]
		s.queue = s.queue[1:]
		job := s.jobs[id]
		if job == nil {
			continue
		}
		job.mu.Lock()
		if job.status != pullJobQueued {
			job.mu.Unlock()
			continue
		}
		job.status = pullJobRunning
		job.updatedAt = time.Now()
		job.mu.Unlock()
		return job
	}
	return nil
}

func (s *pullJobStore) runningCountLocked() int {
	running := 0
	for _, job := range s.jobs {
		job.mu.Lock()
		if job.status == pullJobRunning {
			running++
		}
		job.mu.Unlock()
	}
	return running
}

func pullJobActiveKey(kind, name, source, revision string, quants []string) string {
	key := kind + ":" + source + ":" + name + "@" + revision
	if kind == "model" && len(quants) > 0 {
		key += "#" + strings.Join(quants, ",")
	}
	return key
}

func remoteDatasetPullSpec(datasetID, sourceValue string) (string, datasetregistry.Source, error) {
	datasetID = strings.Trim(strings.TrimSpace(datasetID), "/")
	parts := strings.Split(datasetID, "/")
	if len(parts) == 3 {
		source, err := datasetregistry.NormalizeSource(parts[0])
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(sourceValue) != "" {
			requested, normalizeErr := datasetregistry.NormalizeSource(sourceValue)
			if normalizeErr != nil {
				return "", "", normalizeErr
			}
			if requested != source {
				return "", "", fmt.Errorf("dataset ID source %q does not match artifact_source %q", source, requested)
			}
		}
		return strings.Join(parts[1:], "/"), source, nil
	}
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid dataset ID %q", datasetID)
	}
	source, err := datasetregistry.NormalizeSource(sourceValue)
	if err != nil {
		return "", "", err
	}
	return datasetID, source, nil
}

func pullJobResponse(job *pullJob) api.PullJobResponse {
	job.mu.Lock()
	defer job.mu.Unlock()
	return api.PullJobResponse{
		ID:             job.id,
		Status:         job.status,
		Kind:           job.kind,
		Name:           job.name,
		ArtifactSource: job.source,
		Revision:       job.revision,
		Quant:          job.quant,
		Quants:         append([]string(nil), job.quants...),
		CreatedAt:      job.createdAt,
		UpdatedAt:      job.updatedAt,
		CompletedAt:    job.completedAt,
		Progress:       job.progress,
		Error:          job.err,
	}
}

func (j *pullJob) setRunning() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = pullJobRunning
	j.updatedAt = time.Now()
}

func (j *pullJob) setProgress(progress api.PullResponse) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.progress = progress
	j.updatedAt = time.Now()
}

func (j *pullJob) setSucceeded() {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.status = pullJobSucceeded
	j.updatedAt = now
	j.completedAt = &now
	j.progress = api.PullResponse{Status: "success"}
}

func (j *pullJob) setFailed(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.status = pullJobFailed
	j.updatedAt = now
	j.completedAt = &now
	if err != nil {
		j.err = err.Error()
		j.progress = api.PullResponse{Status: "error: " + err.Error()}
	}
}

func (j *pullJob) setCancelled() {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.status = pullJobCancelled
	j.updatedAt = now
	j.completedAt = &now
}

func newPullJobID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
