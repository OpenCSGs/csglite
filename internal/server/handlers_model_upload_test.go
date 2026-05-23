package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestHandleModelUpload_Files(t *testing.T) {
	s := newTestServer(t)
	body, contentType := multipartModelUpload(t, map[string]string{
		"model":     "local/uploaded",
		"mode":      "files",
		"overwrite": "false",
	}, []uploadTestFile{
		{Path: "weights/model.gguf", Body: "gguf"},
		{Path: "config.json", Body: "{}"},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/models/upload", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp api.ModelUploadResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "success" || resp.Model != "local/uploaded" {
		t.Fatalf("response = %#v, want success local/uploaded", resp)
	}
	if resp.Details.Format != string(model.FormatGGUF) {
		t.Fatalf("format = %q, want gguf", resp.Details.Format)
	}
	if len(resp.Files) != 2 {
		t.Fatalf("files len = %d, want 2", len(resp.Files))
	}
	if _, err := os.Stat(filepath.Join(s.cfg.ModelDir, "local", "uploaded", "weights", "model.gguf")); err != nil {
		t.Fatalf("uploaded file: %v", err)
	}
	assertNoUploadStagingDirs(t, s.cfg.TempDir())
}

func TestHandleModelUpload_Archive(t *testing.T) {
	s := newTestServer(t)
	archive := zipBytes(t, map[string]string{
		"wrapped/model.safetensors": "weights",
	})
	body, contentType := multipartModelUpload(t, map[string]string{
		"model": "local/archive",
		"mode":  "archive",
	}, []uploadTestFile{{Path: "model.zip", BodyBytes: archive}})

	req := httptest.NewRequest(http.MethodPost, "/api/models/upload", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp api.ModelUploadResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Details.Format != string(model.FormatSafeTensors) {
		t.Fatalf("format = %q, want safetensors", resp.Details.Format)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.ModelDir, "local", "archive", "model.safetensors")); err != nil {
		t.Fatalf("archive was not imported: %v", err)
	}
}

func TestHandleModelUpload_RejectsInvalidPath(t *testing.T) {
	s := newTestServer(t)
	body, contentType := multipartModelUpload(t, map[string]string{
		"model": "local/bad",
		"mode":  "files",
	}, []uploadTestFile{{Path: "../bad.gguf", Body: "bad"}})

	req := httptest.NewRequest(http.MethodPost, "/api/models/upload", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleModelUpload_RejectsConflictUnlessOverwrite(t *testing.T) {
	s := newTestServer(t)
	if err := model.SaveManifest(s.cfg.ModelDir, &model.LocalModel{Namespace: "local", Name: "demo", Format: model.FormatGGUF}); err != nil {
		t.Fatal(err)
	}

	body, contentType := multipartModelUpload(t, map[string]string{
		"model": "local/demo",
		"mode":  "files",
	}, []uploadTestFile{{Path: "model.gguf", Body: "new"}})
	req := httptest.NewRequest(http.MethodPost, "/api/models/upload", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("conflict status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	body, contentType = multipartModelUpload(t, map[string]string{
		"model":     "local/demo",
		"mode":      "files",
		"overwrite": "true",
	}, []uploadTestFile{{Path: "model.gguf", Body: "new"}})
	req = httptest.NewRequest(http.MethodPost, "/api/models/upload", body)
	req.Header.Set("Content-Type", contentType)
	w = httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("overwrite status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

type uploadTestFile struct {
	Path      string
	Body      string
	BodyBytes []byte
}

func multipartModelUpload(t *testing.T, fields map[string]string, files []uploadTestFile) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		if err := writer.WriteField("paths", file.Path); err != nil {
			t.Fatal(err)
		}
		part, err := writer.CreateFormFile("files", filepath.Base(file.Path))
		if err != nil {
			t.Fatal(err)
		}
		data := file.BodyBytes
		if data == nil {
			data = []byte(file.Body)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body, writer.FormDataContentType()
}

func assertNoUploadStagingDirs(t *testing.T, tmpDir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(tmpDir, ".csghub-model-upload-*"))
	if err != nil {
		t.Fatalf("glob upload staging dirs: %v", err)
	}
	if len(matches) > 0 {
		t.Fatalf("upload staging dirs were not cleaned up: %#v", matches)
	}
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	body := &bytes.Buffer{}
	writer := zip.NewWriter(body)
	for name, content := range files {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}
