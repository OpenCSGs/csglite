package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/imagegen"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const maxAudioUploadMemory = 32 << 20

// POST /v1/audio/transcriptions -- OpenAI-compatible local audio transcription.
func (s *Server) handleOpenAIAudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxAudioUploadMemory); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid multipart request: "+err.Error())
		return
	}
	modelID := strings.TrimSpace(r.FormValue("model"))
	if modelID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	audioPath, cleanup, err := s.saveUploadedAudio(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	defer cleanup()

	req := api.OpenAIAudioTranscriptionRequest{
		Model:          modelID,
		FilePath:       audioPath,
		Language:       strings.TrimSpace(r.FormValue("language")),
		Prompt:         strings.TrimSpace(r.FormValue("prompt")),
		ResponseFormat: normalizeAudioResponseFormat(r.FormValue("response_format")),
		Hotwords:       parseAudioHotwords(r.FormValue("hotwords")),
	}
	if value := strings.TrimSpace(r.FormValue("temperature")); value != "" {
		temperature, err := strconv.ParseFloat(value, 64)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "temperature must be a number")
			return
		}
		req.Temperature = &temperature
	}
	if value := strings.TrimSpace(r.FormValue("itn")); value != "" {
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

// GET /api/asr-runtime -- report the shared Python runtime ASR package status.
func (s *Server) handleASRRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	manager, err := imagegen.NewRuntimeManager()
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
	manager, err := imagegen.NewRuntimeManager()
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

func (s *Server) saveUploadedAudio(r *http.Request) (string, func(), error) {
	file, header, err := r.FormFile("file")
	if err != nil {
		return "", func() {}, err
	}
	defer file.Close()

	tmpDir := s.cfg.TempDir()
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", func() {}, err
	}
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".audio"
	}
	tmp, err := os.CreateTemp(tmpDir, "asr-*"+ext)
	if err != nil {
		return "", func() {}, err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, file); err != nil {
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
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
