package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/imagegen"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

// POST /v1/images/generations -- OpenAI-compatible local image generation.
func (s *Server) handleOpenAIImagesGenerations(w http.ResponseWriter, r *http.Request) {
	var req api.OpenAIImagesGenerationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	if errMsg := normalizeOpenAIImagesGenerationRequest(&req); errMsg != "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", errMsg)
		return
	}

	eng, err := s.getOrLoadImageEngine(r.Context(), req.Model)
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
	resp, err := eng.Generate(r.Context(), req)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if resp.Created == 0 {
		resp.Created = time.Now().Unix()
	}

	s.mu.Lock()
	if me, ok := s.imageEngines[req.Model]; ok {
		me.lastUsed = time.Now()
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, resp)
}

func normalizeOpenAIImagesGenerationRequest(req *api.OpenAIImagesGenerationRequest) string {
	req.Model = strings.TrimSpace(req.Model)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Model == "" {
		return "model is required"
	}
	if req.Prompt == "" {
		return "prompt is required"
	}
	if req.N != nil && (*req.N < 1 || *req.N > 4) {
		return "n must be between 1 and 4"
	}
	if req.ResponseFormat != "" && req.ResponseFormat != "b64_json" {
		return "only response_format=b64_json is supported for local image generation"
	}
	return ""
}

// GET /api/image-runtime -- report the lazy Diffusers runtime status.
func (s *Server) handleImageRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	manager, err := imagegen.NewRuntimeManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, manager.Status(r.Context()))
}

// POST /api/image-runtime/install -- install or repair the Diffusers runtime.
func (s *Server) handleImageRuntimeInstall(w http.ResponseWriter, r *http.Request) {
	var req api.ImageRuntimeInstallRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	manager, err := imagegen.NewRuntimeManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, err := manager.InstallWithProgressOptions(r.Context(), nil, req.UpgradePackages)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":  err.Error(),
			"status": status,
		})
		return
	}
	writeJSON(w, http.StatusOK, status)
}
