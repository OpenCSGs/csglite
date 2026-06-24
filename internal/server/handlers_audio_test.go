package server

import (
	"bytes"
	"context"
	"encoding/json"
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

type chunkingStreamASREngine struct {
	chunks []string
}

func (e *chunkingStreamASREngine) Transcribe(context.Context, api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error) {
	return nil, errors.New("not used")
}

func (e *chunkingStreamASREngine) TranscribeStream(_ context.Context, _ api.OpenAIAudioTranscriptionRequest, emit func(api.OpenAIAudioTranscriptionResponse) error) error {
	for _, chunk := range e.chunks {
		if err := emit(api.OpenAIAudioTranscriptionResponse{Text: chunk}); err != nil {
			return err
		}
	}
	return nil
}

func (e *chunkingStreamASREngine) Close() error {
	return nil
}

func (e *chunkingStreamASREngine) ModelName() string {
	return "test-asr"
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

func TestStreamAudioTranscriptionEmitsLocalChunks(t *testing.T) {
	modelID := "local-asr"
	engine := &chunkingStreamASREngine{chunks: []string{"hello ", "world"}}
	s := New(&config.Config{
		ModelDir:   config.ModelDirForStorage(t.TempDir()),
		DatasetDir: config.DatasetDirForStorage(t.TempDir()),
	}, "test")
	s.asrEngines[modelID] = &managedASREngine{engine: engine}

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	w := httptest.NewRecorder()

	s.streamAudioTranscription(w, req, modelID, engine, api.OpenAIAudioTranscriptionRequest{})

	body := w.Body.String()
	if !strings.Contains(body, `"text":"hello "`) || !strings.Contains(body, `"text":"world"`) || !strings.Contains(body, `"text":"hello world"`) {
		t.Fatalf("expected local ASR chunks and final transcript, got %s", body)
	}
}

func TestHandleOpenAIAudioTranscriptionsProxiesCloudSource(t *testing.T) {
	var gotAuth string
	var gotModel string
	var gotSource string
	var gotStream string
	var gotAudio string

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(maxAudioUploadMemory); err != nil {
			t.Fatalf("parse cloud multipart form: %v", err)
		}
		gotModel = r.FormValue("model")
		gotSource = r.FormValue("source")
		gotStream = r.FormValue("stream")
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("read cloud audio file: %v", err)
		}
		defer file.Close()
		audio, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("read cloud audio content: %v", err)
		}
		gotAudio = string(audio)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.OpenAIAudioTranscriptionResponse{Text: "cloud transcript"})
	}))
	defer apiServer.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "FunAudioLLM/Fun-ASR-Nano-2512:s-test"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := writer.WriteField("source", "cloud"); err != nil {
		t.Fatalf("write source field: %v", err)
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		t.Fatalf("write response_format field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "clip.mp3")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write([]byte("audio bytes")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	s := New(&config.Config{
		ModelDir:      config.ModelDirForStorage(t.TempDir()),
		DatasetDir:    config.DatasetDirForStorage(t.TempDir()),
		AIGatewayURL:  apiServer.URL,
		OpenCSGAPIKey: "test-key",
	}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleOpenAIAudioTranscriptions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer test-key", gotAuth)
	}
	if gotModel != "FunAudioLLM/Fun-ASR-Nano-2512:s-test" {
		t.Fatalf("model = %q", gotModel)
	}
	if gotSource != "" {
		t.Fatalf("source should not be forwarded upstream, got %q", gotSource)
	}
	if gotStream != "" {
		t.Fatalf("stream should not be forwarded for non-stream request, got %q", gotStream)
	}
	if gotAudio != "audio bytes" {
		t.Fatalf("audio = %q", gotAudio)
	}
	if !strings.Contains(w.Body.String(), "cloud transcript") {
		t.Fatalf("response body = %s", w.Body.String())
	}
}

func TestHandleOpenAIAudioTranscriptionsStreamsCloudSource(t *testing.T) {
	var gotAccept string
	var gotStream string

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		if err := r.ParseMultipartForm(maxAudioUploadMemory); err != nil {
			t.Fatalf("parse cloud multipart form: %v", err)
		}
		gotStream = r.FormValue("stream")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"text\":\"hello \",\"done\":false}\n\n")
		_, _ = io.WriteString(w, "data: {\"text\":\"world\",\"done\":true}\n\n")
	}))
	defer apiServer.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "FunAudioLLM/Fun-ASR-Nano-2512:s-test"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := writer.WriteField("source", "cloud"); err != nil {
		t.Fatalf("write source field: %v", err)
	}
	if err := writer.WriteField("stream", "true"); err != nil {
		t.Fatalf("write stream field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "clip.mp3")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write([]byte("audio bytes")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	s := New(&config.Config{
		ModelDir:      config.ModelDirForStorage(t.TempDir()),
		DatasetDir:    config.DatasetDirForStorage(t.TempDir()),
		AIGatewayURL:  apiServer.URL,
		OpenCSGAPIKey: "test-key",
	}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleOpenAIAudioTranscriptions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept = %q, want text/event-stream", gotAccept)
	}
	if gotStream != "true" {
		t.Fatalf("stream = %q, want true", gotStream)
	}
	if !strings.Contains(w.Body.String(), `"text":"hello "`) || !strings.Contains(w.Body.String(), `"text":"hello world"`) {
		t.Fatalf("stream response body = %s", w.Body.String())
	}
}

func TestHandleOpenAIAudioTranscriptionsStreamsCloudPlainText(t *testing.T) {
	var gotStream string

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(maxAudioUploadMemory); err != nil {
			t.Fatalf("parse cloud multipart form: %v", err)
		}
		gotStream = r.FormValue("stream")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if f, ok := w.(http.Flusher); ok {
			_, _ = io.WriteString(w, "hello ")
			f.Flush()
			_, _ = io.WriteString(w, "world")
			f.Flush()
		} else {
			_, _ = io.WriteString(w, "hello world")
		}
	}))
	defer apiServer.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "FunAudioLLM/Fun-ASR-Nano-2512:s-test"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := writer.WriteField("source", "cloud"); err != nil {
		t.Fatalf("write source field: %v", err)
	}
	if err := writer.WriteField("stream", "true"); err != nil {
		t.Fatalf("write stream field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "clip.mp3")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write([]byte("audio bytes")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	s := New(&config.Config{
		ModelDir:      config.ModelDirForStorage(t.TempDir()),
		DatasetDir:    config.DatasetDirForStorage(t.TempDir()),
		AIGatewayURL:  apiServer.URL,
		OpenCSGAPIKey: "test-key",
	}, "test")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	s.handleOpenAIAudioTranscriptions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotStream != "true" {
		t.Fatalf("stream = %q, want true", gotStream)
	}
	if !strings.Contains(w.Body.String(), `"text":"hello world"`) {
		t.Fatalf("stream response body = %s", w.Body.String())
	}
}
