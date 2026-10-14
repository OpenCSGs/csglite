package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/decision"
	"github.com/opencsgs/csglite/internal/inference"
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
	if err != nil || !decision.IsDecisionModel(dir) {
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
