package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/decision"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
)

// decisionProfileForModel resolves the typed-decision profile of a local
// model. It returns an error suitable for a 400 response when the model is
// not a typed-decision model or its System 1 block is not downloaded.
func (s *Server) decisionProfileForModel(modelID string) (*decision.Profile, error) {
	lm, err := s.manager.ResolveLocalModel(modelID)
	if err != nil {
		return nil, fmt.Errorf("typed decisions need a downloaded local model: %w", err)
	}
	dir, err := s.manager.ModelPath(lm.FullName())
	if err != nil {
		return nil, err
	}
	if decisionType := s.nativeDecisionTypeAt(lm, dir); decisionType != "" {
		return nil, fmt.Errorf("model %q is a native %s decision model: ask it through POST /v1/decisions", modelID, decisionType)
	}
	if !decision.IsDecisionModel(dir) {
		return nil, fmt.Errorf("model %q is not a typed-decision model (its model card has no jev or typed-decisions tag)", modelID)
	}
	return decision.FindProfile(dir, s.locateLocalRepo)
}

// locateLocalRepo finds a downloaded model by repository (namespace/name),
// whichever hub it came from.
func (s *Server) locateLocalRepo(repo string) (string, bool) {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	models, err := s.manager.List()
	if err != nil || repo == "" {
		return "", false
	}
	for _, lm := range models {
		if lm == nil {
			continue
		}
		name := strings.Trim(strings.TrimSpace(lm.Repository), "/")
		if name == "" {
			name = lm.Namespace + "/" + lm.Name
		}
		if !strings.EqualFold(name, repo) {
			continue
		}
		if dir, err := s.manager.ModelPath(lm.FullName()); err == nil {
			return dir, true
		}
	}
	return "", false
}

// resolveDecisionLoRA is the inference.DecisionLoRAResolver: llama engines
// for typed-decision models load the converted System 1 adapter.
func (s *Server) resolveDecisionLoRA(modelName string) (string, error) {
	lm, err := s.manager.ResolveLocalModel(modelName)
	if err != nil {
		return "", nil
	}
	dir, err := s.manager.ModelPath(lm.FullName())
	if err != nil || !decision.IsDecisionModel(dir) || s.nativeDecisionTypeAt(lm, dir) != "" {
		return "", nil
	}
	profile, err := decision.FindProfile(dir, s.locateLocalRepo)
	if err != nil {
		return "", err
	}
	return profile.EnsureLoRA(filepath.Join(s.cfg.StorageDir(), "cache", "decision-lora"))
}

// POST /v1/decide -- JEV-style typed decision (System 1).
func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	var req decision.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	profile, err := s.decisionProfileForModel(req.Model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	question, err := profile.Prepare(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	scorer, err := s.decisionScorer(r, req.Model, req.Source)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	defer s.touchEngine(req.Model)

	resp, err := question.Run(r.Context(), scorer, req.Model)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	s.recordAPIUsage(r, req.Model, req.Source, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	writeJSON(w, http.StatusOK, resp)
}

// POST /v1/decisions -- OpenAI Decisions API (predicate, choice and score
// questions) answered by a typed-decision model's System 1 head.
func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	var req decision.OpenAIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	// A downloaded model always wins over a cloud model of the same name.
	if lm, err := s.manager.ResolveLocalModel(req.Model); err == nil {
		s.handleLocalDecisions(w, r, req, lm)
		return
	}
	cloudModel, err := s.isCloudTypedDecisionModel(r.Context(), req.Model)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	if cloudModel {
		s.handleCloudDecisions(w, r, req)
		return
	}
	writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
		fmt.Sprintf("model %q is not a typed-decision model", req.Model))
}

