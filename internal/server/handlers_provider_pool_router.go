package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	routerprofile "github.com/opencsgs/semantic-router"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	providerPoolRouterJSONLimit             = 32 << 10
	providerPoolRouterMinimumHistoryRecords = 20
)

func (s *Server) withRouterStoreRead(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.routerStoreMu.RLock()
		defer s.routerStoreMu.RUnlock()
		next(w, r)
	}
}

func (s *Server) handleProviderPoolRouterStatus(w http.ResponseWriter, r *http.Request) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return
	}
	store := s.routerProfileStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "router profile store is unavailable")
		return
	}
	status, err := providerPoolRouterStatus(r, store, pool)
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleProviderPoolRouterSuggestion(w http.ResponseWriter, r *http.Request) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return
	}
	store := s.routerProfileStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "router profile store is unavailable")
		return
	}
	values, err := store.ListSuggestions(r.Context(), pool.ID, "", routerprofile.ListOptions{Limit: 1})
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	if len(values) == 0 {
		writeError(w, http.StatusNotFound, "pending router evaluation suggestion not found")
		return
	}
	writeJSON(w, http.StatusOK, providerPoolRouterSuggestionAPI(values[0], pool))
}

func (s *Server) handleProviderPoolRouterEvaluationPreview(w http.ResponseWriter, r *http.Request) {
	pool, request, ok := decodeProviderPoolEvaluationRequest(w, r)
	if !ok {
		return
	}
	preview, err := s.PreviewProviderPoolEvaluation(r.Context(), request)
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	_ = pool
	writeJSON(w, http.StatusOK, providerPoolRouterEvaluationPreviewAPI(preview))
}

func (s *Server) handleProviderPoolRouterEvaluationCreate(w http.ResponseWriter, r *http.Request) {
	pool, request, ok := decodeProviderPoolEvaluationRequest(w, r)
	if !ok {
		return
	}
	store := s.routerProfileStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "router profile store is unavailable")
		return
	}
	historyCount, err := store.CountQuerySnapshots(r.Context(), pool.ID)
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	if historyCount < providerPoolRouterMinimumHistoryRecords {
		writeError(w, http.StatusBadRequest, "at least 20 historical records are required for evaluation")
		return
	}
	for _, status := range []routerprofile.JobStatus{routerprofile.JobQueued, routerprofile.JobRunning} {
		jobs, err := s.ListProviderPoolEvaluationJobs(r.Context(), pool.ID, status, routerprofile.ListOptions{Limit: 1})
		if err != nil {
			writeProviderPoolRouterError(w, err)
			return
		}
		if len(jobs) > 0 {
			writeError(w, http.StatusConflict, "provider pool already has an active evaluation job")
			return
		}
	}
	suggestionID := ""
	if suggestions, listErr := store.ListSuggestions(r.Context(), pool.ID, routerprofile.SuggestionPending, routerprofile.ListOptions{Limit: 1}); listErr == nil && len(suggestions) > 0 {
		suggestionID = suggestions[0].ID
		if err := store.UpdateSuggestionStatus(r.Context(), pool.ID, suggestionID, routerprofile.SuggestionEvaluating); err != nil {
			writeProviderPoolRouterError(w, err)
			return
		}
	}
	job, err := s.CreateProviderPoolEvaluationJob(r.Context(), request)
	if err != nil {
		if suggestionID != "" {
			_ = store.UpdateSuggestionStatus(r.Context(), pool.ID, suggestionID, routerprofile.SuggestionPending)
		}
		writeProviderPoolRouterError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, providerPoolRouterJobAPI(job, pool))
}

func (s *Server) handleProviderPoolRouterEvaluationList(w http.ResponseWriter, r *http.Request) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return
	}
	options, ok := providerPoolRouterListOptions(w, r)
	if !ok {
		return
	}
	status := routerprofile.JobStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	values, err := s.ListProviderPoolEvaluationJobs(r.Context(), pool.ID, status, options)
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	items := make([]api.ProviderPoolRouterEvaluationJob, 0, len(values))
	for _, value := range values {
		items = append(items, providerPoolRouterJobAPI(value, pool))
	}
	writeJSON(w, http.StatusOK, api.ProviderPoolRouterEvaluationJobsResponse{Items: items, Limit: options.Limit, Offset: options.Offset})
}

