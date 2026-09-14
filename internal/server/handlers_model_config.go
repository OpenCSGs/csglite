package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

// minModelNumCtx mirrors the lower bound the resolver already enforces for a
// context window; anything below it is treated as "not set".
const minModelNumCtx = 1024

// minModelNumParallel mirrors the lower bound llama-server itself enforces for
// the slot count; anything below it is treated as "not set".
const minModelNumParallel = 1

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

// modelNumParallelSetting returns the per-model slot count saved for modelID,
// or 0 when the model has no setting of its own.
func (s *Server) modelNumParallelSetting(modelID string) int {
	if s.cfg == nil || len(s.cfg.Inference.ModelNumParallel) == 0 {
		return 0
	}
	storageID := s.resolveLocalModelStorageID(modelID)
	if numParallel, ok := s.cfg.Inference.ModelNumParallel[storageID]; ok && numParallel >= minModelNumParallel {
		return numParallel
	}
	return 0
}

// modelDTypeSetting returns the per-model GGUF quantization saved for modelID,
// or "" when the model has no setting of its own.
func (s *Server) modelDTypeSetting(modelID string) string {
	if s.cfg == nil || len(s.cfg.Inference.ModelDType) == 0 {
		return ""
	}
	storageID := s.resolveLocalModelStorageID(modelID)
	return strings.TrimSpace(s.cfg.Inference.ModelDType[storageID])
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

	// dtype is optional for the same reason as num_parallel; an empty string
	// clears it and returns the model to the repository default.
	normalizedDType := ""
	if req.DType != nil {
		normalizedDType, err = convert.NormalizeRuntimeDType(*req.DType)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// num_parallel is optional so that a client which only knows about the
	// context window keeps the slot count it already saved.
	numParallel := 0
	if req.NumParallel != nil {
		numParallel = *req.NumParallel
		if numParallel != 0 && numParallel < minModelNumParallel {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("num_parallel must be 0 to clear the setting, or at least %d", minModelNumParallel))
			return
		}
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
	if req.NumParallel != nil {
		if numParallel == 0 {
			delete(s.cfg.Inference.ModelNumParallel, storageID)
		} else {
			if s.cfg.Inference.ModelNumParallel == nil {
				s.cfg.Inference.ModelNumParallel = make(map[string]int)
			}
			s.cfg.Inference.ModelNumParallel[storageID] = numParallel
		}
	}
	if req.DType != nil {
		if normalizedDType == "" {
			delete(s.cfg.Inference.ModelDType, storageID)
		} else {
			if s.cfg.Inference.ModelDType == nil {
				s.cfg.Inference.ModelDType = make(map[string]string)
			}
			s.cfg.Inference.ModelDType[storageID] = normalizedDType
		}
	}
	if err := config.Save(s.cfg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, s.modelConfigResponse(modelID, modelDir))
}

func (s *Server) modelConfigResponse(modelID, modelDir string) api.ModelConfigResponse {
	setting := s.modelNumCtxSetting(modelID)
	parallelSetting := s.modelNumParallelSetting(modelID)
	return api.ModelConfigResponse{
		Model:                modelID,
		NumCtx:               setting,
		ModelMaxNumCtx:       inference.ModelMaxPositionEmbeddings(modelDir),
		GlobalNumCtx:         inference.ResolveNumCtxWithModelMax(modelDir, 0, s.cfg.Inference.LlamaUseModelMaxCtx),
		EffectiveNumCtx:      inference.ResolveNumCtxWithModelSetting(modelDir, 0, setting, s.cfg.Inference.LlamaUseModelMaxCtx),
		NumParallel:          parallelSetting,
		GlobalNumParallel:    inference.ResolveNumParallel(s.cfg.Inference.LlamaNumParallel),
		EffectiveNumParallel: inference.ResolveNumParallelWithModelSetting(0, parallelSetting, s.cfg.Inference.LlamaNumParallel),
		DType:                s.modelDTypeSetting(modelID),
		Runtime:              s.modelRuntimeKind(modelID),
	}
}

// modelRuntimeKind reports which runtime serves a model. A caller cannot work
// this out from the pipeline tag alone: an embedding model runs on llama.cpp
// when its weights convert to GGUF and in the Python embedding runtime when
// they do not, and the two accept completely different load options.
func (s *Server) modelRuntimeKind(modelID string) string {
	switch {
	case s.modelUsesTTSEngine(modelID):
		return api.ModelRuntimePythonTTS
	case s.modelUsesASREngine(modelID):
		return api.ModelRuntimePythonASR
	case s.modelUsesImageGenerationEngine(modelID):
		return api.ModelRuntimeDiffusers
	case s.shouldUsePythonEmbeddingRuntime(modelID):
		return api.ModelRuntimePythonEmbedding
	default:
		return api.ModelRuntimeLlama
	}
}
