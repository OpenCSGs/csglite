package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
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

func TestHandleModelUpload_GGUFWithMMProjIsVisionModel(t *testing.T) {
	s := newTestServer(t)
	body, contentType := multipartModelUpload(t, map[string]string{
		"model": "local/qwen3.5-0.8b-gguf",
		"mode":  "files",
	}, []uploadTestFile{
		{Path: "qwen3.5-0.8b-q4_k_m.gguf", Body: "gguf"},
		{Path: "mmproj-qwen3.5-0.8b-f16.gguf", Body: "mmproj"},
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
	if resp.Details.PipelineTag != "image-text-to-text" {
		t.Fatalf("pipeline_tag = %q, want image-text-to-text", resp.Details.PipelineTag)
	}
	if !resp.Details.HasMMProj {
		t.Fatal("has_mmproj = false, want true")
	}
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

func TestHandleModelUploadSession_AssemblesChunks(t *testing.T) {
	s := newTestServer(t)
	uploadID := startModelUploadSession(t, s, "local/chunked")

	first := uploadModelSessionChunk(t, s, uploadID, "model.gguf", "abc", "bytes 0-2/7")
	if first.Code != http.StatusOK {
		t.Fatalf("first chunk status = %d: %s", first.Code, first.Body.String())
	}
	var firstResp struct {
		NextOffset int64 `json:"next_offset"`
		Complete   bool  `json:"complete"`
	}
	if err := json.NewDecoder(first.Body).Decode(&firstResp); err != nil {
		t.Fatalf("decode first chunk response: %v", err)
	}
	if firstResp.NextOffset != 3 || firstResp.Complete {
		t.Fatalf("first chunk response = %#v, want offset 3 incomplete", firstResp)
	}

	second := uploadModelSessionChunk(t, s, uploadID, "model.gguf", "defg", "bytes 3-6/7")
	if second.Code != http.StatusOK {
		t.Fatalf("second chunk status = %d: %s", second.Code, second.Body.String())
	}
	var secondResp struct {
		NextOffset int64 `json:"next_offset"`
		Complete   bool  `json:"complete"`
	}
	if err := json.NewDecoder(second.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second chunk response: %v", err)
	}
	if secondResp.NextOffset != 7 || !secondResp.Complete {
		t.Fatalf("second chunk response = %#v, want offset 7 complete", secondResp)
	}

	completeModelUploadSession(t, s, uploadID)
	data, err := os.ReadFile(filepath.Join(s.cfg.ModelDir, "local", "chunked", "model.gguf"))
	if err != nil {
		t.Fatalf("read imported model: %v", err)
	}
	if string(data) != "abcdefg" {
		t.Fatalf("imported model = %q, want %q", data, "abcdefg")
	}
	assertNoUploadStagingDirs(t, s.cfg.TempDir())
}

func TestHandleModelUploadSession_RejectsWrongChunkOffset(t *testing.T) {
	s := newTestServer(t)
	uploadID := startModelUploadSession(t, s, "local/offset")
	t.Cleanup(func() { cancelModelUploadSession(s, uploadID) })

	first := uploadModelSessionChunk(t, s, uploadID, "model.gguf", "abc", "bytes 0-2/6")
	if first.Code != http.StatusOK {
		t.Fatalf("first chunk status = %d: %s", first.Code, first.Body.String())
	}
	completeReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/models/upload/%s/complete", uploadID), nil)
	completeResp := httptest.NewRecorder()
	s.routes().ServeHTTP(completeResp, completeReq)
	if completeResp.Code != http.StatusBadRequest {
		t.Fatalf("incomplete file completion status = %d, want %d", completeResp.Code, http.StatusBadRequest)
	}
	if getModelUploadSession(uploadID) == nil {
		t.Fatal("incomplete completion unexpectedly cleaned the upload session")
	}
	wrong := uploadModelSessionChunk(t, s, uploadID, "model.gguf", "def", "bytes 2-4/6")
	if wrong.Code != http.StatusConflict {
		t.Fatalf("wrong offset status = %d, want %d: %s", wrong.Code, http.StatusConflict, wrong.Body.String())
	}
	if !strings.Contains(wrong.Body.String(), `"expected_offset":3`) {
		t.Fatalf("wrong offset response = %s", wrong.Body.String())
	}

	sess := getModelUploadSession(uploadID)
	data, err := os.ReadFile(filepath.Join(sess.Source, "model.gguf"))
	if err != nil {
		t.Fatalf("read staged model: %v", err)
	}
	if string(data) != "abc" {
		t.Fatalf("staged model = %q, want abc", data)
	}
}

func TestHandleModelUploadSession_DiscardsIncompleteChunk(t *testing.T) {
	s := newTestServer(t)
	uploadID := startModelUploadSession(t, s, "local/incomplete")
	t.Cleanup(func() { cancelModelUploadSession(s, uploadID) })

	resp := uploadModelSessionChunk(t, s, uploadID, "model.gguf", "abc", "bytes 0-4/5")
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("incomplete chunk status = %d, want %d: %s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	sess := getModelUploadSession(uploadID)
	chunks, err := filepath.Glob(filepath.Join(sess.Root, ".upload-chunk-*"))
	if err != nil {
		t.Fatalf("glob chunks: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("temporary chunks were not cleaned up: %#v", chunks)
	}
	if _, err := os.Stat(filepath.Join(sess.Source, "model.gguf")); !os.IsNotExist(err) {
		t.Fatalf("partial target exists, stat error = %v", err)
	}
}

func TestHandleModelUploadSession_PreservesWholeFilePUT(t *testing.T) {
	s := newTestServer(t)
	uploadID := startModelUploadSession(t, s, "local/legacy")

	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/models/upload/%s/file?path=model.gguf&filename=model.gguf", uploadID), strings.NewReader("legacy"))
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy upload status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"complete":true`) {
		t.Fatalf("legacy upload response = %s", w.Body.String())
	}
	completeModelUploadSession(t, s, uploadID)
}

func startModelUploadSession(t *testing.T, s *Server, modelID string) string {
	t.Helper()
	body := fmt.Sprintf(`{"model":%q,"mode":"files"}`, modelID)
	req := httptest.NewRequest(http.MethodPost, "/api/models/upload/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("start upload status = %d: %s", w.Code, w.Body.String())
	}
	var resp api.ModelUploadStartResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	t.Cleanup(func() { cancelModelUploadSession(s, resp.UploadID) })
	return resp.UploadID
}

func uploadModelSessionChunk(t *testing.T, s *Server, uploadID, path, body, contentRange string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/models/upload/%s/file?path=%s&filename=%s", uploadID, path, path), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Range", contentRange)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	return w
}

func completeModelUploadSession(t *testing.T, s *Server, uploadID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/models/upload/%s/complete", uploadID), nil)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("complete upload status = %d: %s", w.Code, w.Body.String())
	}
}

func cancelModelUploadSession(s *Server, uploadID string) {
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/models/upload/%s", uploadID), nil)
	s.routes().ServeHTTP(httptest.NewRecorder(), req)
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