func (s *Server) handleProviderPoolRouterEvaluationGet(w http.ResponseWriter, r *http.Request) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return
	}
	job, _, err := s.GetProviderPoolEvaluationJob(r.Context(), pool.ID, strings.TrimSpace(r.PathValue("jobID")))
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, providerPoolRouterJobAPI(job, pool))
}

func (s *Server) handleProviderPoolRouterEvaluationCancel(w http.ResponseWriter, r *http.Request) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return
	}
	jobID := strings.TrimSpace(r.PathValue("jobID"))
	current, _, err := s.GetProviderPoolEvaluationJob(r.Context(), pool.ID, jobID)
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	if current.Status != routerprofile.JobQueued && current.Status != routerprofile.JobRunning {
		writeError(w, http.StatusConflict, "evaluation job is already terminal")
		return
	}
	if err := s.CancelProviderPoolEvaluationJob(r.Context(), pool.ID, jobID); err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	job, _, err := s.GetProviderPoolEvaluationJob(r.Context(), pool.ID, jobID)
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, providerPoolRouterJobAPI(job, pool))
}

func (s *Server) handleProviderPoolRouterProfileList(w http.ResponseWriter, r *http.Request) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return
	}
	options, ok := providerPoolRouterListOptions(w, r)
	if !ok {
		return
	}
	store := s.routerProfileStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "router profile store is unavailable")
		return
	}
	values, err := store.ListProfiles(r.Context(), pool.ID, options)
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	activeID := activeProviderPoolRouterProfileID(r, store, pool.ID)
	items := make([]api.ProviderPoolRouterProfile, 0, len(values))
	for _, value := range values {
		items = append(items, providerPoolRouterProfileAPI(value, pool, activeID, false))
	}
	writeJSON(w, http.StatusOK, api.ProviderPoolRouterProfilesResponse{Items: items, Limit: options.Limit, Offset: options.Offset})
}

func (s *Server) handleProviderPoolRouterProfileGet(w http.ResponseWriter, r *http.Request) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return
	}
	store := s.routerProfileStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "router profile store is unavailable")
		return
	}
	value, err := store.GetProfile(r.Context(), pool.ID, strings.TrimSpace(r.PathValue("profileID")))
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, providerPoolRouterProfileAPI(value, pool, activeProviderPoolRouterProfileID(r, store, pool.ID), true))
}

func (s *Server) handleProviderPoolRouterProfileActivate(w http.ResponseWriter, r *http.Request) {
	pool, audit, ok := decodeProviderPoolRouterAudit(w, r)
	if !ok {
		return
	}
	value, err := s.ActivateRouterProfile(r.Context(), pool.ID, strings.TrimSpace(r.PathValue("profileID")), audit.Actor, audit.Reason)
	if err != nil {
		writeProviderPoolRouterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, providerPoolRouterActivationAPI(value))
}

func (s *Server) handleProviderPoolRouterRollback(w http.ResponseWriter, r *http.Request) {
	pool, audit, ok := decodeProviderPoolRouterAudit(w, r)
	if !ok {
		return
	}
	value, err := s.RollbackRouterProfile(r.Context(), pool.ID, audit.ExpectedCurrentProfileID, audit.Actor, audit.Reason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusConflict, "no compatible router profile is available for rollback")
			return
		}
		writeProviderPoolRouterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, providerPoolRouterActivationAPI(value))
}

func (s *Server) routerProfileStore() *routerprofile.Store {
	s.routerProfileMu.RLock()
	defer s.routerProfileMu.RUnlock()
	return s.routerProfiles
}

func providerPoolForRouterRequest(w http.ResponseWriter, r *http.Request) (config.ProviderPool, bool) {
	pool, ok := providerPoolByID(strings.TrimSpace(r.PathValue("id")))
	if !ok {
		writeError(w, http.StatusNotFound, "provider pool not found")
		return config.ProviderPool{}, false
	}
	return pool, true
}

