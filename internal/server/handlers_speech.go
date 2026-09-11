package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/opencsgs/csglite/internal/tts"
	"github.com/opencsgs/csglite/pkg/api"
)

// speechContentTypes maps a response_format to the media type the client is
// told it is receiving.
var speechContentTypes = map[string]string{
	"mp3":  "audio/mpeg",
	"wav":  "audio/wav",
	"opus": "audio/opus",
	"flac": "audio/flac",
	"aac":  "audio/aac",
	"pcm":  "audio/L16",
}

func speechContentType(format string, sampleRate int) string {
	mediaType := speechContentTypes[format]
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	if format == "pcm" && sampleRate > 0 {
		return fmt.Sprintf("%s; rate=%d; channels=1", mediaType, sampleRate)
	}
	return mediaType
}

// POST /v1/audio/speech -- OpenAI-compatible speech synthesis served by the
// local Python text-to-speech runtime.
func (s *Server) handleOpenAIAudioSpeech(w http.ResponseWriter, r *http.Request) {
	var req api.OpenAIAudioSpeechRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if strings.TrimSpace(req.Input) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "input is required")
		return
	}
	req.ResponseFormat = strings.ToLower(strings.TrimSpace(req.ResponseFormat))
	if req.ResponseFormat == "" {
		req.ResponseFormat = "mp3"
	}
	if _, ok := speechContentTypes[req.ResponseFormat]; !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			"response_format must be mp3, wav, pcm, opus, flac, or aac")
		return
	}
	if req.Speed != nil && (*req.Speed < 0.25 || *req.Speed > 4.0) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "speed must be between 0.25 and 4.0")
		return
	}
	source, err := effectiveRequestSource(r.Context(), req.Source)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if providerIDFromSource(source) != "" {
		writeOpenAIError(w, http.StatusNotImplemented, "unsupported_error",
			"third-party provider speech synthesis is not supported")
		return
	}
	req.Source = source

	eng, err := s.getOrLoadTTSEngine(r.Context(), req.Model)
	if err != nil {
		writeSpeechEngineError(w, err)
		return
	}

	if req.Stream {
		s.streamAudioSpeech(w, r, eng, req)
		return
	}

	audio, err := eng.Speak(r.Context(), req)
	if err != nil {
		log.Printf("MODEL %s: speech synthesis failed: %v", req.Model, err)
		s.closeTTSEngine(req.Model)
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	s.touchTTSEngine(req.Model)
	w.Header().Set("Content-Type", speechContentType(req.ResponseFormat, audio.SampleRate))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(audio.Data)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(audio.Data); err != nil {
		log.Printf("MODEL %s: writing speech response failed: %v", req.Model, err)
	}
}

// streamAudioSpeech forwards audio as it is synthesised. The headers go out
// before the first chunk, so a failure mid-stream can only be signalled by
// ending the response early -- the status is already committed.
func (s *Server) streamAudioSpeech(w http.ResponseWriter, r *http.Request, eng tts.Engine, req api.OpenAIAudioSpeechRequest) {
	wrote := false
	flusher, canFlush := w.(http.Flusher)
	err := eng.SpeakStream(r.Context(), req, func(chunk tts.Chunk) error {
		if len(chunk.Data) == 0 {
			return nil
		}
		if !wrote {
			w.Header().Set("Content-Type", speechContentType(req.ResponseFormat, req.SampleRate))
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			wrote = true
		}
		if _, err := w.Write(chunk.Data); err != nil {
			return err
		}
		if canFlush {
			flusher.Flush()
		}
		return nil
	})
	if err != nil {
		log.Printf("MODEL %s: speech synthesis stream failed: %v", req.Model, err)
		s.closeTTSEngine(req.Model)
		if !wrote {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		}
		return
	}
	s.touchTTSEngine(req.Model)
	if !wrote {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "text-to-speech produced no audio")
	}
}

// writeSpeechEngineError reports a runtime that is not installed as a 503 with
// the status attached, the way the image and ASR paths do, so a client can
// offer to install it.
func writeSpeechEngineError(w http.ResponseWriter, err error) {
	if status, ok := imagegen.RuntimeStatusFromError(err); ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error":     err.Error(),
			"errorCode": http.StatusServiceUnavailable,
			"runtime":   status,
		})
		return
	}
	writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
}

// GET /api/tts-runtime -- report whether the Python text-to-speech runtime is usable.
func (s *Server) handleTTSRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	manager, err := imagegen.NewTTSRuntimeManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, manager.TTSStatus(r.Context()))
}

// POST /api/tts-runtime/install -- install or repair the text-to-speech runtime.
func (s *Server) handleTTSRuntimeInstall(w http.ResponseWriter, r *http.Request) {
	var req api.ASRRuntimeInstallRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	manager, err := imagegen.NewTTSRuntimeManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, err := manager.InstallTTSWithProgressOptions(r.Context(), nil, req.UpgradePackages)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":  err.Error(),
			"status": status,
		})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// GET /api/tts-voices?model=... -- list the voices a local text-to-speech model
// can render. OpenAI has a fixed voice enum; local models do not, so clients
// need a way to discover what a given model offers.
//
// The model id travels as a query parameter rather than in the path: a
// source-scoped id such as modelscope/hexgrad/Kokoro-82M has three
// slash-separated segments, which neither {model} nor {namespace}/{name} can
// match, and an unmatched GET falls through to the static handler and returns
// the web UI with status 200.
func (s *Server) handleTTSVoices(w http.ResponseWriter, r *http.Request) {
	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	if modelID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	eng, err := s.getOrLoadTTSEngine(r.Context(), modelID)
	if err != nil {
		writeSpeechEngineError(w, err)
		return
	}
	info, err := eng.Info(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.touchTTSEngine(modelID)
	writeJSON(w, http.StatusOK, info)
}
