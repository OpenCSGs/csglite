package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type failingStreamASREngine struct {
	closed bool
}

func (e *failingStreamASREngine) Transcribe(context.Context, api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error) {
	return nil, errors.New("not used")
}

func (e *failingStreamASREngine) TranscribeStream(context.Context, api.OpenAIAudioTranscriptionRequest, func(api.OpenAIAudioTranscriptionResponse) error) error {
	return errors.New("worker exited")
}

func (e *failingStreamASREngine) Close() error {
	e.closed = true
	return nil
}

func (e *failingStreamASREngine) ModelName() string {
	return "test-asr"
}

func TestHandleOpenAIAudioTranscriptionsUsesLiteTempDir(t *testing.T) {
	missingTempDir := filepath.Join(t.TempDir(), "missing-temp")
	t.Setenv("TMPDIR", missingTempDir)
	t.Setenv("TMP", missingTempDir)
	t.Setenv("TEMP", missingTempDir)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "missing-asr-model"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		t.Fatalf("write response_format field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "long_recording.mp3")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := io.CopyN(part, zeroReader{}, maxAudioUploadMemory+1024); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	storageDir := t.TempDir()
	cfg := &config.Config{
		ModelDir:   config.ModelDirForStorage(storageDir),
		DatasetDir: config.DatasetDirForStorage(storageDir),
	}
	s := New(cfg, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleOpenAIAudioTranscriptions(w, req)

	if strings.Contains(w.Body.String(), "invalid multipart request") {
		t.Fatalf("expected upload parsing to avoid system temp dir, got status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(missingTempDir); !os.IsNotExist(err) {
		t.Fatalf("expected system temp dir to remain unused, stat err=%v", err)
	}
	if _, err := os.Stat(cfg.TempDir()); err != nil {
		t.Fatalf("expected lite temp dir to be used: %v", err)
	}
}

func TestHandleOpenAIAudioTranscriptionsParsesFieldsFromStreamedMultipart(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", ""); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "clip.mp3")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write([]byte("audio")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	s := New(&config.Config{
		ModelDir:   config.ModelDirForStorage(t.TempDir()),
		DatasetDir: config.DatasetDirForStorage(t.TempDir()),
	}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleOpenAIAudioTranscriptions(w, req)

	if !strings.Contains(w.Body.String(), "model is required") {
		t.Fatalf("expected streamed multipart fields to be parsed, got status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestStreamAudioTranscriptionClosesFailedWorker(t *testing.T) {
	modelID := "AIWizards/Fun-ASR-Nano-2512"
	engine := &failingStreamASREngine{}
	s := New(&config.Config{
		ModelDir:   config.ModelDirForStorage(t.TempDir()),
		DatasetDir: config.DatasetDirForStorage(t.TempDir()),
	}, "test")
	s.asrEngines[modelID] = &managedASREngine{engine: engine}

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	w := httptest.NewRecorder()

	s.streamAudioTranscription(w, req, modelID, engine, api.OpenAIAudioTranscriptionRequest{})

	if !engine.closed {
		t.Fatal("expected failed ASR stream worker to be closed")
	}
	if _, ok := s.asrEngines[modelID]; ok {
		t.Fatal("expected failed ASR stream worker to be removed from cache")
	}
	if !strings.Contains(w.Body.String(), "worker exited") {
		t.Fatalf("expected SSE error response, got %q", w.Body.String())
	}
}