func decodeProviderPoolEvaluationRequest(w http.ResponseWriter, r *http.Request) (config.ProviderPool, ProviderPoolEvaluationRequest, bool) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return config.ProviderPool{}, ProviderPoolEvaluationRequest{}, false
	}
	var value api.ProviderPoolRouterEvaluationRequest
	if err := decodeProviderPoolRouterJSON(w, r, &value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return config.ProviderPool{}, ProviderPoolEvaluationRequest{}, false
	}
	return pool, ProviderPoolEvaluationRequest{
		PoolID: pool.ID, EvaluationMode: value.EvaluationMode,
		BaseProfileID: value.BaseProfileID, JudgeModel: value.JudgeModel,
		MaxQueries: value.MaxQueries, Repeats: value.Repeats, MaxOutputTokens: value.MaxOutputTokens,
		RequestTimeoutSeconds: value.RequestTimeoutSeconds, BudgetCurrency: value.BudgetCurrency,
		BudgetAmount: value.BudgetAmount, AllowUnknownPricing: value.AllowUnknownPricing,
	}, true
}

func decodeProviderPoolRouterAudit(w http.ResponseWriter, r *http.Request) (config.ProviderPool, api.ProviderPoolRouterActivationRequest, bool) {
	pool, ok := providerPoolForRouterRequest(w, r)
	if !ok {
		return config.ProviderPool{}, api.ProviderPoolRouterActivationRequest{}, false
	}
	var value api.ProviderPoolRouterActivationRequest
	if err := decodeProviderPoolRouterJSON(w, r, &value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return config.ProviderPool{}, api.ProviderPoolRouterActivationRequest{}, false
	}
	value.Actor, value.Reason = strings.TrimSpace(value.Actor), strings.TrimSpace(value.Reason)
	if value.Actor == "" || value.Reason == "" {
		writeError(w, http.StatusBadRequest, "actor and reason are required")
		return config.ProviderPool{}, api.ProviderPoolRouterActivationRequest{}, false
	}
	return pool, value, true
}

func decodeProviderPoolRouterJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, providerPoolRouterJSONLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return errors.New("invalid request body")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func providerPoolRouterListOptions(w http.ResponseWriter, r *http.Request) (routerprofile.ListOptions, bool) {
	parse := func(name string, fallback int) (int, error) {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			return fallback, nil
		}
		return strconv.Atoi(raw)
	}
	limit, err := parse("limit", 50)
	if err != nil || limit < 1 || limit > 100 {
		writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
		return routerprofile.ListOptions{}, false
	}
	offset, err := parse("offset", 0)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "offset must be non-negative")
		return routerprofile.ListOptions{}, false
	}
	return routerprofile.ListOptions{Limit: limit, Offset: offset}, true
}

