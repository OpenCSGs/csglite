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

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
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
	quant       string
	status      string
	createdAt   time.Time
	updatedAt   time.Time
	completedAt *time.Time
	progress    api.PullResponse
	err         string
	cancel      context.CancelFunc
}

type pullJobStore struct {
	mu        sync.Mutex
	jobs      map[string]*pullJob
	activeKey map[string]string
}

func newPullJobStore() *pullJobStore {
	return &pullJobStore{
		jobs:      map[string]*pullJob{},
		activeKey: map[string]string{},
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
	job, err := s.createPullJob("model", req.Model, strings.TrimSpace(req.Quant))
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
	job, err := s.createPullJob("dataset", req.Dataset, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, pullJobResponse(job))
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
	if job.cancel != nil && job.status != pullJobSucceeded && job.status != pullJobFailed && job.status != pullJobCancelled {
		job.cancel()
		job.status = pullJobCancelled
		now := time.Now()
		job.updatedAt = now
		job.completedAt = &now
	}
	job.mu.Unlock()
	s.pullJobs.clearActive(job)
	writeJSON(w, http.StatusOK, pullJobResponse(job))
}

func (s *Server) createPullJob(kind, name, quant string) (*pullJob, error) {
	if existing := s.pullJobs.getActive(kind, name, quant); existing != nil {
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
		quant:     quant,
		status:    pullJobQueued,
		createdAt: now,
		updatedAt: now,
		progress: api.PullResponse{
			Status: fmt.Sprintf("pulling %s", name),
		},
		cancel: cancel,
	}
	s.pullJobs.add(job)
	log.Printf("PULL JOB %s: queued kind=%s name=%q", id, kind, name)
	go s.runPullJob(ctx, job)
	return job, nil
}

func (s *Server) runPullJob(ctx context.Context, job *pullJob) {
	job.setRunning()
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
		_, err = s.manager.Pull(ctx, job.name, job.quant, progress)
	case "dataset":
		_, err = s.datasetManager.Pull(ctx, job.name, progress)
	default:
		err = fmt.Errorf("unsupported pull job kind %q", job.kind)
	}

	s.pullJobs.clearActive(job)
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
	log.Printf("PULL JOB %s: failed kind=%s name=%q error=%v", job.id, job.kind, job.name, err)
}

func (s *pullJobStore) add(job *pullJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.id] = job
	s.activeKey[pullJobActiveKey(job.kind, job.name, job.quant)] = job.id
}

func (s *pullJobStore) get(id string) *pullJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

func (s *pullJobStore) getActive(kind, name, quant string) *pullJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.activeKey[pullJobActiveKey(kind, name, quant)]
	if id == "" {
		return nil
	}
	job := s.jobs[id]
	if job == nil {
		delete(s.activeKey, pullJobActiveKey(kind, name, quant))
		return nil
	}
	job.mu.Lock()
	status := job.status
	job.mu.Unlock()
	if status == pullJobQueued || status == pullJobRunning {
		return job
	}
	delete(s.activeKey, pullJobActiveKey(kind, name, quant))
	return nil
}

func (s *pullJobStore) clearActive(job *pullJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pullJobActiveKey(job.kind, job.name, job.quant)
	if s.activeKey[key] == job.id {
		delete(s.activeKey, key)
	}
}

func pullJobActiveKey(kind, name, quant string) string {
	if kind == "model" && quant != "" {
		return kind + ":" + name + "@" + quant
	}
	return kind + ":" + name
}

func pullJobResponse(job *pullJob) api.PullJobResponse {
	job.mu.Lock()
	defer job.mu.Unlock()
	return api.PullJobResponse{
		ID:          job.id,
		Status:      job.status,
		Kind:        job.kind,
		Name:        job.name,
		Quant:       job.quant,
		CreatedAt:   job.createdAt,
		UpdatedAt:   job.updatedAt,
		CompletedAt: job.completedAt,
		Progress:    job.progress,
		Error:       job.err,
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
