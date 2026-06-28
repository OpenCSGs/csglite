package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestPullJobCreateRequiresModel(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/pull/jobs", strings.NewReader(`{"model":""}`))
	w := httptest.NewRecorder()
	s.handlePullJobCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPullJobGetNotFound(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pull/jobs/missing", nil)
	req.SetPathValue("jobID", "missing")
	w := httptest.NewRecorder()
	s.handlePullJobGet(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestPullJobCreateReturnsExistingActiveJob(t *testing.T) {
	s := newTestServer(t)
	body := `{"model":"test/model"}`
	req := httptest.NewRequest(http.MethodPost, "/api/pull/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handlePullJobCreate(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", w.Code, http.StatusAccepted)
	}
	var first api.PullJobResponse
	if err := json.NewDecoder(w.Body).Decode(&first); err != nil {
		t.Fatalf("decode first job: %v", err)
	}
	if first.ID == "" {
		t.Fatal("first job id is empty")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/pull/jobs", strings.NewReader(body))
	w = httptest.NewRecorder()
	s.handlePullJobCreate(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("second status = %d, want %d", w.Code, http.StatusAccepted)
	}
	var second api.PullJobResponse
	if err := json.NewDecoder(w.Body).Decode(&second); err != nil {
		t.Fatalf("decode second job: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second job id = %q, want %q", second.ID, first.ID)
	}
}

func TestPullJobCancel(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/pull/jobs", strings.NewReader(`{"model":"test/model"}`))
	w := httptest.NewRecorder()
	s.handlePullJobCreate(w, req)
	var job api.PullJobResponse
	if err := json.NewDecoder(w.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/pull/jobs/"+job.ID, nil)
	req.SetPathValue("jobID", job.ID)
	w = httptest.NewRecorder()
	s.handlePullJobCancel(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d", w.Code, http.StatusOK)
	}
	var cancelled api.PullJobResponse
	if err := json.NewDecoder(w.Body).Decode(&cancelled); err != nil {
		t.Fatalf("decode cancelled job: %v", err)
	}
	if cancelled.Status != pullJobCancelled {
		t.Fatalf("status = %q, want %q", cancelled.Status, pullJobCancelled)
	}
}
