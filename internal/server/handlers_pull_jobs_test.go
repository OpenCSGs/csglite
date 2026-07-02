package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/cloud"
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

func TestPullJobNormalizeQuantsPrefersQuantsOverLegacyQuant(t *testing.T) {
	got := normalizePullQuants("Q8_0", []string{" q4_0 ", "Q4_0", "q5_k_m"})
	want := []string{"Q4_0", "Q5_K_M"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizePullQuants = %#v, want %#v", got, want)
	}
}

func TestPullJobNormalizeQuantsEmptyListOverridesLegacyQuant(t *testing.T) {
	got := normalizePullQuants("Q8_0", []string{})
	if len(got) != 0 {
		t.Fatalf("normalizePullQuants = %#v, want empty", got)
	}
}

func TestPullJobResponseIncludesSelectedQuants(t *testing.T) {
	now := time.Now()
	job := &pullJob{
		id:        "job-1",
		kind:      "model",
		name:      "test/model",
		quant:     "Q4_0",
		quants:    []string{"Q4_0", "Q8_0"},
		status:    pullJobQueued,
		createdAt: now,
		updatedAt: now,
	}
	got := pullJobResponse(job)
	if got.Quant != "Q4_0" {
		t.Fatalf("Quant = %q, want Q4_0", got.Quant)
	}
	want := []string{"Q4_0", "Q8_0"}
	if !reflect.DeepEqual(got.Quants, want) {
		t.Fatalf("Quants = %#v, want %#v", got.Quants, want)
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

func TestReportModelDownloadFailureSendsModelID(t *testing.T) {
	var events []cloud.ClientEvent
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("path = %q, want /events", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
			t.Fatalf("decode events: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer apiServer.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = apiServer.URL
	s.reportModelDownloadFailure(&pullJob{
		id:   "job-1",
		kind: "model",
		name: "missing/model",
	}, errors.New("not found"))

	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Module != "csghub-lite" || events[0].ID != "model_download_failed" || events[0].Value != "missing/model" {
		t.Fatalf("event = %#v, want model download failure with model id", events[0])
	}
	var ext modelDownloadFailureEventExtension
	if err := json.Unmarshal([]byte(events[0].Extension), &ext); err != nil {
		t.Fatalf("decode extension: %v", err)
	}
	if ext.ReportFrom != "csglite" {
		t.Fatalf("report_from = %q, want csglite", ext.ReportFrom)
	}
}
