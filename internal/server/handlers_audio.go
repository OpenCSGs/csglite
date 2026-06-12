package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/asr"
	"github.com/opencsgs/csghub-lite/internal/imagegen"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const maxAudioUploadMemory = 32 << 20
const maxAudioFormFieldBytes = 1 << 20

// POST /v1/audio/transcriptions -- OpenAI-compatible local audio transcription.
func (s *Server) handleOpenAIAudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	audioPath, cleanup, form, err := s.saveUploadedAudio(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	defer cleanup()

	modelID := strings.TrimSpace(form.Get("model"))
	if modelID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	req := api.OpenAIAudioTranscriptionRequest{
		Model:          modelID,
		FilePath:       audioPath,
		Language:       strings.TrimSpace(form.Get("language")),
		Prompt:         strings.TrimSpace(form.Get("prompt")),
		ResponseFormat: normalizeAudioResponseFormat(form.Get("response_format")),
		Hotwords:       parseAudioHotwords(form.Get("hotwords")),
	}
	stream := parseAudioStream(form.Get("stream")) || requestWantsSSE(r)
	if value := strings.TrimSpace(form.Get("temperature")); value != "" {
		temperature, err := strconv.ParseFloat(value, 64)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "temperature must be a number")
			return
		}
		req.Temperature = &temperature
	}
	if value := strings.TrimSpace(form.Get("itn")); value != "" {
		itn, err := strconv.ParseBool(value)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "itn must be a boolean")
			return
		}
		req.ITN = &itn
	}
	switch req.ResponseFormat {
	case "json", "verbose_json", "text":
	default:
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "response_format must be json, verbose_json, or text")
		return
	}

	eng, err := s.getOrLoadASREngine(r.Context(), modelID)
	if err != nil {
		if status, ok := imagegen.RuntimeStatusFromError(err); ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"errorCode": http.StatusServiceUnavailable,
				"error": map[string]interface{}{
					"message": err.Error(),
					"type":    "runtime_not_ready",
				},
				"runtime": status,
			})
			return
		}
		writeOpenAIError(w, http.StatusBadRequest, "model_not_found", err.Error())
		return
	}
	if stream {
		s.streamAudioTranscription(w, r, modelID, eng, req)
		return
	}
	resp, err := eng.Transcribe(r.Context(), req)
	if err != nil {
		log.Printf("MODEL %s: ASR transcription failed, reloading worker once: %v", modelID, err)
		s.closeASREngine(modelID)
		eng, reloadErr := s.getOrLoadASREngine(r.Context(), modelID)
		if reloadErr != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", reloadErr.Error())
			return
		}
		resp, err = eng.Transcribe(r.Context(), req)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
	}
	s.touchASREngine(modelID)

	if req.ResponseFormat == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, resp.Text)
		return
	}
	if req.ResponseFormat == "json" {
		writeJSON(w, http.StatusOK, map[string]string{"text": resp.Text})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) streamAudioTranscription(w http.ResponseWriter, r *http.Request, modelID string, eng asr.Engine, req api.OpenAIAudioTranscriptionRequest) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	var fullText strings.Builder
	err := eng.TranscribeStream(r.Context(), req, func(chunk api.OpenAIAudioTranscriptionResponse) error {
		fullText.WriteString(chunk.Text)
		writeSSE(w, map[string]interface{}{
			"text":     chunk.Text,
			"response": chunk,
			"done":     false,
		})
		return nil
	})
	if err != nil {
		log.Printf("MODEL %s: ASR stream transcription failed: %v", modelID, err)
		s.closeASREngine(modelID)
		writeSSE(w, map[string]interface{}{
			"error": err.Error(),
			"done":  true,
		})
		return
	}
	s.touchASREngine(modelID)
	writeSSE(w, map[string]interface{}{
		"text": fullText.String(),
		"done": true,
	})
}

// GET /api/asr-runtime -- report the shared Python runtime ASR package status.
func (s *Server) handleASRRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	manager, err := imagegen.NewASRRuntimeManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, manager.ASRStatus(r.Context()))
}

// POST /api/asr-runtime/install -- install or repair ASR packages in the shared Python runtime.
func (s *Server) handleASRRuntimeInstall(w http.ResponseWriter, r *http.Request) {
	var req api.ASRRuntimeInstallRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	manager, err := imagegen.NewASRRuntimeManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, err := manager.InstallASRWithProgressOptions(r.Context(), nil, req.UpgradePackages)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":  err.Error(),
			"status": status,
		})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) saveUploadedAudio(r *http.Request) (string, func(), url.Values, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return "", func() {}, nil, err
	}
	form := url.Values{}
	audioPath := ""
	cleanup := func() {}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return "", func() {}, nil, err
		}
		name := part.FormName()
		if name == "" {
			_ = part.Close()
			continue
		}
		if name == "file" {
			if audioPath != "" {
				_ = part.Close()
				continue
			}
			path, err := s.saveUploadedAudioPart(part)
			_ = part.Close()
			if err != nil {
				cleanup()
				return "", func() {}, nil, err
			}
			audioPath = path
			cleanup = func() { _ = os.Remove(path) }
			continue
		}
		value, err := readAudioFormField(part)
		_ = part.Close()
		if err != nil {
			cleanup()
			return "", func() {}, nil, err
		}
		form.Add(name, value)
	}
	if audioPath == "" {
		return "", func() {}, nil, http.ErrMissingFile
	}
	return audioPath, cleanup, form, nil
}

func (s *Server) saveUploadedAudioPart(part interface {
	FileName() string
	io.Reader
}) (string, error) {
	tmpDir := s.cfg.TempDir()
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(part.FileName())
	if ext == "" {
		ext = ".audio"
	}
	tmp, err := os.CreateTemp(tmpDir, "asr-*"+ext)
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, part); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func readAudioFormField(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxAudioFormFieldBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxAudioFormFieldBytes {
		return "", http.ErrBodyReadAfterClose
	}
	return string(data), nil
}

func normalizeAudioResponseFormat(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "json"
	}
	return value
}

func parseAudioHotwords(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") {
		var out []string
		if json.Unmarshal([]byte(value), &out) == nil {
			return out
		}
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseAudioStream(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
