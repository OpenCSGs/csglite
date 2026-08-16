package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/internal/observability"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestTraceDatasetExportCreatesBrowsableLocalDataset(t *testing.T) {
	s := newTestServer(t)
	record := observability.RequestRecord{
		ID: "request-export", TraceID: "trace-export", Status: "completed", StatusCode: 200,
		Method: "POST", Path: "/v1/chat/completions", Protocol: "openai", Model: "test/model",
		StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
		RequestBody:  `{"messages":[{"role":"user","content":"hello user@example.com"}]}`,
		ResponseBody: `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`,
	}
	if err := s.observability.Add(t.Context(), record); err != nil {
		t.Fatal(err)
	}

	previewRecorder := httptest.NewRecorder()
	s.handleDatasetExportPreview(previewRecorder, httptest.NewRequest(
		http.MethodPost,
		"/api/observability/dataset-exports/preview",
		strings.NewReader(`{"trace_ids":["trace-export"],"format":"openai_messages","redaction_policy":"redact"}`),
	))
	if previewRecorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", previewRecorder.Code, previewRecorder.Body.String())
	}
	var preview api.DatasetExportPreviewResponse
	if err := json.Unmarshal(previewRecorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Exported != 1 || len(preview.Risks) != 1 || bytes.Contains(preview.Sample, []byte("user@example.com")) {
		t.Fatalf("preview = %+v sample=%s", preview, preview.Sample)
	}

	createRecorder := httptest.NewRecorder()
	s.handleDatasetExportCreate(createRecorder, httptest.NewRequest(
		http.MethodPost,
		"/api/observability/dataset-exports",
		strings.NewReader(`{"trace_ids":["trace-export"],"format":"openai_messages","redaction_policy":"redact","dataset_name":"training-data"}`),
	))
	if createRecorder.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	var created api.DatasetExportJobResponse
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var completed datasetExportJob
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := s.datasetExportJobs.Get(created.ID)
		if ok && (job.Status == "completed" || job.Status == "failed") {
			completed = job
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Artifact == nil {
		t.Fatalf("job = %+v", completed)
	}
	if completed.Artifact.DatasetID != "trace-exports/training-data" {
		t.Fatalf("dataset ID = %q", completed.Artifact.DatasetID)
	}
	local, err := s.datasetManager.Get(completed.Artifact.DatasetID)
	if err != nil {
		t.Fatal(err)
	}
	if local.Origin != dataset.LocalDatasetOriginExport || len(local.FileEntries) != 3 {
		t.Fatalf("local dataset = %+v", local)
	}

	archiveRequest := httptest.NewRequest(http.MethodGet, "/api/datasets/trace-exports/training-data/export", nil)
	archiveRequest.SetPathValue("namespace", "trace-exports")
	archiveRequest.SetPathValue("name", "training-data")
	archiveRecorder := httptest.NewRecorder()
	s.handleLocalDatasetExport(archiveRecorder, archiveRequest)
	if archiveRecorder.Code != http.StatusOK {
		t.Fatalf("archive status = %d body=%s", archiveRecorder.Code, archiveRecorder.Body.String())
	}
	reader, err := zip.NewReader(bytes.NewReader(archiveRecorder.Body.Bytes()), int64(archiveRecorder.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 3 {
		t.Fatalf("archive files = %d, want 3", len(reader.File))
	}
}

func TestTraceDatasetExportPreviewSelectsAllMatchingFilter(t *testing.T) {
	s := newTestServer(t)
	start := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	for _, record := range []observability.RequestRecord{
		{
			ID: "request-before", TraceID: "trace-before", Status: "completed", StatusCode: 200,
			Method: "POST", Path: "/v1/chat/completions", Protocol: "openai", Model: "model-a",
			StartedAt: start, CompletedAt: start,
			RequestBody:  `{"messages":[{"role":"user","content":"before"}]}`,
			ResponseBody: `{"choices":[{"message":{"role":"assistant","content":"before"}}]}`,
		},
		{
			ID: "request-after", TraceID: "trace-after", Status: "completed", StatusCode: 200,
			Method: "POST", Path: "/v1/chat/completions", Protocol: "openai", Model: "model-b",
			StartedAt: start.Add(2 * time.Hour), CompletedAt: start.Add(2 * time.Hour),
			RequestBody:  `{"messages":[{"role":"user","content":"after"}]}`,
			ResponseBody: `{"choices":[{"message":{"role":"assistant","content":"after"}}]}`,
		},
	} {
		if err := s.observability.Add(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	body := `{"filter":{"from":"2026-08-15T09:00:00Z","to":"2026-08-15T11:00:00Z"},"format":"openai_messages","redaction_policy":"redact"}`
	recorder := httptest.NewRecorder()
	s.handleDatasetExportPreview(recorder, httptest.NewRequest(
		http.MethodPost,
		"/api/observability/dataset-exports/preview",
		strings.NewReader(body),
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var preview api.DatasetExportPreviewResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Selected != 1 || preview.Exported != 1 {
		t.Fatalf("filtered preview = %+v", preview)
	}
}

func TestLocalDatasetPublishCreatesAndStreamsFiles(t *testing.T) {
	var uploads atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/token/test-token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"user_name":"alice","user_uuid":"user-1"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user/alice":
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"username":"alice","uuid":"user-1"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/datasets":
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			var create csghub.CreateDatasetRequest
			if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
				t.Fatal(err)
			}
			if create.Namespace != "alice" {
				t.Fatalf("create namespace = %q, want current username alice", create.Namespace)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"name":"remote-data","path":"alice/remote-data","private":true}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/upload_file"):
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Fatal(err)
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			_ = file.Close()
			uploads.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":null}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	s := newTestServer(t)
	s.cfg.ServerURL = remote.URL
	s.cfg.Token = "test-token"
	root := dataset.DatasetDir(s.cfg.DatasetDir, "trace-exports", "local-data")
	if err := dataset.EnsureDatasetDir(s.cfg.DatasetDir, "trace-exports", "local-data"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "train.jsonl"), []byte(`{"messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dataset.SaveManifest(s.cfg.DatasetDir, &dataset.LocalDataset{
		Namespace: "trace-exports", Name: "local-data", Size: 15,
		Files: []string{"train.jsonl"}, FileEntries: []dataset.LocalDatasetFile{{Path: "train.jsonl", Size: 15}},
		Origin: dataset.LocalDatasetOriginExport,
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/datasets/trace-exports/local-data/publish", strings.NewReader(
		`{"create":true,"name":"remote-data","private":true}`,
	))
	request.SetPathValue("namespace", "trace-exports")
	request.SetPathValue("name", "local-data")
	recorder := httptest.NewRecorder()
	s.handleLocalDatasetPublish(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("publish status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if uploads.Load() != 1 {
		t.Fatalf("uploads = %d, want 1", uploads.Load())
	}
}