func providerPoolRouterStatus(r *http.Request, store *routerprofile.Store, pool config.ProviderPool) (api.ProviderPoolRouterStatus, error) {
	var out api.ProviderPoolRouterStatus
	counts, err := store.EvaluationOpportunityCounts(r.Context(), pool.ID)
	if err != nil {
		return out, err
	}
	out.QualifiedQueryCount = counts.QualifiedQueryCount
	out.NewQueryCount = counts.NewQueryCount
	suggestions, err := store.ListSuggestions(r.Context(), pool.ID, routerprofile.SuggestionPending, routerprofile.ListOptions{Limit: 1})
	if err != nil {
		return out, err
	}
	if len(suggestions) > 0 {
		value := providerPoolRouterSuggestionAPI(suggestions[0], pool)
		out.PendingSuggestion = &value
	}
	jobs, err := store.ListEvaluationJobs(r.Context(), pool.ID, "", routerprofile.ListOptions{Limit: 20})
	if err != nil {
		return out, err
	}
	if len(jobs) > 0 {
		value := providerPoolRouterJobAPI(jobs[0], pool)
		out.LatestJob = &value
	}
	for _, job := range jobs {
		if job.Status == routerprofile.JobQueued || job.Status == routerprofile.JobRunning {
			value := providerPoolRouterJobAPI(job, pool)
			out.RunningJob = &value
			break
		}
	}
	active, err := store.ActiveProfile(r.Context(), pool.ID)
	activeID := ""
	if err == nil {
		activeID = active.ID
		out.CurrentProfileID = active.ID
		value := providerPoolRouterProfileAPI(active, pool, active.ID, false)
		out.ActiveProfile = &value
		history, historyErr := store.ListActivationHistory(r.Context(), pool.ID, routerprofile.ListOptions{Limit: 100})
		if historyErr != nil {
			return out, historyErr
		}
		for _, item := range history {
			if item.ToProfileID == active.ID && item.FromProfileID != "" {
				target, targetErr := store.GetProfile(r.Context(), pool.ID, item.FromProfileID)
				if targetErr != nil {
					return out, targetErr
				}
				targetAPI := providerPoolRouterProfileAPI(target, pool, active.ID, false)
				out.RollbackTargetProfile = &targetAPI
				break
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	profiles, err := store.ListProfiles(r.Context(), pool.ID, routerprofile.ListOptions{Limit: 1})
	if err != nil {
		return out, err
	}
	if len(profiles) > 0 {
		value := providerPoolRouterProfileAPI(profiles[0], pool, activeID, false)
		out.LatestCandidateProfile = &value
		out.SemanticDifferentiation = value.Metrics.SemanticDifferentiation && !value.Metrics.AllClustersOneMember
	}
	return out, nil
}

func providerPoolRouterSuggestionAPI(value routerprofile.EvaluationSuggestion, pool config.ProviderPool) api.ProviderPoolRouterSuggestion {
	return api.ProviderPoolRouterSuggestion{
		ID: value.ID, Reason: value.Reason, QualifiedQueryCount: value.QualifiedQueryCount,
		NewQueryCount: value.NewQueryCount, MemberCompatible: value.MemberFingerprint == providerPoolMemberFingerprint(pool),
		Status: string(value.Status), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func providerPoolRouterEvaluationPreviewAPI(value ProviderPoolEvaluationPreview) api.ProviderPoolRouterEvaluationPreview {
	targets := make([]api.ProviderPoolRouterTarget, 0, len(value.UnknownPriceMembers))
	for _, target := range value.UnknownPriceMembers {
		targets = append(targets, api.ProviderPoolRouterTarget{Source: target.Source, Model: target.Model})
	}
	return api.ProviderPoolRouterEvaluationPreview{
		EvaluationMode:        value.EvaluationMode,
		EligibleSnapshotCount: value.EligibleSnapshotCount, SelectedSnapshotCount: value.SelectedSnapshotCount,
		DirectCandidateCalls: value.DirectCandidateCalls, JudgeCalls: value.JudgeCalls,
		MaxJudgeCalls: value.MaxJudgeCalls, MaxTotalCalls: value.MaxTotalCalls,
		JudgePromptTokens: value.JudgePromptTokens, MaxJudgeTokenExposure: value.MaxJudgeTokenExposure,
		MaxTokenExposure: value.MaxTokenExposure, KnownEstimatedCost: value.KnownEstimatedCost,
		KnownJudgeEstimatedCost: value.KnownJudgeEstimatedCost,
		Currency:                value.Currency, UnknownPriceMembers: targets, JudgePriceKnown: value.JudgePriceKnown,
		RequiresUnknownPricingConsent: value.RequiresUnknownPricingConsent,
		Limits: api.ProviderPoolRouterEvaluationLimits{
			MaxQueries: value.Limits.MaxQueries, MaxRepeats: value.Limits.MaxRepeats,
			MaxOutputTokens: value.Limits.MaxOutputTokens, MaxRequestTimeoutSeconds: value.Limits.MaxRequestTimeoutSecs,
		},
	}
}

func providerPoolRouterJobAPI(value routerprofile.EvaluationJob, pool config.ProviderPool) api.ProviderPoolRouterEvaluationJob {
	out := api.ProviderPoolRouterEvaluationJob{
		ID: value.ID, BaseProfileID: value.BaseProfileID,
		EvaluationMode:   value.EvaluationMode,
		MemberCompatible: value.MemberFingerprint == providerPoolMemberFingerprint(pool),
		JudgeModel:       value.JudgeModel, MaxQueries: value.MaxQueries, Repeats: value.Repeats,
		MaxOutputTokens: value.MaxOutputTokens, RequestTimeoutSeconds: value.RequestTimeoutSeconds,
		BudgetCurrency: value.BudgetCurrency, BudgetAmount: value.BudgetAmount,
		AllowUnknownPricing:  value.AllowUnknownPricing,
		DirectCandidateCalls: value.DirectCandidateCalls, JudgeCalls: value.JudgeCalls,
		MaxJudgeCalls: value.MaxJudgeCalls, JudgePromptTokens: value.JudgePromptTokens,
		MaxJudgeTokenExposure: value.MaxJudgeTokenExposure, MaxTokenExposure: value.MaxTokenExposure,
		KnownJudgeEstimatedCost: value.KnownJudgeEstimatedCost,
		KnownEstimatedCost:      value.KnownEstimatedCost, EstimateCurrency: value.EstimateCurrency,
		UnknownPricing: value.UnknownPricing,
		Current:        value.Current, Total: value.Total, Phase: value.Phase,
		CancellationRequested: value.CancellationRequested, Status: string(value.Status), Error: value.Error,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
	if !value.StartedAt.IsZero() {
		started := value.StartedAt
		out.StartedAt = &started
	}
	if !value.CompletedAt.IsZero() {
		completed := value.CompletedAt
		out.CompletedAt = &completed
	}
	return out
}

func providerPoolRouterProfileAPI(value routerprofile.Profile, pool config.ProviderPool, activeID string, detail bool) api.ProviderPoolRouterProfile {
	if value.ArtifactSchemaVersion() == routerprofile.SchemaVersionV2 {
		artifact := value.ProfileV2
		compatible := true
		blocked := ""
		members := make(map[string]config.ProviderPoolMember, len(pool.Members))
		for _, member := range pool.Members {
			members[member.ID] = member
		}
		if _, ok := members[artifact.Calibration.Policy.SafeFallbackMemberID]; !ok {
			compatible = false
			blocked = "missing_safe_fallback"
		} else {
			for _, candidate := range artifact.Candidates {
				member, ok := members[candidate.MemberID]
				if !ok || !strings.EqualFold(strings.TrimSpace(member.Source), strings.TrimSpace(candidate.Source)) ||
					strings.TrimSpace(member.Model) != strings.TrimSpace(candidate.Model) {
					compatible = false
					break
				}
			}
		}
		if !compatible {
			if blocked == "" {
				blocked = "candidate_members_incompatible"
			}
		} else if !artifact.Validation.ActivationAllowed {
			if artifact.Validation.CollapsedSingleMember && !artifact.Validation.CollapsedQualityPassed {
				blocked = "collapsed_quality_not_passed"
			} else {
				blocked = "infeasible"
			}
		}
		out := api.ProviderPoolRouterProfile{
			ID: value.ID, Version: value.Version, SchemaVersion: routerprofile.SchemaVersionV2,
			RouterAlgorithm: artifact.RouterAlgorithm, MemberCompatible: compatible,
			MemberFingerprintDrift: value.MemberFingerprint != providerPoolMemberFingerprint(pool),
			Active:                 value.ID == activeID, CreatedAt: value.CreatedAt, CreatedBy: value.CreatedBy,
			SourceJobID: value.SourceJobID, Description: value.Description,
			GeneratedAt: artifact.GeneratedAt, Distance: artifact.Distance,
			CostUnit:          artifact.Calibration.CostBasis,
			FallbackMemberID:  artifact.Calibration.Policy.SafeFallbackMemberID,
			ActivationAllowed: blocked == "", ActivationBlockedReason: blocked,
			ValidationState: artifact.Validation.State, Feasible: artifact.Validation.Feasible,
			CollapsedSingleMember:  artifact.Validation.CollapsedSingleMember,
			CollapsedQualityPassed: artifact.Validation.CollapsedQualityPassed,
		}
		out.CandidateDistribution = make([]api.ProviderPoolRouterCandidateDistribution, 0, len(artifact.Candidates))
		for _, candidate := range artifact.Candidates {
			item := api.ProviderPoolRouterCandidateDistribution{
				MemberID: candidate.MemberID,
				Target:   api.ProviderPoolRouterTarget{Source: candidate.Source, Model: candidate.Model},
			}
			for _, distribution := range artifact.Calibration.MemberDistribution {
				if distribution.MemberID == candidate.MemberID {
					item.SampleCount = distribution.Count
					item.Fraction = distribution.Fraction
					break
				}
			}
			out.CandidateDistribution = append(out.CandidateDistribution, item)
		}
		if detail {
			calibration := artifact.Calibration
			diagnostics := artifact.Learner.Diagnostics
			out.V2 = &api.ProviderPoolRouterV2Summary{
				ProfileAlgorithm: artifact.ProfileAlgorithm, ModelType: artifact.Learner.ModelType,
				ModelFallbackReason: diagnostics.FallbackReason, SampleCount: diagnostics.ObservationCount,
				QueryGroupCount: calibration.QueryGroupCount, RoundCount: calibration.RoundCount,
				CVFoldCount: diagnostics.CVFoldCount, TargetQualityRetention: calibration.TargetQualityRetention,
				ConfidenceLevel: calibration.ConfidenceLevel,
				Baseline:        providerPoolRouterQualityCostAPI(calibration.Baseline),
				Routed:          providerPoolRouterQualityCostAPI(calibration.Routed),
				PointRetention:  calibration.PointRetention, ConservativeRetention: calibration.ConservativeRetention,
				RetentionLowerBound: calibration.RetentionLowerBound, Savings: calibration.Savings,
				SavingsFraction: calibration.SavingsFraction, SavingsKnown: calibration.SavingsKnown,
				Coverage: calibration.Coverage, FallbackRate: calibration.FallbackRate,
				LowConfidenceRate: calibration.LowConfidenceRate, OODRate: calibration.OODRate,
				PairwiseMetrics: providerPoolRouterPairwiseMetricsAPI(calibration.PairwiseMetrics),
				Thresholds: api.ProviderPoolRouterThresholds{
					MinimumConfidence: calibration.Policy.Thresholds.MinimumConfidence,
					MinimumMargin:     calibration.Policy.Thresholds.MinimumMargin,
					MinimumSimilarity: calibration.Policy.Thresholds.MinimumSimilarity,
					QualitySlack:      calibration.Policy.Thresholds.QualitySlack,
				},
				OptimizeKnownCost: calibration.Policy.OptimizeKnownCost,
				QualityFeasible:   calibration.QualityFeasible, KnownCostFeasible: calibration.KnownCostFeasible,
				InsufficientEvidence: calibration.InsufficientEvidence,
				CollapsedMemberID:    calibration.CollapsedMemberID,
				Warnings:             providerPoolRouterWarnings(artifact.Warnings, artifact.Validation.Warnings),
			}
		}
		return out
	}
	evaluation := value.Profile.Evaluation
	compatible := validateRouterProfileForPool(pool, value, false) == nil
	blocked := ""
	switch {
	case !compatible:
		blocked = "candidate_members_incompatible"
	case evaluation.HeldOutQueryCount == 0:
		blocked = "no_held_out_queries"
	case evaluation.AllClustersOneMember:
		blocked = "collapsed"
	case !evaluation.SemanticDifferentiation:
		blocked = "no_semantic_differentiation"
	}
	out := api.ProviderPoolRouterProfile{
		ID: value.ID, Version: value.Version, SchemaVersion: routerprofile.SchemaVersionV1,
		RouterAlgorithm: "semantic_cluster_v1", MemberCompatible: compatible,
		MemberFingerprintDrift: value.MemberFingerprint != providerPoolMemberFingerprint(pool),
		Active:                 value.ID == activeID,
		CreatedAt:              value.CreatedAt, CreatedBy: value.CreatedBy, SourceJobID: value.SourceJobID,
		Description: value.Description, GeneratedAt: value.Profile.GeneratedAt, Distance: value.Profile.Distance,
		CostUnit: value.Profile.CostUnit, FallbackMemberID: value.Profile.FallbackMemberID,
		Metrics: api.ProviderPoolRouterMetrics{
			QueryCount: evaluation.QueryCount, CellCount: evaluation.CellCount, TrialCount: evaluation.TrialCount,
			Repeats: evaluation.Repeats, ResponseOutcomes: evaluation.ResponseOutcomes, WinRate: evaluation.WinRate,
			Spend: evaluation.Spend, TotalCost: evaluation.TotalCost, Currency: evaluation.Currency,
			CostUnit: evaluation.CostUnit, MonetarySpendKnown: evaluation.MonetarySpendKnown,
			UnknownMonetarySpend: evaluation.UnknownMonetarySpend, TrainQueryCount: evaluation.TrainQueryCount,
			HeldOutQueryCount: evaluation.HeldOutQueryCount, CVFoldCount: evaluation.CVFoldCount,
			TrainUtility: evaluation.TrainUtility, TrainQuality: evaluation.TrainQuality, TrainCost: evaluation.TrainCost,
			HeldOutUtility: evaluation.HeldOutUtility, HeldOutQuality: evaluation.HeldOutQuality,
			HeldOutCost: evaluation.HeldOutCost, AllClustersOneMember: evaluation.AllClustersOneMember,
			SemanticDifferentiation: evaluation.SemanticDifferentiation,
		},
		ActivationAllowed: blocked == "", ActivationBlockedReason: blocked,
		ValidationState: "valid", Feasible: blocked == "",
	}
	if !detail {
		return out
	}
	distribution := make(map[string]*api.ProviderPoolRouterCandidateDistribution)
	for _, candidate := range value.Profile.Candidates {
		distribution[candidate.MemberID] = &api.ProviderPoolRouterCandidateDistribution{
			MemberID: candidate.MemberID,
			Target:   api.ProviderPoolRouterTarget{Source: candidate.Source, Model: candidate.Model},
		}
	}
	out.Clusters = make([]api.ProviderPoolRouterCluster, 0, len(value.Profile.Clusters))
	for _, cluster := range value.Profile.Clusters {
		out.Clusters = append(out.Clusters, api.ProviderPoolRouterCluster{
			ID: cluster.ID, TargetMemberID: cluster.TargetMemberID,
			Target:      api.ProviderPoolRouterTarget{Source: cluster.Target.Source, Model: cluster.Target.Model},
			SampleCount: cluster.SampleCount,
			DistanceQuantile: api.ProviderPoolRouterDistanceQuantiles{
				P50: cluster.DistanceQuantile.P50, P90: cluster.DistanceQuantile.P90,
				P95: cluster.DistanceQuantile.P95, P99: cluster.DistanceQuantile.P99,
			},
			OODThreshold: cluster.OODThreshold,
		})
		if item := distribution[cluster.TargetMemberID]; item != nil {
			item.ClusterCount++
			item.SampleCount += cluster.SampleCount
		}
	}
	out.CandidateDistribution = make([]api.ProviderPoolRouterCandidateDistribution, 0, len(value.Profile.Candidates))
	for _, candidate := range value.Profile.Candidates {
		out.CandidateDistribution = append(out.CandidateDistribution, *distribution[candidate.MemberID])
	}
	return out
}

func providerPoolRouterQualityCostAPI(value routerprofile.CalibrationQualityCost) api.ProviderPoolRouterQualityCost {
	return api.ProviderPoolRouterQualityCost{
		Quality: value.Quality, Cost: value.Cost, CostKnown: value.CostKnown, Currency: value.Currency,
	}
}

func providerPoolRouterPairwiseMetricsAPI(value routerprofile.PairwiseMetrics) api.ProviderPoolRouterPairwiseMetrics {
	return api.ProviderPoolRouterPairwiseMetrics{
		Count: value.Count, LogLoss: value.LogLoss, Brier: value.Brier,
		TopClassAccuracy: value.TopClassAccuracy, ECE: value.ECE,
	}
}

func providerPoolRouterWarnings(groups ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, group := range groups {
		for _, warning := range group {
			warning = strings.TrimSpace(warning)
			if warning == "" {
				continue
			}
			if _, ok := seen[warning]; ok {
				continue
			}
			seen[warning] = struct{}{}
			out = append(out, warning)
		}
	}
	return out
}

func activeProviderPoolRouterProfileID(r *http.Request, store *routerprofile.Store, poolID string) string {
	value, err := store.ActiveProfile(r.Context(), poolID)
	if err != nil {
		return ""
	}
	return value.ID
}

func providerPoolRouterActivationAPI(value routerprofile.Activation) api.ProviderPoolRouterActivation {
	return api.ProviderPoolRouterActivation{
		ID: value.ID, FromProfileID: value.FromProfileID, ToProfileID: value.ToProfileID,
		Action: value.Action, Reason: value.Reason, Actor: value.Actor, CreatedAt: value.CreatedAt,
	}
}

func writeProviderPoolRouterError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status = http.StatusNotFound
	case errors.Is(err, routerprofile.ErrConflict):
		status = http.StatusConflict
	case strings.Contains(err.Error(), "unavailable"):
		status = http.StatusServiceUnavailable
	case strings.Contains(err.Error(), "not found"):
		status = http.StatusNotFound
	case strings.Contains(err.Error(), "did not pass"),
		strings.Contains(err.Error(), "fingerprint"),
		strings.Contains(err.Error(), "already"),
		strings.Contains(err.Error(), "cancellation"):
		status = http.StatusConflict
	}
	writeError(w, status, err.Error())
}