// handleLocalDecisions answers a Decisions request with a downloaded model:
// a native decision model reads the answers from its llama.cpp head, other
// typed-decision models run their System 1 adapter.
func (s *Server) handleLocalDecisions(w http.ResponseWriter, r *http.Request, req decision.OpenAIRequest, lm *model.LocalModel) {
	if dir, err := s.manager.ModelPath(lm.FullName()); err == nil && s.nativeDecisionTypeAt(lm, dir) != "" {
		s.handleNativeDecisions(w, r, req)
		return
	}
	profile, err := s.decisionProfileForModel(req.Model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	questions, err := profile.PrepareOpenAI(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	scorer, err := s.decisionScorer(r, req.Model, req.Source)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	defer s.touchEngine(req.Model)

	resp, err := questions.Run(r.Context(), scorer, req.Model)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	s.recordAPIUsage(r, req.Model, req.Source, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	writeJSON(w, http.StatusOK, resp)
}

// handleNativeDecisions answers an OpenAI Decisions request with a native
// decision model (Laya, OpenJev, ...): llama-server reads the answers from the
// head stored in the GGUF at /v1/systemone.
func (s *Server) handleNativeDecisions(w http.ResponseWriter, r *http.Request, req decision.OpenAIRequest) {
	questions, err := decision.PrepareNativeOpenAI(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	eng, err := s.getChatEngine(r.Context(), req.Model, req.Source, 0, 0, -1, "", "", "")
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	defer s.touchEngine(req.Model)
	answerer, ok := eng.(inference.SystemOneAnswerer)
	if !ok || answerer.NativeDecisionType() == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("model %q did not load as a native decision model; its llama.cpp engine may be too old", req.Model))
		return
	}
	data, err := answerer.SystemOne(r.Context(), questions.Body())
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	resp, err := questions.Answer(data, req.Model)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	s.recordAPIUsage(r, req.Model, req.Source, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	writeJSON(w, http.StatusOK, resp)
}

// handleCloudDecisions forwards a Decisions request to the gateway's System
// One endpoint for a cloud typed-decision model and translates the answer
// back to the OpenAI Decisions response.
func (s *Server) handleCloudDecisions(w http.ResponseWriter, r *http.Request, req decision.OpenAIRequest) {
	questions, err := decision.PrepareNativeOpenAI(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	apiKey, err := s.cloudAPIKey(r.Context())
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	data, err := s.cloud.SystemOne(r.Context(), apiKey, s.resolveCloudOriginalModelID(req.Model), questions.Body())
	if err != nil {
		writeOpenAIInferenceError(w, toInferenceError(err))
		return
	}
	resp, err := questions.Answer(data, req.Model)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	s.recordAPIUsage(r, req.Model, req.Source, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	writeJSON(w, http.StatusOK, resp)
}

// isCloudTypedDecisionModel reports whether modelID is a cloud model the
// gateway advertises as a typed-decision model. The model list is refreshed
// (throttled to once per 30s) so a gateway-side task change is picked up
// without restarting the server.
func (s *Server) isCloudTypedDecisionModel(ctx context.Context, modelID string) (bool, error) {
	if s == nil || s.cloud == nil {
		return false, nil
	}
	models, err := s.listCloudModels(ctx, true)
	if err != nil {
		return false, err
	}
	modelID = strings.TrimSpace(modelID)
	for _, item := range models {
		if !strings.EqualFold(strings.TrimSpace(item.Source), "cloud") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Model), modelID) {
			return item.TypedDecisions, nil
		}
	}
	return false, nil
}

// toInferenceError translates a cloud gateway error into an inference error so
// the OpenAI handler reports the upstream status instead of a generic 500.
func toInferenceError(err error) error {
	var statusErr *cloud.StatusError
	if !errors.As(err, &statusErr) {
		return err
	}
	if statusErr.StatusCode == http.StatusNotFound {
		return inference.NewHTTPStatusError(http.StatusNotFound,
			"this model needs a newer AIGateway that serves /v1/systemone")
	}
	return inference.NewHTTPStatusError(statusErr.StatusCode, statusErr.Message())
}

func (s *Server) nativeDecisionTypeAt(lm *model.LocalModel, dir string) string {
	return s.localModelMetadata(lm.FullName(), dir, strings.TrimSpace(lm.PipelineTag), lm.Format).DecisionType
}

// GET /v1/decide/info?model=... -- typed-decision capabilities of a model.
func (s *Server) handleDecideInfo(w http.ResponseWriter, r *http.Request) {
	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	if modelID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model query parameter is required")
		return
	}
	profile, err := s.decisionProfileForModel(modelID)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profile.Info(modelID))
}

// decisionScorer returns the model's loaded engine as a decision scorer. An
// engine started before the System 1 block was downloaded has no adapter, so
// it is reloaded once.
func (s *Server) decisionScorer(r *http.Request, modelID, source string) (inference.DecisionScorer, error) {
	for attempt := 0; attempt < 2; attempt++ {
		eng, err := s.getChatEngine(r.Context(), modelID, source, 0, 0, -1, "", "", "")
		if err != nil {
			return nil, err
		}
		scorer, ok := eng.(inference.DecisionScorer)
		if !ok {
			return nil, inference.NewHTTPStatusError(http.StatusBadRequest, "typed decisions run on local llama.cpp models only")
		}
		if scorer.HasDecisionAdapter() {
			return scorer, nil
		}
		log.Printf("DECIDE %s: loaded engine has no typed-decision adapter; reloading", modelID)
		s.closeEngineKey(engineCacheKey(s.resolveLocalModelStorageID(modelID), engineModeChat))
	}
	return nil, inference.NewHTTPStatusError(http.StatusServiceUnavailable, "the typed-decision adapter could not be loaded; see the server log")
}
