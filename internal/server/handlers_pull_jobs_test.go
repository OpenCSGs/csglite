package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/dataset"
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

func TestDatasetPullJobsDedupeBySourceRepositoryAndRevision(t *testing.T) {
	store := newPullJobStore()
	now := time.Now()
	first := &pullJob{
		id: "first", kind: "dataset", name: "acme/demo", source: "huggingface",
		revision: "main", status: pullJobQueued, createdAt: now, updatedAt: now,
	}
	store.add(first)

	if got := store.getActive("dataset", "acme/demo", "huggingface", "main", nil); got != first {
		t.Fatalf("matching active job = %#v, want first", got)
	}
	if got := store.getActive("dataset", "acme/demo", "modelscope", "main", nil); got != nil {
		t.Fatalf("different-source active job = %#v, want nil", got)
	}
	if got := store.getActive("dataset", "acme/demo", "huggingface", "v2", nil); got != nil {
		t.Fatalf("different-revision active job = %#v, want nil", got)
	}
	duplicate := &pullJob{
		id: "duplicate", kind: "dataset", name: "acme/demo", source: "huggingface",
		revision: "main", status: pullJobQueued, createdAt: now, updatedAt: now,
	}
	if got := store.addIfAbsent(duplicate); got != first {
		t.Fatalf("addIfAbsent duplicate = %#v, want first", got)
	}
	if len(store.jobs) != 1 {
		t.Fatalf("jobs len = %d, want 1", len(store.jobs))
	}
}

func TestHandleDatasetPullJobCreateCarriesSourceAndRevision(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < maxConcurrentPullJobs; i++ {
		now := time.Now()
		s.pullJobs.add(&pullJob{
			id: "blocker-" + string(rune('a'+i)), kind: "model", name: "acme/blocker-" + string(rune('a'+i)),
			status: pullJobRunning, createdAt: now, updatedAt: now,
		})
	}
	req := httptest.NewRequest(http.MethodPost, "/api/datasets/pull/jobs", strings.NewReader(
		`{"dataset":"acme/demo","artifact_source":"huggingface","revision":"refs/pr/1"}`,
	))
	w := httptest.NewRecorder()
	s.handleDatasetPullJobCreate(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 body=%s", w.Code, w.Body.String())
	}
	var response api.PullJobResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Kind != "dataset" || response.Name != "acme/demo" ||
		response.ArtifactSource != "huggingface" || response.Revision != "refs/pr/1" {
		t.Fatalf("response = %#v", response)
	}
}

func TestHandlePartialDatasetPullDeleteProtectsActiveSourceScopedJob(t *testing.T) {
	s := newTestServer(t)
	partialDir := dataset.RegistryDatasetDir(s.cfg.DatasetDir, "huggingface", "acme", "demo")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, ".csghub-lite-pull.json"),
		[]byte(`{"artifact_source":"huggingface","revision":"main"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.pullJobs.add(&pullJob{
		id: "active-dataset", kind: "dataset", name: "acme/demo", source: "huggingface",
		revision: "other-revision", status: pullJobRunning, createdAt: now, updatedAt: now,
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/datasets/pull/partial", strings.NewReader(
		`{"dataset":"acme/demo","artifact_source":"huggingface","revision":"main"}`,
	))
	w := httptest.NewRecorder()
	s.handlePartialDatasetPullDelete(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(partialDir); err != nil {
		t.Fatalf("active partial directory was removed: %v", err)
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

func TestPullJobStoreClaimsAtMostMaxQueuedJobs(t *testing.T) {
	store := newPullJobStore()
	now := time.Now()
	for i := 0; i < 5; i++ {
		store.add(&pullJob{
			id:        string(rune('a' + i)),
			kind:      "model",
			name:      "test/model-" + string(rune('a'+i)),
			status:    pullJobQueued,
			createdAt: now,
			updatedAt: now,
		})
	}

	for i := 0; i < maxConcurrentPullJobs; i++ {
		job := store.claimNextQueued(maxConcurrentPullJobs)
		if job == nil {
			t.Fatalf("claim %d returned nil, want job", i)
		}
		got := pullJobResponse(job)
		if got.Status != pullJobRunning {
			t.Fatalf("claimed job status = %q, want %q", got.Status, pullJobRunning)
		}
	}
	if job := store.claimNextQueued(maxConcurrentPullJobs); job != nil {
		t.Fatalf("claim with full slots returned %q, want nil", job.id)
	}

	first := store.get("a")
	first.setSucceeded()
	job := store.claimNextQueued(maxConcurrentPullJobs)
	if job == nil {
		t.Fatal("claim after releasing one slot returned nil, want job")
	}
	if job.id != "d" {
		t.Fatalf("claimed job id = %q, want d", job.id)
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
