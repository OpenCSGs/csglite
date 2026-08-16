package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/datasetexport"
)

const (
	datasetExportJobRetention = 24 * time.Hour
	datasetExportJobMaxStored = 256
)

type datasetExportJob struct {
	ID        string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Error     string
	Artifact  *datasetexport.Artifact
}

type datasetExportJobStore struct {
	mu      sync.RWMutex
	jobs    map[string]*datasetExportJob
	workers chan struct{}
}

func newDatasetExportJobStore() *datasetExportJobStore {
	return &datasetExportJobStore{
		jobs:    make(map[string]*datasetExportJob),
		workers: make(chan struct{}, 1),
	}
}

func (s *datasetExportJobStore) Start(run func() (datasetexport.Artifact, error)) datasetExportJob {
	now := time.Now().UTC()
	job := &datasetExportJob{
		ID:        newDatasetExportJobID(),
		Status:    "queued",
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.pruneLocked(now)
	s.mu.Unlock()
	initial := *job
	go func() {
		s.workers <- struct{}{}
		defer func() { <-s.workers }()
		s.update(job.ID, func(current *datasetExportJob) {
			current.Status = "running"
			current.UpdatedAt = time.Now().UTC()
		})
		artifact, err := run()
		s.update(job.ID, func(current *datasetExportJob) {
			current.UpdatedAt = time.Now().UTC()
			if err != nil {
				current.Status = "failed"
				current.Error = err.Error()
				return
			}
			current.Status = "completed"
			current.Artifact = &artifact
		})
	}()
	return initial
}

func (s *datasetExportJobStore) Get(id string) (datasetExportJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now().UTC())
	job, ok := s.jobs[id]
	if !ok {
		return datasetExportJob{}, false
	}
	copy := *job
	if job.Artifact != nil {
		artifact := *job.Artifact
		copy.Artifact = &artifact
	}
	return copy, true
}

func (s *datasetExportJobStore) update(id string, update func(*datasetExportJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobs[id]; job != nil {
		update(job)
	}
}

func (s *datasetExportJobStore) pruneLocked(now time.Time) {
	type terminalJob struct {
		id        string
		updatedAt time.Time
	}
	terminal := make([]terminalJob, 0, len(s.jobs))
	for id, job := range s.jobs {
		if job.Status != "completed" && job.Status != "failed" {
			continue
		}
		if now.Sub(job.UpdatedAt) > datasetExportJobRetention {
			delete(s.jobs, id)
			continue
		}
		terminal = append(terminal, terminalJob{id: id, updatedAt: job.UpdatedAt})
	}
	excess := len(s.jobs) - datasetExportJobMaxStored
	if excess <= 0 {
		return
	}
	sort.Slice(terminal, func(left, right int) bool {
		return terminal[left].updatedAt.Before(terminal[right].updatedAt)
	})
	for _, job := range terminal {
		if excess <= 0 {
			break
		}
		delete(s.jobs, job.id)
		excess--
	}
}

func newDatasetExportJobID() string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err == nil {
		return "dataset-export-" + hex.EncodeToString(value)
	}
	return fmt.Sprintf("dataset-export-%d", time.Now().UnixNano())
}
