package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"encoding/base64"
	"github.com/opencsgs/csghub-lite/internal/imagegen"
	"github.com/opencsgs/csghub-lite/pkg/api"
	"strings"
)

const (
	imageJobQueued    = "queued"
	imageJobRunning   = "running"
	imageJobSucceeded = "succeeded"
	imageJobFailed    = "failed"
	imageJobCancelled = "cancelled"
)

type imageGenerationJob struct {
	mu          sync.Mutex
	id          string
	status      string
	createdAt   time.Time
	updatedAt   time.Time
	completedAt *time.Time
	req         api.OpenAIImagesGenerationRequest
	result      *api.OpenAIImagesGenerationResponse
	err         string
	cancel      context.CancelFunc
}

type imageGenerationJobStore struct {
	mu   sync.Mutex
	jobs map[string]*imageGenerationJob
}

func newImageGenerationJobStore() *imageGenerationJobStore {
	return &imageGenerationJobStore{jobs: map[string]*imageGenerationJob{}}
}

func (s *Server) handleImageGenerationJobCreate(w http.ResponseWriter, r *http.Request) {
	var req api.OpenAIImagesGenerationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if errMsg := firstImageJobValidationError(&req); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	job, err := s.createImageGenerationJob(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, imageJobResponse(job))
}

func (s *Server) handleImageGenerationJobGet(w http.ResponseWriter, r *http.Request) {
	job := s.imageJobs.get(r.PathValue("jobID"))
	if job == nil {
		writeError(w, http.StatusNotFound, "image generation job not found")
		return
	}
	writeJSON(w, http.StatusOK, imageJobResponse(job))
}

func (s *Server) handleImageGenerationJobResult(w http.ResponseWriter, r *http.Request) {
	job := s.imageJobs.get(r.PathValue("jobID"))
	if job == nil {
		writeError(w, http.StatusNotFound, "image generation job not found")
		return
	}
	job.mu.Lock()
	status := job.status
	result := job.result
	errMsg := job.err
	job.mu.Unlock()
	switch status {
	case imageJobSucceeded:
		writeJSON(w, http.StatusOK, result)
	case imageJobFailed:
		writeError(w, http.StatusInternalServerError, errMsg)
	case imageJobCancelled:
		writeError(w, http.StatusConflict, "image generation job was cancelled")
	default:
		writeJSON(w, http.StatusAccepted, imageJobResponse(job))
	}
}

func (s *Server) handleImageGenerationJobCancel(w http.ResponseWriter, r *http.Request) {
	job := s.imageJobs.get(r.PathValue("jobID"))
	if job == nil {
		writeError(w, http.StatusNotFound, "image generation job not found")
		return
	}
	job.mu.Lock()
	if job.cancel != nil && job.status != imageJobSucceeded && job.status != imageJobFailed && job.status != imageJobCancelled {
		job.cancel()
		job.status = imageJobCancelled
		now := time.Now()
		job.updatedAt = now
		job.completedAt = &now
	}
	job.mu.Unlock()
	writeJSON(w, http.StatusOK, imageJobResponse(job))
}

func (s *Server) createImageGenerationJob(req api.OpenAIImagesGenerationRequest) (*imageGenerationJob, error) {
	id, err := newImageGenerationJobID()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	job := &imageGenerationJob{
		id:        id,
		status:    imageJobQueued,
		createdAt: now,
		updatedAt: now,
		req:       req,
		cancel:    cancel,
	}
	s.imageJobs.add(job)
	log.Printf("IMAGE JOB %s: queued model=%q size=%q", id, req.Model, req.Size)
	go s.runImageGenerationJob(ctx, job)
	return job, nil
}

func (s *Server) runImageGenerationJob(ctx context.Context, job *imageGenerationJob) {
	job.setRunning()
	log.Printf("IMAGE JOB %s: started model=%q", job.id, job.req.Model)
	var resp *api.OpenAIImagesGenerationResponse
	var err error
	if imageGenerationUsesCloud(job.req) {
		if strings.TrimSpace(job.req.Image) != "" || len(job.req.Images) > 0 {
			resp, err = s.generateCloudImageEdit(ctx, imageInferenceRequestFromJob(job.req))
		} else {
			resp, err = s.generateCloudImage(ctx, job.req)
		}
	} else {
		var eng imagegen.Engine
		eng, err = s.getOrLoadImageEngine(ctx, job.req.Model)
		if err == nil {
			resp, err = eng.Generate(ctx, job.req)
			if err == nil {
				s.touchImageEngine(job.req.Model)
			}
		}
	}
	if resp != nil && resp.Created == 0 {
		resp.Created = time.Now().Unix()
	}
	if err == nil {
		job.setSucceeded(resp)
		log.Printf("IMAGE JOB %s: succeeded model=%q", job.id, job.req.Model)
		return
	}
	if ctx.Err() != nil {
		job.setCancelled()
		log.Printf("IMAGE JOB %s: cancelled model=%q", job.id, job.req.Model)
		return
	}
	job.setFailed(err)
	if status, ok := imagegen.RuntimeStatusFromError(err); ok {
		log.Printf("IMAGE JOB %s: failed runtime not ready ready=%t error=%v", job.id, status.Ready, err)
		return
	}
	log.Printf("IMAGE JOB %s: failed model=%q error=%v", job.id, job.req.Model, err)
}

func (s *imageGenerationJobStore) add(job *imageGenerationJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.id] = job
}

func (s *imageGenerationJobStore) get(id string) *imageGenerationJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

func imageJobResponse(job *imageGenerationJob) api.ImageGenerationJobResponse {
	job.mu.Lock()
	defer job.mu.Unlock()
	return api.ImageGenerationJobResponse{
		ID:          job.id,
		Status:      job.status,
		CreatedAt:   job.createdAt,
		UpdatedAt:   job.updatedAt,
		CompletedAt: job.completedAt,
		Request:     job.req,
		Result:      job.result,
		Error:       job.err,
	}
}

func (j *imageGenerationJob) setRunning() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = imageJobRunning
	j.updatedAt = time.Now()
}

func (j *imageGenerationJob) setSucceeded(result *api.OpenAIImagesGenerationResponse) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.status = imageJobSucceeded
	j.updatedAt = now
	j.completedAt = &now
	j.result = result
}

func (j *imageGenerationJob) setFailed(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.status = imageJobFailed
	j.updatedAt = now
	j.completedAt = &now
	if err != nil {
		j.err = err.Error()
	}
}

func (j *imageGenerationJob) setCancelled() {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.status = imageJobCancelled
	j.updatedAt = now
	j.completedAt = &now
}

func firstImageJobValidationError(req *api.OpenAIImagesGenerationRequest) string {
	if strings.TrimSpace(req.Image) != "" || len(req.Images) > 0 {
		return normalizeOpenAIImagesEditRequest(req)
	}
	return normalizeOpenAIImagesGenerationRequest(req)
}

func imageInferenceRequestFromJob(req api.OpenAIImagesGenerationRequest) imageInferenceRequest {
	out := imageInferenceRequest{OpenAIImagesGenerationRequest: req}
	if strings.TrimSpace(req.Image) != "" {
		if data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.Image)); err == nil {
			out.images = append(out.images, data)
		}
	}
	for _, encoded := range req.Images {
		encoded = strings.TrimSpace(encoded)
		if encoded == "" {
			continue
		}
		if data, err := base64.StdEncoding.DecodeString(encoded); err == nil {
			out.images = append(out.images, data)
		}
	}
	return out
}

func newImageGenerationJobID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
