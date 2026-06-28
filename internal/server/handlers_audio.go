package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/opencsgs/csglite/internal/asr"
	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
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
	stream := parseAudioStream(form.Get("stream")) || requestWantsSSE(r)

	req := api.OpenAIAudioTranscriptionRequest{
		Model:          modelID,
		FilePath:       audioPath,
		Source:         strings.TrimSpace(form.Get("source")),
		Language:       strings.TrimSpace(form.Get("language")),
		Prompt:         strings.TrimSpace(form.Get("prompt")),
		ResponseFormat: normalizeAudioResponseFormat(form.Get("response_format")),
		Stream:         stream,
		Hotwords:       parseAudioHotwords(form.Get("hotwords")),
	}
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
	if audioTranscriptionUsesCloud(req) {
		if stream {
			if err := s.streamCloudAudioTranscription(w, r.Context(), req); err != nil {
				log.Printf("MODEL %s: cloud ASR stream proxy failed: %v", req.Model, err)
			}
			return
		}
		resp, err := s.transcribeCloudAudio(r.Context(), req)
		if err != nil {
			writeOpenAIInferenceError(w, err)
			return
		}
		writeAudioTranscriptionResponse(w, req, resp)
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
	writeAudioTranscriptionResponse(w, req, resp)
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

func writeAudioTranscriptionResponse(w http.ResponseWriter, req api.OpenAIAudioTranscriptionRequest, resp *api.OpenAIAudioTranscriptionResponse) {
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

func writeCloudAudioTranscriptionStream(w http.ResponseWriter, resp *api.OpenAIAudioTranscriptionResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	writeSSE(w, map[string]interface{}{
		"text":     resp.Text,
		"response": resp,
		"done":     true,
	})
}

func audioTranscriptionUsesCloud(req api.OpenAIAudioTranscriptionRequest) bool {
	return strings.EqualFold(strings.TrimSpace(req.Source), "cloud")
}

func (s *Server) transcribeCloudAudio(ctx context.Context, req api.OpenAIAudioTranscriptionRequest) (*api.OpenAIAudioTranscriptionResponse, error) {
	httpReq, err := s.newCloudAudioTranscriptionRequest(ctx, req, "application/json")
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("cloud audio transcription request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading cloud audio transcription response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(respBody))
		log.Printf("MODEL %s: cloud ASR transcription failed: status=%d content_type=%q body=%q", req.Model, resp.StatusCode, resp.Header.Get("Content-Type"), truncateLogString(message, 2048))
		return nil, inference.NewHTTPStatusError(resp.StatusCode, message)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		return &api.OpenAIAudioTranscriptionResponse{Text: string(respBody)}, nil
	}
	var out api.OpenAIAudioTranscriptionResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		log.Printf("MODEL %s: cloud ASR response decode failed: content_type=%q body=%q err=%v", req.Model, resp.Header.Get("Content-Type"), truncateLogString(strings.TrimSpace(string(respBody)), 2048), err)
		return nil, fmt.Errorf("decoding cloud audio transcription response: %w", err)
	}
	return &out, nil
}

func (s *Server) streamCloudAudioTranscription(w http.ResponseWriter, ctx context.Context, req api.OpenAIAudioTranscriptionRequest) error {
	req.Stream = true
	httpReq, err := s.newCloudAudioTranscriptionRequest(ctx, req, "text/event-stream")
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("cloud audio transcription request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("reading cloud audio transcription error response: %w", readErr)
		}
		message := strings.TrimSpace(string(respBody))
		log.Printf("MODEL %s: cloud ASR stream transcription failed: status=%d content_type=%q body=%q", req.Model, resp.StatusCode, resp.Header.Get("Content-Type"), truncateLogString(message, 2048))
		return inference.NewHTTPStatusError(resp.StatusCode, message)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	contentType := resp.Header.Get("Content-Type")
	log.Printf("MODEL %s: cloud ASR stream response: content_type=%q transfer_encoding=%v", req.Model, contentType, resp.TransferEncoding)
	if !strings.Contains(contentType, "text/event-stream") {
		if strings.HasPrefix(contentType, "text/plain") {
			return streamCloudPlainTextAudioTranscription(w, resp.Body)
		}
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("reading cloud audio transcription response: %w", readErr)
		}
		var out api.OpenAIAudioTranscriptionResponse
		if err := json.Unmarshal(respBody, &out); err != nil {
			log.Printf("MODEL %s: cloud ASR stream response decode failed: content_type=%q body=%q err=%v", req.Model, contentType, truncateLogString(strings.TrimSpace(string(respBody)), 2048), err)
			return fmt.Errorf("decoding cloud audio transcription response: %w", err)
		}
		writeCloudAudioTranscriptionStream(w, &out)
		return nil
	}

	var fullText strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxAudioFormFieldBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		text, done := cloudAudioStreamChunkText(payload)
		if text != "" {
			fullText.WriteString(text)
			writeSSE(w, map[string]interface{}{
				"text": text,
				"done": false,
			})
		}
		if done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading cloud audio transcription stream: %w", err)
	}
	writeSSE(w, map[string]interface{}{
		"text": fullText.String(),
		"done": true,
	})
	return nil
}

