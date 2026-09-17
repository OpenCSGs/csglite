package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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

// modelSettings returns the per-model load options saved for modelID, or the
// zero value when the model has none of its own.
func (s *Server) modelSettings(modelID string) config.ModelRuntimeSettings {
	if s.cfg == nil {
		return config.ModelRuntimeSettings{}
	}
	storageID := s.resolveLocalModelStorageID(modelID)
	s.modelSettingsMu.RLock()
	defer s.modelSettingsMu.RUnlock()
	return s.cfg.Inference.ModelSettings(storageID)
}

// modelNumCtxSetting returns the per-model context window saved for modelID,
// or 0 when the model has no setting of its own.
func (s *Server) modelNumCtxSetting(modelID string) int {
	if numCtx := s.modelSettings(modelID).NumCtx; numCtx >= minModelNumCtx {
		return numCtx
	}
	return 0
}

// modelNumParallelSetting returns the per-model slot count saved for modelID,
// or 0 when the model has no setting of its own.
func (s *Server) modelNumParallelSetting(modelID string) int {
	if numParallel := s.modelSettings(modelID).NumParallel; numParallel >= minModelNumParallel {
		return numParallel
	}
	return 0
}

// modelDTypeSetting returns the per-model GGUF quantization saved for modelID,
// or "" when the model has no setting of its own.
func (s *Server) modelDTypeSetting(modelID string) string {
	return strings.TrimSpace(s.modelSettings(modelID).DType)
}

// modelKeepAliveSetting returns the per-model idle window saved for modelID and
// whether the model has one. An unparsable saved value is reported as absent
// rather than failing the load it is resolved for.
func (s *Server) modelKeepAliveSetting(modelID string) (time.Duration, bool) {
	keepAlive, set, err := api.ParseKeepAlive(s.modelSettings(modelID).KeepAlive)
	if err != nil || !set {
		return 0, false
	}
	return keepAlive, true
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
	// Every field is optional so that a client which knows about only some of
	// them keeps the rest of the model's saved settings: a model served by a
	// Python runtime has a keep-alive but no context window to send along.
	numCtx := 0
	if req.NumCtx != nil {
		numCtx = *req.NumCtx
		if numCtx != 0 && numCtx < minModelNumCtx {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("num_ctx must be 0 to clear the setting, or at least %d", minModelNumCtx))
			return
		}
	}

	// keep_alive is stored in the spelling it arrived in, but parsing it here
	// keeps an unusable value out of the config: it is resolved on every later
	// load, where there is no caller left to report the error to.
	normalizedKeepAlive := ""
	if req.KeepAlive != nil {
		keepAlive, set, err := api.ParseKeepAlive(*req.KeepAlive)
		if err != nil {
			writeError(w, http.StatusBadRequest, "keep_alive "+err.Error())
			return
		}
		if set {
			normalizedKeepAlive = api.FormatKeepAlive(keepAlive)
		}
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
		// Refuse a quantization this model cannot actually serve. A saved dtype
		// is applied to every later load that names none, so an unservable one
		// would either start a SafeTensors conversion inside an ordinary chat
		// request or quietly load the repository default while the cache entry
		// claims otherwise.
		if normalizedDType != "" {
			_, haveGGUF, err := convert.FindGGUFForDType(modelDir, normalizedDType)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !haveGGUF && !convert.HasConvertibleHFWeights(modelDir) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("model %q has no %s build and no weights to convert into one", modelID, normalizedDType))
				return
			}
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
	s.modelSettingsMu.Lock()
	settings := s.cfg.Inference.ModelSettings(storageID)
	if req.NumCtx != nil {
		settings.NumCtx = numCtx
	}
	if req.NumParallel != nil {
		settings.NumParallel = numParallel
	}
	if req.DType != nil {
		settings.DType = normalizedDType
	}
	if req.KeepAlive != nil {
		settings.KeepAlive = normalizedKeepAlive
	}
	s.cfg.Inference.SetModelSettings(storageID, settings)
	s.modelSettingsMu.Unlock()
	if err := config.Save(s.cfg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	// An engine already loaded keeps running, so apply the new window to it
	// instead of making the user reload the model to change when it expires.
	if req.KeepAlive != nil {
		s.applyModelKeepAlive(modelID, s.effectiveModelKeepAlive(modelID))
	}

	writeJSON(w, http.StatusOK, s.modelConfigResponse(modelID, modelDir))
}

func (s *Server) modelConfigResponse(modelID, modelDir string) api.ModelConfigResponse {
	setting := s.modelNumCtxSetting(modelID)
	parallelSetting := s.modelNumParallelSetting(modelID)
	keepAliveSetting, keepAliveSet := s.modelKeepAliveSetting(modelID)
	savedKeepAlive := ""
	if keepAliveSet {
		savedKeepAlive = api.FormatKeepAlive(keepAliveSetting)
	}
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
		KeepAlive:            savedKeepAlive,
		GlobalKeepAlive:      api.FormatKeepAlive(s.defaultKeepAliveFor(modelID)),
		EffectiveKeepAlive:   api.FormatKeepAlive(s.effectiveModelKeepAlive(modelID)),
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
