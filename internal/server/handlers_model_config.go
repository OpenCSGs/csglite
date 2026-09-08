package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

// minModelNumCtx mirrors the lower bound the resolver already enforces for a
// context window; anything below it is treated as "not set".
const minModelNumCtx = 1024

// modelNumCtxSetting returns the per-model context window saved for modelID,
// or 0 when the model has no setting of its own.
func (s *Server) modelNumCtxSetting(modelID string) int {
	if s.cfg == nil || len(s.cfg.Inference.ModelNumCtx) == 0 {
		return 0
	}
	storageID := s.resolveLocalModelStorageID(modelID)
	if numCtx, ok := s.cfg.Inference.ModelNumCtx[storageID]; ok && numCtx >= minModelNumCtx {
		return numCtx
	}
	return 0
}

// GET /api/models/{namespace}/{name}/config
func (s *Server) handleModelConfig(w http.ResponseWriter, r *http.Request) {
	s.handleModelConfigForID(w, modelIDFromPathValues(r))
}

// GET /api/models/{model}/config
func (s *Server) handleModelConfigByPublicID(w http.ResponseWriter, r *http.Request) {
	modelID, err := s.manager.ResolveLocalModelID(strings.TrimSpace(r.PathValue("model")))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.handleModelConfigForID(w, modelID)
}

// PUT /api/models/{namespace}/{name}/config
func (s *Server) handleModelConfigUpdate(w http.ResponseWriter, r *http.Request) {
	s.handleModelConfigUpdateForID(w, r, modelIDFromPathValues(r))
}

// PUT /api/models/{model}/config
func (s *Server) handleModelConfigUpdateByPublicID(w http.ResponseWriter, r *http.Request) {
	modelID, err := s.manager.ResolveLocalModelID(strings.TrimSpace(r.PathValue("model")))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.handleModelConfigUpdateForID(w, r, modelID)
}

func (s *Server) handleModelConfigForID(w http.ResponseWriter, modelID string) {
	w.Header().Set("Cache-Control", "no-cache")

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", modelID))
		return
	}
	writeJSON(w, http.StatusOK, s.modelConfigResponse(modelID, modelDir))
}

func (s *Server) handleModelConfigUpdateForID(w http.ResponseWriter, r *http.Request, modelID string) {
	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", modelID))
		return
	}

	var req api.ModelConfigUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.NumCtx == nil {
		writeError(w, http.StatusBadRequest, "num_ctx is required; send 0 to clear the setting")
		return
	}
	numCtx := *req.NumCtx
	if numCtx != 0 && numCtx < minModelNumCtx {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("num_ctx must be 0 to clear the setting, or at least %d", minModelNumCtx))
		return
	}

	storageID := s.resolveLocalModelStorageID(modelID)
	if numCtx == 0 {
		delete(s.cfg.Inference.ModelNumCtx, storageID)
	} else {
		if s.cfg.Inference.ModelNumCtx == nil {
			s.cfg.Inference.ModelNumCtx = make(map[string]int)
		}
		s.cfg.Inference.ModelNumCtx[storageID] = numCtx
	}
	if err := config.Save(s.cfg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, s.modelConfigResponse(modelID, modelDir))
}

func (s *Server) modelConfigResponse(modelID, modelDir string) api.ModelConfigResponse {
	setting := s.modelNumCtxSetting(modelID)
	return api.ModelConfigResponse{
		Model:           modelID,
		NumCtx:          setting,
		ModelMaxNumCtx:  inference.ModelMaxPositionEmbeddings(modelDir),
		GlobalNumCtx:    inference.ResolveNumCtxWithModelMax(modelDir, 0, s.cfg.Inference.LlamaUseModelMaxCtx),
		EffectiveNumCtx: inference.ResolveNumCtxWithModelSetting(modelDir, 0, setting, s.cfg.Inference.LlamaUseModelMaxCtx),
	}
}