func streamCloudPlainTextAudioTranscription(w http.ResponseWriter, r io.Reader) error {
	var fullText strings.Builder
	pending := make([]byte, 0, utf8.UTFMax)
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunkBytes := append(pending, buf[:n]...)
			emitLen := validUTF8PrefixLength(chunkBytes)
			if emitLen > 0 {
				text := strings.ToValidUTF8(string(chunkBytes[:emitLen]), "")
				if text != "" {
					fullText.WriteString(text)
					writeSSE(w, map[string]interface{}{
						"text": text,
						"done": false,
					})
				}
			}
			pending = append(pending[:0], chunkBytes[emitLen:]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading cloud audio transcription text stream: %w", err)
		}
	}
	if len(pending) > 0 {
		text := strings.ToValidUTF8(string(pending), "")
		if text != "" {
			fullText.WriteString(text)
			writeSSE(w, map[string]interface{}{
				"text": text,
				"done": false,
			})
		}
	}
	writeSSE(w, map[string]interface{}{
		"text": fullText.String(),
		"done": true,
	})
	return nil
}

func validUTF8PrefixLength(data []byte) int {
	i := 0
	for i < len(data) {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && !utf8.FullRune(data[i:]) {
			break
		}
		i += size
	}
	return i
}

func (s *Server) newCloudAudioTranscriptionRequest(ctx context.Context, req api.OpenAIAudioTranscriptionRequest, accept string) (*http.Request, error) {
	apiKey, err := s.cloudAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	req.Model = s.resolveCloudOriginalModelID(req.Model)
	baseURL := resolveCloudURL(s.cfg)
	if s.cloud != nil && strings.TrimSpace(s.cloud.BaseURL()) != "" {
		baseURL = s.cloud.BaseURL()
	}

	body, contentType, err := buildCloudAudioTranscriptionBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/audio/transcriptions", body)
	if err != nil {
		return nil, fmt.Errorf("creating cloud audio transcription request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", accept)
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	return httpReq, nil
}

func cloudAudioStreamChunkText(payload string) (string, bool) {
	var value interface{}
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return payload, false
	}
	return cloudAudioStreamValueText(value)
}

func cloudAudioStreamValueText(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, false
	case map[string]interface{}:
		done, _ := v["done"].(bool)
		if text, ok := v["text"].(string); ok {
			return text, done
		}
		if response, ok := v["response"].(map[string]interface{}); ok {
			if text, ok := response["text"].(string); ok {
				return text, done
			}
		}
		if delta, ok := v["delta"].(string); ok {
			return delta, done
		}
		if delta, ok := v["delta"].(map[string]interface{}); ok {
			if text, ok := delta["content"].(string); ok {
				return text, done
			}
		}
		if choices, ok := v["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				return cloudAudioStreamValueText(choice)
			}
		}
	}
	return "", false
}

func truncateLogString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...(truncated)"
}

func buildCloudAudioTranscriptionBody(req api.OpenAIAudioTranscriptionRequest) (io.Reader, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("model", req.Model); err != nil {
		return nil, "", err
	}
	if req.Language != "" {
		if err := writer.WriteField("language", req.Language); err != nil {
			return nil, "", err
		}
	}
	if req.Prompt != "" {
		if err := writer.WriteField("prompt", req.Prompt); err != nil {
			return nil, "", err
		}
	}
	responseFormat := strings.TrimSpace(req.ResponseFormat)
	if responseFormat == "" {
		responseFormat = "json"
	}
	if err := writer.WriteField("response_format", responseFormat); err != nil {
		return nil, "", err
	}
	if req.Stream {
		if err := writer.WriteField("stream", "true"); err != nil {
			return nil, "", err
		}
	}
	if req.Temperature != nil {
		if err := writer.WriteField("temperature", strconv.FormatFloat(*req.Temperature, 'f', -1, 64)); err != nil {
			return nil, "", err
		}
	}

	file, err := os.Open(req.FilePath)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file", filepath.Base(req.FilePath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return &buf, writer.FormDataContentType(), nil
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
