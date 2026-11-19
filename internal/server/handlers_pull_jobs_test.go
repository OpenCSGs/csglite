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
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestActiveModelPullsReportsByteProgress(t *testing.T) {
	store := newPullJobStore()
	store.add(&pullJob{
		id: "job-1", kind: "model", name: "Qwen/Qwen3-ASR-0.6B", source: "modelscope",
		status:   pullJobRunning,
		progress: api.PullResponse{Status: "copying model.safetensors", CompletedBytes: 12, TotalBytes: 40, SpeedBytesPerSec: 7},
	})
	names, pulls := store.activeModelPulls()
	if len(names) != 1 || names[0] != "Qwen/Qwen3-ASR-0.6B" || len(pulls) != 1 {
		t.Fatalf("names=%v pulls=%+v", names, pulls)
	}
	if pulls[0].CompletedBytes != 12 || pulls[0].TotalBytes != 40 || pulls[0].Detail != "copying model.safetensors" || pulls[0].SpeedBytesPerSec != 7 {
		t.Fatalf("pull = %+v", pulls[0])
	}
}

func TestPullJobProgressReportsTransferSpeed(t *testing.T) {
	job := &pullJob{}
	job.setProgress(api.PullResponse{Status: "downloading a", CompletedBytes: 0})
	if job.progress.SpeedBytesPerSec != 0 {
		t.Fatalf("first sample speed = %d, want 0", job.progress.SpeedBytesPerSec)
	}
	for i := int64(1); i <= 2; i++ {
		time.Sleep(120 * time.Millisecond)
		job.setProgress(api.PullResponse{Status: "downloading a", CompletedBytes: i << 20})
	}
	if job.progress.SpeedBytesPerSec <= 0 {
		t.Fatalf("speed = %d, want > 0", job.progress.SpeedBytesPerSec)
	}
}

func TestPullJobCancelClearsSpeed(t *testing.T) {
	job := &pullJob{
		status:   pullJobRunning,
		progress: api.PullResponse{Status: "downloading a", CompletedBytes: 12, SpeedBytesPerSec: 4096},
		speed:    csghub.NewSpeedMeter(0),
	}
	job.setCancelled()
	if job.progress.SpeedBytesPerSec != 0 {
		t.Fatalf("cancelled speed = %d, want 0", job.progress.SpeedBytesPerSec)
	}
	// A callback that was already in flight must not revive the job.
	job.setProgress(api.PullResponse{Status: "downloading b", CompletedBytes: 1 << 20})
	if job.progress.Status != "downloading a" || job.progress.SpeedBytesPerSec != 0 {
		t.Fatalf("progress after cancel = %+v, want unchanged", job.progress)
	}
}

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

// liveSpeedMeter returns a meter whose newest sample is at now, so Rate(now)
// reports five times bytesPerInterval per second.
func liveSpeedMeter(now time.Time, bytesPerInterval int64) *csghub.SpeedMeter {
	m := csghub.NewSpeedMeter(3 * time.Second)
	start := now.Add(-400 * time.Millisecond)
	m.Update(0, start)
	m.Update(bytesPerInterval, start.Add(200*time.Millisecond))
	m.Update(2*bytesPerInterval, start.Add(400*time.Millisecond))
	return m
}

func TestPullJobResponseReportsLiveSpeed(t *testing.T) {
	job := &pullJob{
		id: "job-live", kind: "model", name: "Qwen/x", status: pullJobRunning,
		progress: api.PullResponse{Status: "downloading a", CompletedBytes: 2 << 20, SpeedBytesPerSec: 999},
		speed:    liveSpeedMeter(time.Now(), 1<<20),
	}
	if got := pullJobResponse(job).Progress.SpeedBytesPerSec; got != 5<<20 {
		t.Fatalf("running speed = %d, want %d", got, int64(5<<20))
	}

	job.status = pullJobSucceeded
	if got := pullJobResponse(job).Progress.SpeedBytesPerSec; got != 999 {
		t.Fatalf("finished speed = %d, want the stored 999", got)
	}

	job.status = pullJobRunning
	job.speed = nil
	if got := pullJobResponse(job).Progress.SpeedBytesPerSec; got != 999 {
		t.Fatalf("fallback speed = %d, want 999", got)
	}
}

func TestActiveModelPullsReportsLiveSpeed(t *testing.T) {
	store := newPullJobStore()
	now := time.Now()
	store.add(&pullJob{
		id: "job-live", kind: "model", name: "Qwen/live", source: "opencsg", status: pullJobRunning,
		progress: api.PullResponse{Status: "downloading a", CompletedBytes: 2 << 20, SpeedBytesPerSec: 999},
		speed:    liveSpeedMeter(now, 1<<20),
	})
	store.add(&pullJob{
		id: "job-stalled", kind: "model", name: "Qwen/stalled", source: "opencsg", status: pullJobRunning,
		progress: api.PullResponse{Status: "downloading b", CompletedBytes: 1 << 20, SpeedBytesPerSec: 4096},
		speed:    liveSpeedMeter(now.Add(-10*time.Second), 1<<20),
	})

	_, pulls := store.activeModelPulls()
	byName := make(map[string]int64, len(pulls))
	for _, p := range pulls {
		byName[p.Model] = p.SpeedBytesPerSec
	}
	if got := byName["Qwen/live"]; got != 5<<20 {
		t.Fatalf("live speed = %d, want %d", got, int64(5<<20))
	}
	if got := byName["Qwen/stalled"]; got != 0 {
		t.Fatalf("stalled speed = %d, want 0", got)
	}
}

func TestPullJobProgressIgnoresCallbacksAfterFinish(t *testing.T) {
	for _, status := range []string{pullJobSucceeded, pullJobFailed, pullJobCancelled} {
		t.Run(status, func(t *testing.T) {
			job := &pullJob{
				status:   status,
				progress: api.PullResponse{Status: "downloading a", CompletedBytes: 12, SpeedBytesPerSec: 4096},
			}
			job.setProgress(api.PullResponse{Status: "downloading b", CompletedBytes: 1 << 20})
			if job.progress.Status != "downloading a" || job.progress.SpeedBytesPerSec != 4096 {
				t.Fatalf("progress = %+v, want unchanged", job.progress)
			}
			if job.speed != nil {
				t.Fatal("finished job must not allocate a meter")
			}
		})
	}
}

func TestPullJobGetExposesTransferSpeed(t *testing.T) {
	s := newTestServer(t)
	job := &pullJob{
		id: "job-speed", kind: "model", name: "Qwen/x", status: pullJobRunning,
		progress: api.PullResponse{Status: "downloading a", CompletedBytes: 2 << 20, TotalBytes: 4 << 20},
		speed:    liveSpeedMeter(time.Now(), 1<<20),
	}
	s.pullJobs.add(job)

	progress := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/pull/jobs/job-speed", nil)
		req.SetPathValue("jobID", "job-speed")
		rec := httptest.NewRecorder()
		s.handlePullJobGet(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Progress map[string]any `json:"progress"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return body.Progress
	}

	if p := progress(); p["speed_bytes_per_sec"] != float64(5<<20) {
		t.Fatalf("speed = %v, want %d", p["speed_bytes_per_sec"], int64(5<<20))
	}

	job.setSucceeded()
	if p := progress(); p["speed_bytes_per_sec"] != nil {
		t.Fatalf("finished job must omit the speed, got %v", p["speed_bytes_per_sec"])
	}
}
