package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	routerprofile "github.com/opencsgs/semantic-router"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	providerPoolJudgePromptVersion         = "gateway-judge-v1"
	providerPoolListwiseJudgePromptVersion = "gateway-listwise-v2"
	providerPoolJudgeMaxTokens             = 2048
	providerPoolJudgeMaxAttempts           = 3
	providerPoolJudgeContextBytes          = 32 << 10
)

type ProviderPoolEvaluationRequest struct {
	PoolID                string
	EvaluationMode        string
	BaseProfileID         string
	JudgeModel            string
	MaxQueries            int
	Repeats               int
	MaxOutputTokens       int
	RequestTimeoutSeconds int
	BudgetCurrency        string
	BudgetAmount          float64
	AllowUnknownPricing   bool
}

type ProviderPoolEvaluationLimits struct {
	MaxQueries            int
	MaxRepeats            int
	MaxOutputTokens       int
	MaxRequestTimeoutSecs int
}

type ProviderPoolEvaluationPreview struct {
	EvaluationMode                string
	EligibleSnapshotCount         int
	SelectedSnapshotCount         int
	DirectCandidateCalls          int
	JudgeCalls                    int
	MaxJudgeCalls                 int
	MaxTotalCalls                 int
	JudgePromptTokens             int64
	MaxJudgeTokenExposure         int64
	MaxTokenExposure              int64
	KnownJudgeEstimatedCost       float64
	KnownEstimatedCost            float64
	Currency                      string
	UnknownPriceMembers           []routerprofile.Target
	JudgePriceKnown               bool
	RequiresUnknownPricingConsent bool
	Limits                        ProviderPoolEvaluationLimits
}

type evaluationPrice struct {
	input, output float64
	currency      string
	known         bool
}

type evaluationCallResult struct {
	content      string
	promptTokens int
	outputTokens int
	totalTokens  int
	finishReason string
	latency      time.Duration
}

// PreviewProviderPoolEvaluation resolves pricing from the model catalog at
// request time. It performs no inference and does not create a job.
func (s *Server) PreviewProviderPoolEvaluation(ctx context.Context, request ProviderPoolEvaluationRequest) (ProviderPoolEvaluationPreview, error) {
	request = normalizeProviderPoolEvaluationRequest(request)
	if err := validateProviderPoolEvaluationRequest(request); err != nil {
		return ProviderPoolEvaluationPreview{}, err
	}
	if s.routerProfiles == nil {
		return ProviderPoolEvaluationPreview{}, errors.New("semantic router profile store is unavailable")
	}
	pool, ok := providerPoolByID(request.PoolID)
	if !ok || !pool.Enabled {
		return ProviderPoolEvaluationPreview{}, errors.New("provider pool not found or disabled")
	}
	if providerPoolMemberFingerprint(pool) == "" {
		return ProviderPoolEvaluationPreview{}, errors.New("provider pool members are unavailable")
	}
	if request.BaseProfileID != "" {
		base, err := s.routerProfiles.GetProfile(ctx, pool.ID, request.BaseProfileID)
		if err != nil {
			return ProviderPoolEvaluationPreview{}, fmt.Errorf("loading base router profile: %w", err)
		}
		if err := validateRouterProfileForPool(pool, base, false); err != nil {
			return ProviderPoolEvaluationPreview{}, fmt.Errorf("base router profile is incompatible: %w", err)
		}
	}
	if len(pool.Members) > routerprofile.MaxOptimizerCandidates {
		return ProviderPoolEvaluationPreview{}, fmt.Errorf(
			"provider pool has %d members; evaluation optimizer supports at most %d",
			len(pool.Members), routerprofile.MaxOptimizerCandidates,
		)
	}
	count, err := s.routerProfiles.CountQuerySnapshots(ctx, pool.ID)
	if err != nil {
		return ProviderPoolEvaluationPreview{}, err
	}
	models, err := s.evaluationCatalog(ctx)
	if err != nil {
		return ProviderPoolEvaluationPreview{}, fmt.Errorf("loading evaluation model catalog: %w", err)
	}
	judgeInfo, ok := evaluationCatalogModel(models, "cloud", request.JudgeModel)
	if !ok || judgeInfo.PipelineTag != "text-generation" {
		return ProviderPoolEvaluationPreview{}, errors.New("judge model must be an available cloud text-generation model")
	}
	if !s.hasCloudCredential() {
		return ProviderPoolEvaluationPreview{}, errors.New("cloud credential is required for the evaluation judge")
	}
	selected := min(count, request.MaxQueries)
	candidateCalls := selected * len(pool.Members) * request.Repeats
	judgeCalls := candidateCalls
	if request.EvaluationMode == routerprofile.EvaluationModeListwiseV2 {
		judgeCalls = selected * request.Repeats
	}
	preview := ProviderPoolEvaluationPreview{
		EvaluationMode:        request.EvaluationMode,
		EligibleSnapshotCount: count,
		SelectedSnapshotCount: selected,
		DirectCandidateCalls:  candidateCalls,
		JudgeCalls:            judgeCalls,
		MaxJudgeCalls:         judgeCalls * providerPoolJudgeMaxAttempts,
		MaxTotalCalls:         candidateCalls + judgeCalls*providerPoolJudgeMaxAttempts,
		Currency:              request.BudgetCurrency,
		Limits: ProviderPoolEvaluationLimits{
			MaxQueries: routerprofile.MaxEvaluationQueries, MaxRepeats: routerprofile.MaxEvaluationRepeats,
			MaxOutputTokens: routerprofile.MaxEvaluationOutputTokens, MaxRequestTimeoutSecs: 600,
		},
	}
	judgePrice := evaluationModelPrice(judgeInfo, request.BudgetCurrency)
	preview.JudgePriceKnown = judgePrice.known
	snapshots, err := s.routerProfiles.ListQuerySnapshots(ctx, pool.ID, routerprofile.ListOptions{Limit: selected})
	if err != nil {
		return ProviderPoolEvaluationPreview{}, err
	}
	for _, member := range pool.Members {
		info, exists := evaluationCatalogModel(models, member.Source, member.Model)
		price := evaluationPrice{}
		if exists {
			price = evaluationModelPrice(info, request.BudgetCurrency)
		}
		if !price.known {
			preview.UnknownPriceMembers = append(preview.UnknownPriceMembers,
				routerprofile.Target{Source: member.Source, Model: member.Model})
		}
		for _, snapshot := range snapshots {
			replayMessages := providerPoolEvaluationMessages(snapshot)
			inputText := renderProviderPoolEvaluationMessages(replayMessages, 0)
			inputTokens := estimateProviderPoolTextTokens(inputText)
			repeats := float64(request.Repeats)
			preview.MaxTokenExposure += int64(request.Repeats * (inputTokens + request.MaxOutputTokens))
			if price.known {
				preview.KnownEstimatedCost += repeats * evaluationCost(price, inputTokens, request.MaxOutputTokens)
			}
		}
	}
	for _, snapshot := range snapshots {
		replayMessages := providerPoolEvaluationMessages(snapshot)
		judgeContext := renderProviderPoolEvaluationMessages(replayMessages, providerPoolJudgeContextBytes)
		var judgePrompt string
		if request.EvaluationMode == routerprofile.EvaluationModeListwiseV2 {
			candidates := make([]listwisePromptCandidate, len(pool.Members))
			for index := range candidates {
				candidates[index] = listwisePromptCandidate{
					Alias: string(rune('A' + index)), Answer: strings.Repeat("x", request.MaxOutputTokens*4),
					Available: true,
				}
			}
			judgePrompt = providerPoolListwiseJudgePrompt(judgeContext, candidates)
		} else {
			judgePrompt = providerPoolJudgePrompt(judgeContext, strings.Repeat("x", request.MaxOutputTokens*4))
		}
		judgeInput := estimateProviderPoolTextTokens(judgePrompt)
		attempts := request.Repeats * providerPoolJudgeMaxAttempts
		if request.EvaluationMode == routerprofile.EvaluationModeAbsoluteV1 {
			attempts *= len(pool.Members)
		}
		preview.JudgePromptTokens += int64(request.Repeats * judgeInput)
		judgeExposure := int64(attempts * (judgeInput + providerPoolJudgeMaxTokens))
		preview.MaxJudgeTokenExposure += judgeExposure
		preview.MaxTokenExposure += judgeExposure
		if judgePrice.known {
			judgeCost := float64(attempts) * evaluationCost(judgePrice, judgeInput, providerPoolJudgeMaxTokens)
			preview.KnownJudgeEstimatedCost += judgeCost
			preview.KnownEstimatedCost += judgeCost
		}
	}
	preview.RequiresUnknownPricingConsent = !preview.JudgePriceKnown || len(preview.UnknownPriceMembers) > 0
	return preview, nil
}

func (s *Server) CreateProviderPoolEvaluationJob(ctx context.Context, request ProviderPoolEvaluationRequest) (routerprofile.EvaluationJob, error) {
	preview, err := s.PreviewProviderPoolEvaluation(ctx, request)
	if err != nil {
		return routerprofile.EvaluationJob{}, err
	}
	request = normalizeProviderPoolEvaluationRequest(request)
	if preview.SelectedSnapshotCount == 0 {
		return routerprofile.EvaluationJob{}, errors.New("provider pool has no eligible query snapshots")
	}
	if preview.RequiresUnknownPricingConsent && !request.AllowUnknownPricing {
		return routerprofile.EvaluationJob{}, errors.New("unknown candidate or judge pricing requires explicit consent")
	}
	if request.BudgetAmount+1e-12 < preview.KnownEstimatedCost {
		return routerprofile.EvaluationJob{}, fmt.Errorf("known estimated cost %.6f %s exceeds budget %.6f",
			preview.KnownEstimatedCost, request.BudgetCurrency, request.BudgetAmount)
	}
	pool, _ := providerPoolByID(request.PoolID)
	models, err := s.evaluationCatalog(ctx)
	if err != nil {
		return routerprofile.EvaluationJob{}, err
	}
	judgeInfo, exists := evaluationCatalogModel(models, "cloud", request.JudgeModel)
	if !exists {
		return routerprofile.EvaluationJob{}, errors.New("judge model disappeared from the request-time catalog")
	}
	judgePrice := evaluationModelPrice(judgeInfo, request.BudgetCurrency)
	snapshots, err := s.routerProfiles.ListQuerySnapshots(ctx, pool.ID,
		routerprofile.ListOptions{Limit: preview.SelectedSnapshotCount})
	if err != nil {
		return routerprofile.EvaluationJob{}, err
	}
	cells := make([]routerprofile.EvaluationCell, 0, preview.DirectCandidateCalls)
	for _, snapshot := range snapshots {
		for _, member := range pool.Members {
			info, exists := evaluationCatalogModel(models, member.Source, member.Model)
			price := evaluationPrice{}
			if exists {
				price = evaluationModelPrice(info, request.BudgetCurrency)
			}
			for repeat := 0; repeat < request.Repeats; repeat++ {
				cells = append(cells, routerprofile.EvaluationCell{
					QuerySnapshotID: snapshot.ID, CandidateMemberID: member.ID,
					Candidate: routerprofile.Target{Source: member.Source, Model: member.Model},
					Repeat:    repeat, PriceInput: price.input, PriceOutput: price.output,
					PriceCurrency: price.currency, PriceKnown: price.known,
					JudgePriceInput: judgePrice.input, JudgePriceOutput: judgePrice.output,
					JudgePriceCurrency: judgePrice.currency, JudgePriceKnown: judgePrice.known,
				})
			}
		}
	}
	promptVersion := providerPoolJudgePromptVersion
	if request.EvaluationMode == routerprofile.EvaluationModeListwiseV2 {
		promptVersion = providerPoolListwiseJudgePromptVersion
	}
	jobValue := routerprofile.EvaluationJob{
		PoolID: pool.ID, BaseProfileID: request.BaseProfileID,
		EvaluationMode:    request.EvaluationMode,
		MemberFingerprint: providerPoolMemberFingerprint(pool), JudgeModel: request.JudgeModel,
		JudgePromptVersion: promptVersion, MaxQueries: request.MaxQueries,
		Repeats: request.Repeats, MaxOutputTokens: request.MaxOutputTokens,
		RequestTimeoutSeconds: request.RequestTimeoutSeconds,
		BudgetCurrency:        request.BudgetCurrency, BudgetAmount: request.BudgetAmount,
		AllowUnknownPricing:  request.AllowUnknownPricing,
		DirectCandidateCalls: preview.DirectCandidateCalls, JudgeCalls: preview.JudgeCalls,
		MaxJudgeCalls: preview.MaxJudgeCalls, JudgePromptTokens: preview.JudgePromptTokens,
		MaxJudgeTokenExposure: preview.MaxJudgeTokenExposure, MaxTokenExposure: preview.MaxTokenExposure,
		KnownJudgeEstimatedCost: preview.KnownJudgeEstimatedCost,
		KnownEstimatedCost:      preview.KnownEstimatedCost, EstimateCurrency: preview.Currency,
		UnknownPricing: preview.RequiresUnknownPricingConsent,
		Total:          len(cells),
	}
	var job routerprofile.EvaluationJob
	if request.EvaluationMode == routerprofile.EvaluationModeListwiseV2 {
		jobValue.Total += preview.JudgeCalls
		job, err = s.routerProfiles.CreateListwiseEvaluationJob(ctx, jobValue, cells)
	} else {
		job, err = s.routerProfiles.CreateEvaluationJob(ctx, jobValue, cells)
	}
	if err == nil && s.routerEvaluationWake != nil {
		select {
		case s.routerEvaluationWake <- struct{}{}:
		default:
		}
	}
	return job, err
}

func (s *Server) CancelProviderPoolEvaluationJob(ctx context.Context, poolID, jobID string) error {
	if s.routerProfiles == nil {
		return errors.New("semantic router profile store is unavailable")
	}
	err := s.routerProfiles.RequestEvaluationJobCancellation(ctx, poolID, jobID)
	s.cancelRouterEvaluationCurrent(poolID, jobID)
	return err
}

func (s *Server) GetProviderPoolEvaluationJob(ctx context.Context, poolID, jobID string) (routerprofile.EvaluationJob, []routerprofile.EvaluationCell, error) {
	if s.routerProfiles == nil {
		return routerprofile.EvaluationJob{}, nil, errors.New("semantic router profile store is unavailable")
	}
	return s.routerProfiles.GetEvaluationJob(ctx, poolID, jobID)
}

func (s *Server) ListProviderPoolEvaluationJobs(ctx context.Context, poolID string, status routerprofile.JobStatus, options routerprofile.ListOptions) ([]routerprofile.EvaluationJob, error) {
	if s.routerProfiles == nil {
		return nil, errors.New("semantic router profile store is unavailable")
	}
	return s.routerProfiles.ListEvaluationJobs(ctx, poolID, status, options)
}

func (s *Server) startRouterEvaluationWorker(ctx context.Context) {
	for {
		s.routerStoreMu.RLock()
		job, err := s.routerProfiles.ClaimNextEvaluationJobGlobal(ctx)
		if err == nil {
			runCtx, cancel := context.WithCancel(ctx)
			s.setRouterEvaluationCurrent(job.PoolID, job.ID, cancel)
			s.runProviderPoolEvaluation(runCtx, job)
			cancel()
			s.clearRouterEvaluationCurrent(job.PoolID, job.ID)
			s.routerStoreMu.RUnlock()
			continue
		}
		s.routerStoreMu.RUnlock()
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			log.Printf("SEMANTIC ROUTER: claiming evaluation job failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.routerEvaluationWake:
		case <-time.After(time.Second):
		}
	}
}

func (s *Server) setRouterEvaluationCurrent(poolID, jobID string, cancel context.CancelFunc) {
	s.routerEvaluationMu.Lock()
	s.routerEvaluationPoolID, s.routerEvaluationJobID = poolID, jobID
	s.routerEvaluationRunCancel = cancel
	s.routerEvaluationMu.Unlock()
}

func (s *Server) clearRouterEvaluationCurrent(poolID, jobID string) {
	s.routerEvaluationMu.Lock()
	if s.routerEvaluationPoolID == poolID && s.routerEvaluationJobID == jobID {
		s.routerEvaluationPoolID, s.routerEvaluationJobID = "", ""
		s.routerEvaluationRunCancel = nil
	}
	s.routerEvaluationMu.Unlock()
}

func (s *Server) cancelRouterEvaluationCurrent(poolID, jobID string) {
	s.routerEvaluationMu.Lock()
	cancel := s.routerEvaluationRunCancel
	if s.routerEvaluationPoolID != poolID || s.routerEvaluationJobID != jobID {
		cancel = nil
	}
	s.routerEvaluationMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) runProviderPoolEvaluation(ctx context.Context, job routerprofile.EvaluationJob) {
	if job.EvaluationMode == routerprofile.EvaluationModeListwiseV2 {
		s.runProviderPoolListwiseEvaluation(ctx, job)
		return
	}
	fail := func(err error) {
		status := routerprofile.JobFailed
		if errors.Is(err, context.Canceled) {
			current, _, getErr := s.routerProfiles.GetEvaluationJob(context.Background(), job.PoolID, job.ID)
			if getErr == nil && current.CancellationRequested {
				status = routerprofile.JobCancelled
			}
		}
		_ = s.routerProfiles.UpdateEvaluationJobStatus(context.Background(), job.PoolID, job.ID, status, err.Error())
		s.finishProviderPoolEvaluationSuggestion(job.PoolID, routerprofile.SuggestionRejected)
	}
	if _, err := s.routerProfiles.GetProfileBySourceJob(ctx, job.PoolID, job.ID); err == nil {
		_ = s.routerProfiles.UpdateEvaluationJobStatus(context.Background(), job.PoolID, job.ID, routerprofile.JobSucceeded, "")
		s.finishProviderPoolEvaluationSuggestion(job.PoolID, routerprofile.SuggestionAccepted)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(fmt.Errorf("checking existing optimized profile: %w", err))
		return
	}
	pool, ok := providerPoolByID(job.PoolID)
	if !ok || !pool.Enabled {
		fail(errors.New("provider pool not found or disabled"))
		return
	}
	if providerPoolMemberFingerprint(pool) != job.MemberFingerprint {
		fail(errors.New("provider pool member fingerprint changed"))
		return
	}
	models, err := s.evaluationCatalog(ctx)
	if err != nil {
		fail(err)
		return
	}
	judgeInfo, ok := evaluationCatalogModel(models, "cloud", job.JudgeModel)
	if !ok || judgeInfo.PipelineTag != "text-generation" || !s.hasCloudCredential() {
		fail(errors.New("judge cloud model or credential is unavailable"))
		return
	}
	cells, err := s.routerProfiles.ListEvaluationCells(ctx, job.PoolID, job.ID)
	if err != nil {
		fail(err)
		return
	}
	completed := 0
	spend := 0.0
	for _, existing := range cells {
		spend += existing.EstimatedCost
		if existing.Status == routerprofile.CellSucceeded || existing.Status == routerprofile.CellFailed {
			completed++
		}
	}
	_ = s.routerProfiles.UpdateEvaluationJobProgress(ctx, job.PoolID, job.ID, "evaluating", completed, len(cells))
	for _, cell := range cells {
		if cell.Status == routerprofile.CellSucceeded || cell.Status == routerprofile.CellFailed {
			continue
		}
		if err := s.checkEvaluationCancellation(ctx, job); err != nil {
			fail(err)
			return
		}
		snapshot, err := s.routerProfiles.GetQuerySnapshot(ctx, job.PoolID, cell.QuerySnapshotID)
		if err != nil {
			fail(err)
			return
		}
		if cell.Status != routerprofile.CellCandidateSucceeded {
			replayMessages := providerPoolEvaluationMessages(snapshot)
			inputText := renderProviderPoolEvaluationMessages(replayMessages, 0)
			maxCandidateCost := 0.0
			if cell.PriceKnown {
				maxCandidateCost = evaluationCost(evaluationPrice{
					input: cell.PriceInput, output: cell.PriceOutput, currency: cell.PriceCurrency, known: true,
				}, estimateProviderPoolTextTokens(inputText), job.MaxOutputTokens)
			}
			projectedSpend := spend - cell.EstimatedCost + maxCandidateCost
			if projectedSpend > job.BudgetAmount+1e-12 {
				fail(errors.New("evaluation budget exhausted before candidate call"))
				return
			}
			cell.Status = routerprofile.CellRunning
			cell.EstimatedCost = maxCandidateCost
			spend = projectedSpend
			if err := s.routerProfiles.UpdateEvaluationCell(ctx, cell); err != nil {
				fail(err)
				return
			}
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(job.RequestTimeoutSeconds)*time.Second)
			result, callErr := s.evaluationChatCompletion(callCtx, cell.Candidate.Model, cell.Candidate.Source,
				replayMessages, job.MaxOutputTokens, false)
			cancel()
			cell.LatencyMillis = result.latency.Milliseconds()
			cell.CandidateResponse, cell.PromptTokens = result.content, result.promptTokens
			cell.OutputTokens, cell.TotalTokens, cell.FinishReason = result.outputTokens, result.totalTokens, result.finishReason
			if cell.TotalTokens == 0 && callErr == nil {
				cell.PromptTokens = estimateProviderPoolTextTokens(inputText)
				if strings.TrimSpace(result.content) != "" {
					cell.OutputTokens = estimateProviderPoolTextTokens(result.content)
				}
				cell.TotalTokens = cell.PromptTokens + cell.OutputTokens
			}
			reservedCandidateCost := cell.EstimatedCost
			if cell.PriceKnown && callErr == nil {
				cell.EstimatedCost = evaluationCost(evaluationPrice{
					input: cell.PriceInput, output: cell.PriceOutput, currency: cell.PriceCurrency, known: true,
				}, cell.PromptTokens, cell.OutputTokens)
			}
			spend += cell.EstimatedCost - reservedCandidateCost
			if callErr != nil || strings.TrimSpace(result.content) == "" {
				if (errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded)) && ctx.Err() != nil {
					fail(ctx.Err())
					return
				}
				if callErr == nil {
					callErr = errors.New("candidate returned an empty answer")
				}
				reason := "candidate availability failure: " + callErr.Error()
				cell.Status, cell.Outcome = routerprofile.CellSucceeded, "candidate_error"
				cell.QualitySource, cell.CandidateAvailable = routerprofile.QualitySourceCandidateFailure, false
				cell.JudgeScore, cell.JudgeReason = 0, reason
				cell.CandidateError, cell.Error = callErr.Error(), callErr.Error()
				cell.MonetaryCostKnown = cell.PriceKnown
				if cell.MonetaryCostKnown {
					cell.MonetaryCurrency = job.BudgetCurrency
				}
				_ = s.routerProfiles.UpdateEvaluationCell(context.Background(), cell)
				completed++
				_ = s.routerProfiles.UpdateEvaluationJobProgress(context.Background(), job.PoolID, job.ID, "evaluating", completed, len(cells))
				continue
			}
			cell.Status, cell.Outcome = routerprofile.CellCandidateSucceeded, "candidate_completed"
			cell.CandidateAvailable = true
			if err := s.routerProfiles.UpdateEvaluationCell(ctx, cell); err != nil {
				fail(err)
				return
			}
		}
		// v5 migration preserves pre-upgrade candidate responses. Treat a
		// durable non-empty candidate boundary as available without rerunning it.
		cell.CandidateAvailable = true
		conversation := renderProviderPoolEvaluationMessages(providerPoolEvaluationMessages(snapshot), providerPoolJudgeContextBytes)
		judgePrompt := providerPoolJudgePrompt(conversation, cell.CandidateResponse)
		judgePrice := evaluationPrice{
			input: cell.JudgePriceInput, output: cell.JudgePriceOutput,
			currency: cell.JudgePriceCurrency, known: cell.JudgePriceKnown,
		}
		cell.JudgeModel, cell.JudgePromptVersion = job.JudgeModel, job.JudgePromptVersion
		judgeSucceeded := false
		var lastJudgeErr error
		for cell.JudgeAttemptCount < providerPoolJudgeMaxAttempts {
			if err := s.checkEvaluationCancellation(ctx, job); err != nil {
				fail(err)
				return
			}
			maxJudgeCost := 0.0
			if judgePrice.known {
				maxJudgeCost = evaluationCost(judgePrice, estimateProviderPoolTextTokens(judgePrompt), providerPoolJudgeMaxTokens)
			}
			if spend+maxJudgeCost > job.BudgetAmount+1e-12 {
				fail(errors.New("evaluation budget exhausted before judge retry"))
				return
			}
			// Consume and reserve this attempt durably before the external call.
			// A crash can over-reserve, but can never make a retry exceed budget.
			cell.JudgeAttemptCount++
			cell.EstimatedCost += maxJudgeCost
			spend += maxJudgeCost
			cell.Status, cell.Outcome = routerprofile.CellCandidateSucceeded, "candidate_completed"
			if err := s.routerProfiles.UpdateEvaluationCell(context.Background(), cell); err != nil {
				fail(err)
				return
			}
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(job.RequestTimeoutSeconds)*time.Second)
			judged, judgeErr := s.evaluationChatCompletion(callCtx, job.JudgeModel, "cloud",
				[]routerprofile.Message{{Role: "user", Content: judgePrompt}}, providerPoolJudgeMaxTokens, true)
			cancel()
			if (errors.Is(judgeErr, context.Canceled) || errors.Is(judgeErr, context.DeadlineExceeded)) && ctx.Err() != nil {
				fail(ctx.Err())
				return
			}
			attemptPromptTokens, attemptOutputTokens, attemptTotalTokens :=
				judged.promptTokens, judged.outputTokens, judged.totalTokens
			if attemptTotalTokens == 0 && judgeErr == nil {
				attemptPromptTokens = estimateProviderPoolTextTokens(judgePrompt)
				if strings.TrimSpace(judged.content) != "" {
					attemptOutputTokens = estimateProviderPoolTextTokens(judged.content)
				}
				attemptTotalTokens = attemptPromptTokens + attemptOutputTokens
			}
			cell.JudgePromptTokens += attemptPromptTokens
			cell.JudgeOutputTokens += attemptOutputTokens
			cell.JudgeTotalTokens += attemptTotalTokens
			actualJudgeCost := 0.0
			if judgePrice.known && attemptTotalTokens > 0 {
				actualJudgeCost = evaluationCost(judgePrice, attemptPromptTokens, attemptOutputTokens)
				cell.EstimatedCost += actualJudgeCost - maxJudgeCost
				spend += actualJudgeCost - maxJudgeCost
			}
			if judgeErr == nil {
				cell.JudgeScore, cell.JudgeReason, judgeErr = parseProviderPoolJudge(judged.content)
			}
			if judgeErr == nil {
				cell.Status, cell.Outcome = routerprofile.CellSucceeded, "judged"
				cell.QualitySource, cell.CandidateAvailable = routerprofile.QualitySourceJudge, true
				cell.JudgeError, cell.Error = "", ""
				cell.MonetaryCostKnown = cell.PriceKnown && cell.JudgePriceKnown &&
					strings.EqualFold(cell.PriceCurrency, cell.JudgePriceCurrency) &&
					strings.EqualFold(cell.PriceCurrency, job.BudgetCurrency)
				if cell.MonetaryCostKnown {
					cell.MonetaryCurrency = job.BudgetCurrency
				}
				judgeSucceeded = true
			} else {
				lastJudgeErr = judgeErr
				cell.Status, cell.Outcome = routerprofile.CellCandidateSucceeded, "judge_error"
				cell.JudgeError, cell.Error = judgeErr.Error(), judgeErr.Error()
			}
			if err := s.routerProfiles.UpdateEvaluationCell(context.Background(), cell); err != nil {
				fail(err)
				return
			}
			if judgeSucceeded {
				break
			}
			if !providerPoolJudgeRetryable(lastJudgeErr) {
				break
			}
		}
		if !judgeSucceeded {
			if lastJudgeErr == nil {
				lastJudgeErr = errors.New("durable judge attempt limit already exhausted")
			}
			fail(fmt.Errorf("judge failed for cell %s after %d/%d attempts; candidate response remains resumable: %w",
				cell.ID, cell.JudgeAttemptCount, providerPoolJudgeMaxAttempts, lastJudgeErr))
			return
		}
		completed++
		_ = s.routerProfiles.UpdateEvaluationJobProgress(context.Background(), job.PoolID, job.ID, "evaluating", completed, len(cells))
	}
	if err := s.generateProviderPoolCandidateProfile(ctx, job, pool); err != nil {
		fail(fmt.Errorf("optimizing router profile: %w", err))
		return
	}
	_ = s.routerProfiles.UpdateEvaluationJobStatus(context.Background(), job.PoolID, job.ID, routerprofile.JobSucceeded, "")
	s.finishProviderPoolEvaluationSuggestion(job.PoolID, routerprofile.SuggestionAccepted)
}

func (s *Server) runProviderPoolListwiseEvaluation(ctx context.Context, job routerprofile.EvaluationJob) {
	fail := func(err error) {
		status := routerprofile.JobFailed
		if errors.Is(err, context.Canceled) {
			current, _, getErr := s.routerProfiles.GetEvaluationJob(context.Background(), job.PoolID, job.ID)
			if getErr == nil && current.CancellationRequested {
				status = routerprofile.JobCancelled
			}
		}
		_ = s.routerProfiles.UpdateEvaluationJobStatus(context.Background(), job.PoolID, job.ID, status, err.Error())
		s.finishProviderPoolEvaluationSuggestion(job.PoolID, routerprofile.SuggestionRejected)
	}
	if _, err := s.routerProfiles.GetProfileBySourceJob(ctx, job.PoolID, job.ID); err == nil {
		_ = s.routerProfiles.UpdateEvaluationJobStatus(context.Background(), job.PoolID, job.ID, routerprofile.JobSucceeded, "")
		s.finishProviderPoolEvaluationSuggestion(job.PoolID, routerprofile.SuggestionAccepted)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(fmt.Errorf("checking existing pairwise profile: %w", err))
		return
	}
	pool, ok := providerPoolByID(job.PoolID)
	if !ok || !pool.Enabled {
		fail(errors.New("provider pool not found or disabled"))
		return
	}
	if providerPoolMemberFingerprint(pool) != job.MemberFingerprint {
		fail(errors.New("provider pool member fingerprint changed"))
		return
	}
	models, err := s.evaluationCatalog(ctx)
	if err != nil {
		fail(err)
		return
	}
	judgeInfo, ok := evaluationCatalogModel(models, "cloud", job.JudgeModel)
	if !ok || judgeInfo.PipelineTag != "text-generation" || !s.hasCloudCredential() {
		fail(errors.New("judge cloud model or credential is unavailable"))
		return
	}
	cells, err := s.routerProfiles.ListEvaluationCells(ctx, job.PoolID, job.ID)
	if err != nil {
		fail(err)
		return
	}
	rounds, err := s.routerProfiles.ListEvaluationRounds(ctx, job.PoolID, job.ID)
	if err != nil {
		fail(err)
		return
	}
	spend, completed := 0.0, 0
	for _, cell := range cells {
		spend += cell.EstimatedCost
		if cell.Status == routerprofile.CellCandidateSucceeded ||
			cell.Status == routerprofile.CellSucceeded || cell.Status == routerprofile.CellFailed {
			completed++
		}
	}
	for _, round := range rounds {
		spend += round.EstimatedCost
		if round.Status == routerprofile.RoundSucceeded {
			completed++
		}
	}
	_ = s.routerProfiles.UpdateEvaluationJobProgress(ctx, job.PoolID, job.ID, "candidates", completed, len(cells)+len(rounds))
	for index := range cells {
		cell := &cells[index]
		if cell.Status == routerprofile.CellCandidateSucceeded ||
			cell.Status == routerprofile.CellSucceeded || cell.Status == routerprofile.CellFailed {
			continue
		}
		if err := s.checkEvaluationCancellation(ctx, job); err != nil {
			fail(err)
			return
		}
		snapshot, err := s.routerProfiles.GetQuerySnapshot(ctx, job.PoolID, cell.QuerySnapshotID)
		if err != nil {
			fail(err)
			return
		}
		replayMessages := providerPoolEvaluationMessages(snapshot)
		inputText := renderProviderPoolEvaluationMessages(replayMessages, 0)
		maxCandidateCost := 0.0
		if cell.PriceKnown {
			maxCandidateCost = evaluationCost(evaluationPrice{
				input: cell.PriceInput, output: cell.PriceOutput, currency: cell.PriceCurrency, known: true,
			}, estimateProviderPoolTextTokens(inputText), job.MaxOutputTokens)
		}
		projectedSpend := spend - cell.EstimatedCost + maxCandidateCost
		if projectedSpend > job.BudgetAmount+1e-12 {
			fail(errors.New("evaluation budget exhausted before candidate call"))
			return
		}
		cell.Status, cell.EstimatedCost = routerprofile.CellRunning, maxCandidateCost
		spend = projectedSpend
		if err := s.routerProfiles.UpdateEvaluationCell(ctx, *cell); err != nil {
			fail(err)
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(job.RequestTimeoutSeconds)*time.Second)
		result, callErr := s.evaluationChatCompletion(callCtx, cell.Candidate.Model, cell.Candidate.Source,
			replayMessages, job.MaxOutputTokens, false)
		cancel()
		cell.LatencyMillis = result.latency.Milliseconds()
		cell.CandidateResponse, cell.PromptTokens = result.content, result.promptTokens
		cell.OutputTokens, cell.TotalTokens, cell.FinishReason = result.outputTokens, result.totalTokens, result.finishReason
		if cell.TotalTokens == 0 && callErr == nil {
			cell.PromptTokens = estimateProviderPoolTextTokens(inputText)
			cell.OutputTokens = estimateProviderPoolTextTokens(result.content)
			cell.TotalTokens = cell.PromptTokens + cell.OutputTokens
		}
		reservedCost := cell.EstimatedCost
		if cell.PriceKnown && callErr == nil {
			cell.EstimatedCost = evaluationCost(evaluationPrice{
				input: cell.PriceInput, output: cell.PriceOutput, currency: cell.PriceCurrency, known: true,
			}, cell.PromptTokens, cell.OutputTokens)
		}
		spend += cell.EstimatedCost - reservedCost
		if callErr != nil || strings.TrimSpace(result.content) == "" {
			if (errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded)) && ctx.Err() != nil {
				fail(ctx.Err())
				return
			}
			if callErr == nil {
				callErr = errors.New("candidate returned an empty answer")
			}
			cell.Status, cell.Outcome = routerprofile.CellSucceeded, "candidate_error"
			cell.QualitySource, cell.CandidateAvailable = routerprofile.QualitySourceCandidateFailure, false
			cell.CandidateResponse = ""
			cell.CandidateError, cell.Error = callErr.Error(), callErr.Error()
		} else {
			cell.Status, cell.Outcome = routerprofile.CellCandidateSucceeded, "candidate_completed"
			cell.CandidateAvailable, cell.CandidateError, cell.Error = true, "", ""
		}
		cell.MonetaryCostKnown = cell.PriceKnown
		if cell.MonetaryCostKnown {
			cell.MonetaryCurrency = job.BudgetCurrency
		}
		if err := s.routerProfiles.UpdateEvaluationCell(context.Background(), *cell); err != nil {
			fail(err)
			return
		}
		completed++
		_ = s.routerProfiles.UpdateEvaluationJobProgress(context.Background(), job.PoolID, job.ID,
			"candidates", completed, len(cells)+len(rounds))
	}

	cellByID := make(map[string]routerprofile.EvaluationCell, len(cells))
	for _, cell := range cells {
		cellByID[cell.ID] = cell
	}
	for index := range rounds {
		round := &rounds[index]
		if round.Status == routerprofile.RoundSucceeded {
			continue
		}
		if err := s.checkEvaluationCancellation(ctx, job); err != nil {
			fail(err)
			return
		}
		snapshot, err := s.routerProfiles.GetQuerySnapshot(ctx, job.PoolID, round.QuerySnapshotID)
		if err != nil {
			fail(err)
			return
		}
		promptCandidates := make([]listwisePromptCandidate, len(round.CandidateReferences))
		for candidateIndex, reference := range round.CandidateReferences {
			cell, exists := cellByID[reference.CellID]
			if !exists || (cell.Status != routerprofile.CellCandidateSucceeded &&
				cell.Status != routerprofile.CellSucceeded && cell.Status != routerprofile.CellFailed) {
				fail(fmt.Errorf("evaluation round %s has a non-durable candidate reference", round.ID))
				return
			}
			promptCandidates[candidateIndex] = listwisePromptCandidate{
				Alias: reference.Alias, Answer: cell.CandidateResponse, Available: cell.CandidateAvailable,
			}
		}
		judgePrompt := providerPoolListwiseJudgePrompt(
			renderProviderPoolEvaluationMessages(providerPoolEvaluationMessages(snapshot), providerPoolJudgeContextBytes), promptCandidates)
		judgePrice := evaluationPrice{
			input: round.JudgePriceInput, output: round.JudgePriceOutput,
			currency: round.JudgePriceCurrency, known: round.JudgePriceKnown,
		}
		var lastJudgeErr error
		for round.JudgeAttemptCount < providerPoolJudgeMaxAttempts {
			if err := s.checkEvaluationCancellation(ctx, job); err != nil {
				fail(err)
				return
			}
			maxJudgeCost := 0.0
			if judgePrice.known {
				maxJudgeCost = evaluationCost(judgePrice, estimateProviderPoolTextTokens(judgePrompt), providerPoolJudgeMaxTokens)
			}
			if spend+maxJudgeCost > job.BudgetAmount+1e-12 {
				fail(errors.New("evaluation budget exhausted before listwise judge retry"))
				return
			}
			round.JudgeAttemptCount++
			round.EstimatedCost += maxJudgeCost
			spend += maxJudgeCost
			round.Status, round.Error = routerprofile.RoundRunning, ""
			if err := s.routerProfiles.UpdateEvaluationRound(context.Background(), *round); err != nil {
				fail(err)
				return
			}
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(job.RequestTimeoutSeconds)*time.Second)
			judged, judgeErr := s.evaluationChatCompletion(callCtx, job.JudgeModel, "cloud",
				[]routerprofile.Message{{Role: "user", Content: judgePrompt}}, providerPoolJudgeMaxTokens, true)
			cancel()
			if (errors.Is(judgeErr, context.Canceled) || errors.Is(judgeErr, context.DeadlineExceeded)) && ctx.Err() != nil {
				fail(ctx.Err())
				return
			}
			promptTokens, outputTokens, totalTokens := judged.promptTokens, judged.outputTokens, judged.totalTokens
			if totalTokens == 0 && judgeErr == nil {
				promptTokens = estimateProviderPoolTextTokens(judgePrompt)
				outputTokens = estimateProviderPoolTextTokens(judged.content)
				totalTokens = promptTokens + outputTokens
			}
			round.JudgePromptTokens += promptTokens
			round.JudgeOutputTokens += outputTokens
			round.JudgeTotalTokens += totalTokens
			if judgePrice.known && totalTokens > 0 {
				actualCost := evaluationCost(judgePrice, promptTokens, outputTokens)
				round.EstimatedCost += actualCost - maxJudgeCost
				spend += actualCost - maxJudgeCost
			}
			if judgeErr == nil {
				round.Result, judgeErr = parseProviderPoolListwiseJudge(judged.content, round.CandidateReferences)
			}
			if judgeErr == nil {
				judgeErr = validateUnavailableListwiseResults(round.Result, round.CandidateReferences, cellByID)
			}
			if judgeErr == nil {
				round.Status, round.Error = routerprofile.RoundSucceeded, ""
				preferences, expandErr := expandProviderPoolListwisePreferences(*round)
				if expandErr != nil {
					fail(expandErr)
					return
				}
				if err := s.routerProfiles.CompleteEvaluationRound(context.Background(), *round, preferences); err != nil {
					fail(err)
					return
				}
				lastJudgeErr = nil
				break
			}
			lastJudgeErr = judgeErr
			round.Status, round.Error = routerprofile.RoundCandidateReady, judgeErr.Error()
			if err := s.routerProfiles.UpdateEvaluationRound(context.Background(), *round); err != nil {
				fail(err)
				return
			}
			if !providerPoolJudgeRetryable(judgeErr) {
				break
			}
		}
		if lastJudgeErr != nil || round.Status != routerprofile.RoundSucceeded {
			if lastJudgeErr == nil {
				lastJudgeErr = errors.New("durable listwise judge attempt limit already exhausted")
			}
			fail(fmt.Errorf("listwise judge failed for round %s after %d/%d attempts: %w",
				round.ID, round.JudgeAttemptCount, providerPoolJudgeMaxAttempts, lastJudgeErr))
			return
		}
		completed++
		_ = s.routerProfiles.UpdateEvaluationJobProgress(context.Background(), job.PoolID, job.ID,
			"judging", completed, len(cells)+len(rounds))
	}
	if err := s.generateProviderPoolPairwiseProfile(ctx, job, pool); err != nil {
		fail(fmt.Errorf("building pairwise router profile: %w", err))
		return
	}
	_ = s.routerProfiles.UpdateEvaluationJobStatus(context.Background(), job.PoolID, job.ID, routerprofile.JobSucceeded, "")
	s.finishProviderPoolEvaluationSuggestion(job.PoolID, routerprofile.SuggestionAccepted)
}

func (s *Server) finishProviderPoolEvaluationSuggestion(poolID string, status routerprofile.SuggestionStatus) {
	suggestions, err := s.routerProfiles.ListSuggestions(context.Background(), poolID,
		routerprofile.SuggestionEvaluating, routerprofile.ListOptions{Limit: 1})
	if err == nil && len(suggestions) > 0 {
		_ = s.routerProfiles.UpdateSuggestionStatus(context.Background(), poolID, suggestions[0].ID, status)
	}
}

func (s *Server) generateProviderPoolCandidateProfile(ctx context.Context, job routerprofile.EvaluationJob, pool config.ProviderPool) error {
	if _, err := s.routerProfiles.GetProfileBySourceJob(ctx, job.PoolID, job.ID); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	cells, err := s.routerProfiles.ListEvaluationCells(ctx, job.PoolID, job.ID)
	if err != nil {
		return err
	}
	snapshotIDs := make(map[string]struct{})
	for _, cell := range cells {
		snapshotIDs[cell.QuerySnapshotID] = struct{}{}
	}
	snapshots := make([]routerprofile.QuerySnapshot, 0, len(snapshotIDs))
	for snapshotID := range snapshotIDs {
		snapshot, err := s.routerProfiles.GetQuerySnapshot(ctx, job.PoolID, snapshotID)
		if err != nil {
			return err
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })
	candidates := make([]routerprofile.CandidateBindingV1, 0, len(pool.Members))
	for _, member := range pool.Members {
		candidates = append(candidates, routerprofile.CandidateBindingV1{
			MemberID: member.ID, Source: member.Source, Model: member.Model, Weight: float64(member.Weight),
		})
	}
	artifact, err := routerprofile.OptimizeEvaluation(routerprofile.OptimizerInput{
		Job: job, Snapshots: snapshots, Cells: cells, Candidates: candidates,
		Config: routerprofile.OptimizerConfig{
			QualityWeight: routerprofile.DefaultQualityWeight, CostWeight: routerprofile.DefaultCostWeight,
			EmbeddingRevision: "unversioned", GeneratedAt: job.CreatedAt,
		},
	})
	if err != nil {
		return err
	}
	version, err := s.routerProfiles.NextProfileVersion(ctx, job.PoolID)
	if err != nil {
		return err
	}
	_, err = s.routerProfiles.CreateProfile(ctx, routerprofile.Profile{
		PoolID: job.PoolID, ID: "profile-" + job.ID, Version: version,
		MemberFingerprint: job.MemberFingerprint, Profile: artifact,
		CreatedAt: job.CreatedAt, CreatedBy: "evaluation-optimizer", SourceJobID: job.ID,
		Description: "Candidate generated by evaluation job; activation is manual.",
	})
	return err
}

func (s *Server) generateProviderPoolPairwiseProfile(ctx context.Context, job routerprofile.EvaluationJob, pool config.ProviderPool) error {
	if _, err := s.routerProfiles.GetProfileBySourceJob(ctx, job.PoolID, job.ID); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	cells, err := s.routerProfiles.ListEvaluationCells(ctx, job.PoolID, job.ID)
	if err != nil {
		return err
	}
	rounds, err := s.routerProfiles.ListEvaluationRounds(ctx, job.PoolID, job.ID)
	if err != nil {
		return err
	}
	preferences, err := s.routerProfiles.ListEvaluationPreferences(ctx, job.PoolID, job.ID)
	if err != nil {
		return err
	}
	snapshotSet := make(map[string]struct{})
	for _, round := range rounds {
		snapshotSet[round.QuerySnapshotID] = struct{}{}
	}
	snapshots := make([]routerprofile.QuerySnapshot, 0, len(snapshotSet))
	for id := range snapshotSet {
		snapshot, loadErr := s.routerProfiles.GetQuerySnapshot(ctx, job.PoolID, id)
		if loadErr != nil {
			return loadErr
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })
	if len(snapshots) == 0 || len(snapshots[0].Embedding) == 0 ||
		strings.TrimSpace(snapshots[0].EmbeddingModel) == "" {
		return errors.New("pairwise profile requires frozen snapshot embeddings")
	}
	for _, snapshot := range snapshots {
		if snapshot.EmbeddingModel != snapshots[0].EmbeddingModel ||
			len(snapshot.Embedding) != len(snapshots[0].Embedding) {
			return errors.New("pairwise frozen embedding contracts do not match")
		}
	}
	candidates := pairwiseCandidateBindings(pool, cells)
	artifact, err := routerprofile.BuildRouterProfileV2(routerprofile.RouterProfileV2Input{
		PoolID: job.PoolID, MemberFingerprint: job.MemberFingerprint, SourceJobID: job.ID,
		Judge:       routerprofile.JudgeProvenance{Model: job.JudgeModel, PromptVersion: job.JudgePromptVersion},
		RoutingText: routerprofile.RoutingTextConfig{Version: "routing-text-v1", MaxTokens: 8192},
		Embedding: routerprofile.EmbeddingConfig{
			Model: snapshots[0].EmbeddingModel, Revision: "frozen-at-job-creation",
			Dimensions: len(snapshots[0].Embedding),
		},
		Candidates: candidates, Snapshots: snapshots, Rounds: rounds,
		Preferences: preferences, Cells: cells, GeneratedAt: job.CreatedAt,
		Seed: routerprofile.DefaultPairwiseSeed,
	})
	if err != nil {
		return err
	}
	version, err := s.routerProfiles.NextProfileVersion(ctx, job.PoolID)
	if err != nil {
		return err
	}
	_, err = s.routerProfiles.CreateProfile(ctx, routerprofile.Profile{
		PoolID: job.PoolID, ID: "profile-" + job.ID, Version: version,
		MemberFingerprint: job.MemberFingerprint, SchemaVersion: routerprofile.SchemaVersionV2,
		ProfileV2: &artifact, CreatedAt: job.CreatedAt, CreatedBy: "pairwise-profile-builder",
		SourceJobID: job.ID, Description: "Pairwise V2 candidate generated from durable listwise evidence; activation is manual.",
	})
	return err
}

func pairwiseCandidateBindings(pool config.ProviderPool, cells []routerprofile.EvaluationCell) []routerprofile.CandidateBindingV2 {
	type costs struct {
		values   []float64
		currency string
		unknown  int
		mixed    bool
	}
	byMember := make(map[string]*costs, len(pool.Members))
	for _, member := range pool.Members {
		byMember[member.ID] = &costs{}
	}
	for _, cell := range cells {
		item := byMember[cell.CandidateMemberID]
		if item == nil {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(cell.PriceCurrency))
		if !cell.PriceKnown || currency == "" {
			item.unknown++
			continue
		}
		if item.currency == "" {
			item.currency = currency
		} else if item.currency != currency {
			item.mixed = true
		}
		item.values = append(item.values, (float64(cell.PromptTokens)*cell.PriceInput+
			float64(cell.OutputTokens)*cell.PriceOutput)/1_000_000)
	}
	result := make([]routerprofile.CandidateBindingV2, 0, len(pool.Members))
	for _, member := range pool.Members {
		item := byMember[member.ID]
		sort.Float64s(item.values)
		cost := routerprofile.CandidateCostV2{SampleCount: len(item.values), UnknownCount: item.unknown}
		if len(item.values) > 0 && item.unknown == 0 && !item.mixed {
			cost.Known, cost.Currency = true, item.currency
			for _, value := range item.values {
				cost.Mean += value
			}
			cost.Mean /= float64(len(item.values))
			cost.P50 = item.values[(len(item.values)-1)/2]
			cost.P95 = item.values[int(math.Ceil(.95*float64(len(item.values))))-1]
		}
		result = append(result, routerprofile.CandidateBindingV2{
			MemberID: member.ID, Source: member.Source, Model: member.Model,
			Weight: float64(member.Weight), Cost: cost,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].MemberID < result[j].MemberID })
	return result
}

func (s *Server) evaluationChatCompletion(ctx context.Context, model, source string, messages []routerprofile.Message, maxTokens int, judge bool) (evaluationCallResult, error) {
	started := time.Now()
	eng, err := s.evaluationEngine(ctx, model, source)
	if err != nil {
		return evaluationCallResult{latency: time.Since(started)}, err
	}
	proxy, ok := eng.(inference.ChatCompletionProxier)
	if !ok {
		return evaluationCallResult{latency: time.Since(started)}, errors.New("evaluation target does not support chat completions")
	}
	requestMessages := make([]map[string]interface{}, 0, len(messages))
	for _, message := range messages {
		requestMessages = append(requestMessages, map[string]interface{}{
			"role": message.Role, "content": message.Content,
		})
	}
	request := map[string]interface{}{
		"model": model, "stream": false, "max_tokens": maxTokens,
		"messages": requestMessages,
	}
	if judge {
		request["temperature"] = 0
		request["response_format"] = map[string]interface{}{"type": "json_object"}
		request["thinking"] = map[string]interface{}{"type": "disabled"}
	}
	response, err := proxy.ChatCompletion(ctx, request)
	result := evaluationCallResult{latency: time.Since(started)}
	if err != nil {
		return result, err
	}
	if response == nil {
		return result, errors.New("evaluation response is empty")
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return result, inference.NewHTTPStatusError(response.StatusCode,
			"evaluation call failed: "+strings.TrimSpace(string(body)))
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&payload); err != nil {
		return result, fmt.Errorf("decoding evaluation response: %w", err)
	}
	if len(payload.Choices) != 1 {
		return result, errors.New("evaluation response must contain exactly one choice")
	}
	result.content, result.finishReason = payload.Choices[0].Message.Content, payload.Choices[0].FinishReason
	result.promptTokens, result.outputTokens, result.totalTokens =
		payload.Usage.PromptTokens, payload.Usage.CompletionTokens, payload.Usage.TotalTokens
	return result, nil
}

func providerPoolJudgeRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return providerPoolRetryable(err)
}

func providerPoolJudgePrompt(query, answer string) string {
	return "You are an impartial answer-quality judge. Evaluate only correctness, relevance, completeness, and clarity. " +
		"Return exactly one JSON object with no markdown and no extra keys: {\"score\": <number from 0 to 1>, \"reason\": <brief string>}.\n" +
		"REDACTED CONVERSATION CONTEXT:\n" + query + "\n\nCANDIDATE ANSWER:\n" + answer
}

type listwisePromptCandidate struct {
	Alias     string
	Answer    string
	Available bool
}

func providerPoolListwiseJudgePrompt(conversation string, candidates []listwisePromptCandidate) string {
	var builder strings.Builder
	builder.WriteString("You are an impartial answer-quality judge. Compare anonymous candidate answers only for correctness, relevance, completeness, and clarity. ")
	builder.WriteString("Return exactly one JSON object with no markdown and no extra keys: {\"candidates\":[{\"alias\":\"A\",\"rank\":1,\"score\":0.9,\"acceptable\":true,\"reason\":\"concise reason\"}]}. ")
	builder.WriteString("Include every presented alias exactly once and no other aliases. Ranks must be the unique integers 1 through the candidate count. ")
	builder.WriteString("Unavailable candidates must be included, scored 0, marked unacceptable, and ranked after every available candidate.\n")
	builder.WriteString("REDACTED CONVERSATION CONTEXT:\n")
	builder.WriteString(conversation)
	builder.WriteString("\n\nANONYMOUS CANDIDATES:\n")
	for _, candidate := range candidates {
		builder.WriteString("CANDIDATE ")
		builder.WriteString(candidate.Alias)
		if !candidate.Available {
			builder.WriteString(": [UNAVAILABLE]\n")
			continue
		}
		builder.WriteString(":\n")
		builder.WriteString(candidate.Answer)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func parseProviderPoolListwiseJudge(value string, references []routerprofile.RoundCandidateReference) (routerprofile.ListwiseResult, error) {
	type wireCandidate struct {
		Alias      *string  `json:"alias"`
		Rank       *int     `json:"rank"`
		Score      *float64 `json:"score"`
		Acceptable *bool    `json:"acceptable"`
		Reason     *string  `json:"reason"`
	}
	var wire struct {
		Candidates *[]wireCandidate `json:"candidates"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return routerprofile.ListwiseResult{}, fmt.Errorf("malformed listwise judge JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF || wire.Candidates == nil ||
		len(*wire.Candidates) != len(references) {
		return routerprofile.ListwiseResult{}, errors.New("listwise judge must contain exactly every candidate")
	}
	expectedAliases := make(map[string]struct{}, len(references))
	for _, reference := range references {
		expectedAliases[reference.Alias] = struct{}{}
	}
	seenAliases := make(map[string]struct{}, len(references))
	seenRanks := make(map[int]struct{}, len(references))
	result := routerprofile.ListwiseResult{Candidates: make([]routerprofile.ListwiseCandidateResult, 0, len(references))}
	for _, candidate := range *wire.Candidates {
		if candidate.Alias == nil || candidate.Rank == nil || candidate.Score == nil ||
			candidate.Acceptable == nil || candidate.Reason == nil {
			return routerprofile.ListwiseResult{}, errors.New("listwise judge candidate fields are required")
		}
		alias, reason := strings.TrimSpace(*candidate.Alias), strings.TrimSpace(*candidate.Reason)
		if _, exists := expectedAliases[alias]; !exists {
			return routerprofile.ListwiseResult{}, fmt.Errorf("listwise judge returned unexpected alias %q", alias)
		}
		if _, duplicate := seenAliases[alias]; duplicate {
			return routerprofile.ListwiseResult{}, fmt.Errorf("listwise judge duplicated alias %q", alias)
		}
		if *candidate.Rank < 1 || *candidate.Rank > len(references) {
			return routerprofile.ListwiseResult{}, errors.New("listwise judge rank is out of range")
		}
		if _, duplicate := seenRanks[*candidate.Rank]; duplicate {
			return routerprofile.ListwiseResult{}, errors.New("listwise judge ranks must be unique")
		}
		if math.IsNaN(*candidate.Score) || math.IsInf(*candidate.Score, 0) ||
			*candidate.Score < 0 || *candidate.Score > 1 || reason == "" || utf8.RuneCountInString(reason) > 512 {
			return routerprofile.ListwiseResult{}, errors.New("listwise judge score or reason is invalid")
		}
		seenAliases[alias], seenRanks[*candidate.Rank] = struct{}{}, struct{}{}
		result.Candidates = append(result.Candidates, routerprofile.ListwiseCandidateResult{
			Alias: alias, Rank: *candidate.Rank, Score: *candidate.Score,
			Acceptable: *candidate.Acceptable, Reason: reason,
		})
	}
	sort.Slice(result.Candidates, func(i, j int) bool {
		return result.Candidates[i].Alias < result.Candidates[j].Alias
	})
	return result, nil
}

func validateUnavailableListwiseResults(result routerprofile.ListwiseResult, references []routerprofile.RoundCandidateReference, cells map[string]routerprofile.EvaluationCell) error {
	resultByAlias := make(map[string]routerprofile.ListwiseCandidateResult, len(result.Candidates))
	for _, candidate := range result.Candidates {
		resultByAlias[candidate.Alias] = candidate
	}
	bestUnavailableRank := len(references) + 1
	worstAvailableRank := 0
	for _, reference := range references {
		cell := cells[reference.CellID]
		candidate := resultByAlias[reference.Alias]
		if cell.CandidateAvailable {
			worstAvailableRank = max(worstAvailableRank, candidate.Rank)
			continue
		}
		if candidate.Acceptable || candidate.Score != 0 {
			return fmt.Errorf("unavailable alias %s must be scored zero and unacceptable", reference.Alias)
		}
		bestUnavailableRank = min(bestUnavailableRank, candidate.Rank)
	}
	if worstAvailableRank >= bestUnavailableRank {
		return errors.New("unavailable candidates must rank after available candidates")
	}
	return nil
}

func expandProviderPoolListwisePreferences(round routerprofile.EvaluationRound) ([]routerprofile.EvaluationPreference, error) {
	resultByAlias := make(map[string]routerprofile.ListwiseCandidateResult, len(round.Result.Candidates))
	for _, candidate := range round.Result.Candidates {
		resultByAlias[candidate.Alias] = candidate
	}
	preferences := make([]routerprofile.EvaluationPreference, 0,
		len(round.CandidateReferences)*(len(round.CandidateReferences)-1)/2)
	for left := 0; left < len(round.CandidateReferences); left++ {
		for right := left + 1; right < len(round.CandidateReferences); right++ {
			aRef, bRef := round.CandidateReferences[left], round.CandidateReferences[right]
			if aRef.CandidateMemberID > bRef.CandidateMemberID {
				aRef, bRef = bRef, aRef
			}
			a, aOK := resultByAlias[aRef.Alias]
			b, bOK := resultByAlias[bRef.Alias]
			if !aOK || !bOK {
				return nil, errors.New("cannot expand incomplete listwise result")
			}
			outcomeClass := routerprofile.PreferenceClassNeitherAcceptable
			switch {
			case a.Acceptable && !b.Acceptable:
				outcomeClass = routerprofile.PreferenceClassAAcceptableBNot
			case a.Acceptable && b.Acceptable:
				outcomeClass = routerprofile.PreferenceClassBothAcceptable
			case !a.Acceptable && b.Acceptable:
				outcomeClass = routerprofile.PreferenceClassANotBAcceptable
			}
			relation := routerprofile.PreferenceRelationTied
			if a.Rank < b.Rank {
				relation = routerprofile.PreferenceRelationAPreferred
			} else if b.Rank < a.Rank {
				relation = routerprofile.PreferenceRelationBPreferred
			}
			preferences = append(preferences, routerprofile.EvaluationPreference{
				PoolID: round.PoolID, JobID: round.JobID, RoundID: round.ID,
				QuerySnapshotID: round.QuerySnapshotID, Repeat: round.Repeat,
				MemberAID: aRef.CandidateMemberID, MemberBID: bRef.CandidateMemberID,
				CellAID: aRef.CellID, CellBID: bRef.CellID, AliasA: aRef.Alias, AliasB: bRef.Alias,
				OutcomeClassID: outcomeClass, RankingRelation: relation,
				Confidence: math.Abs(a.Score - b.Score),
				Reason:     truncateProviderPoolEvaluationText("A: "+a.Reason+"; B: "+b.Reason, 1024),
				RankA:      a.Rank, RankB: b.Rank, ScoreA: a.Score, ScoreB: b.Score,
				AcceptableA: a.Acceptable, AcceptableB: b.Acceptable,
			})
		}
	}
	return preferences, nil
}

func providerPoolEvaluationMessages(snapshot routerprofile.QuerySnapshot) []routerprofile.Message {
	if messages := routerprofile.BoundReplayMessages(snapshot.RedactedMessages); len(messages) > 0 {
		return messages
	}
	return []routerprofile.Message{{Role: "user", Content: snapshot.RedactedQuery}}
}

func renderProviderPoolEvaluationMessages(messages []routerprofile.Message, limit int) string {
	var builder strings.Builder
	for _, message := range messages {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(strings.ToUpper(message.Role))
		builder.WriteString(": ")
		builder.WriteString(message.Content)
	}
	if limit <= 0 {
		return builder.String()
	}
	return truncateProviderPoolEvaluationText(builder.String(), limit)
}

func truncateProviderPoolEvaluationText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	const marker = "\n... context truncated ...\n"
	if limit <= len(marker) {
		data := []byte(value[:limit])
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
		return string(data)
	}
	contentBytes := limit - len(marker)
	headBytes := (contentBytes + 1) / 2
	tailBytes := contentBytes - headBytes
	head := []byte(value[:headBytes])
	for len(head) > 0 && !utf8.Valid(head) {
		head = head[:len(head)-1]
	}
	tail := []byte(value[len(value)-tailBytes:])
	for len(tail) > 0 && !utf8.Valid(tail) {
		tail = tail[1:]
	}
	return string(head) + marker + string(tail)
}

func parseProviderPoolJudge(value string) (float64, string, error) {
	var result struct {
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return 0, "", fmt.Errorf("malformed judge JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF || math.IsNaN(result.Score) || math.IsInf(result.Score, 0) ||
		result.Score < 0 || result.Score > 1 || strings.TrimSpace(result.Reason) == "" {
		return 0, "", errors.New("malformed judge JSON score or reason")
	}
	return result.Score, strings.TrimSpace(result.Reason), nil
}

func (s *Server) evaluationEngine(ctx context.Context, model, source string) (inference.Engine, error) {
	if poolIDFromSource(source) != "" {
		return nil, errors.New("evaluation cannot target a provider pool")
	}
	if s.evaluationEngineFactory != nil {
		return s.evaluationEngineFactory(ctx, model, source)
	}
	return s.getChatEngine(withoutProviderRouteSource(ctx), model, source, 0, 0, -1, "", "", "")
}

func (s *Server) evaluationCatalog(ctx context.Context) ([]api.ModelInfo, error) {
	if s.evaluationCatalogLoader != nil {
		return s.evaluationCatalogLoader(ctx)
	}
	return s.listAvailableModelsWithRefresh(ctx, true)
}

func evaluationCatalogModel(models []api.ModelInfo, source, model string) (api.ModelInfo, bool) {
	for _, item := range models {
		if strings.EqualFold(strings.TrimSpace(item.Source), strings.TrimSpace(source)) &&
			strings.TrimSpace(item.Model) == strings.TrimSpace(model) {
			return item, true
		}
	}
	return api.ModelInfo{}, false
}

func evaluationModelPrice(info api.ModelInfo, currency string) evaluationPrice {
	if info.Pricing == nil || info.Pricing.InputTokenPrice == nil || info.Pricing.OutputTokenPrice == nil {
		return evaluationPrice{}
	}
	input, output := info.Pricing.InputTokenPrice, info.Pricing.OutputTokenPrice
	inputCurrency := normalizeProviderPoolEvaluationCurrency(input.Currency)
	outputCurrency := normalizeProviderPoolEvaluationCurrency(output.Currency)
	if input.PricePerMillion < 0 || output.PricePerMillion < 0 ||
		inputCurrency == "" || inputCurrency != outputCurrency ||
		inputCurrency != normalizeProviderPoolEvaluationCurrency(currency) {
		return evaluationPrice{}
	}
	return evaluationPrice{input: input.PricePerMillion, output: output.PricePerMillion, currency: inputCurrency, known: true}
}

func evaluationCost(price evaluationPrice, inputTokens, outputTokens int) float64 {
	return (float64(inputTokens)*price.input + float64(outputTokens)*price.output) / 1_000_000
}

func normalizeProviderPoolEvaluationRequest(value ProviderPoolEvaluationRequest) ProviderPoolEvaluationRequest {
	value.PoolID, value.BaseProfileID, value.JudgeModel = strings.TrimSpace(value.PoolID), strings.TrimSpace(value.BaseProfileID), strings.TrimSpace(value.JudgeModel)
	value.EvaluationMode = strings.TrimSpace(value.EvaluationMode)
	if value.EvaluationMode == "" {
		value.EvaluationMode = routerprofile.EvaluationModeListwiseV2
	}
	value.BudgetCurrency = normalizeProviderPoolEvaluationCurrency(value.BudgetCurrency)
	if value.RequestTimeoutSeconds == 0 {
		value.RequestTimeoutSeconds = 60
	}
	return value
}

func normalizeProviderPoolEvaluationCurrency(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "CNY", "RMB", "¥", "￥":
		return "￥"
	case "USD", "$":
		return "USD"
	default:
		return ""
	}
}

func validateProviderPoolEvaluationRequest(value ProviderPoolEvaluationRequest) error {
	if value.PoolID == "" || value.JudgeModel == "" || value.BudgetCurrency == "" {
		return errors.New("pool, judge model, and a supported budget currency (CNY or USD) are required")
	}
	if value.MaxQueries < 1 || value.MaxQueries > routerprofile.MaxEvaluationQueries ||
		value.Repeats < 1 || value.Repeats > routerprofile.MaxEvaluationRepeats ||
		value.MaxOutputTokens < 1 || value.MaxOutputTokens > routerprofile.MaxEvaluationOutputTokens ||
		value.RequestTimeoutSeconds < 1 || value.RequestTimeoutSeconds > 600 ||
		value.BudgetAmount < 0 || math.IsNaN(value.BudgetAmount) || math.IsInf(value.BudgetAmount, 0) {
		return errors.New("evaluation request exceeds hard limits")
	}
	if value.EvaluationMode != routerprofile.EvaluationModeAbsoluteV1 &&
		value.EvaluationMode != routerprofile.EvaluationModeListwiseV2 {
		return errors.New("evaluation_mode must be absolute_v1 or listwise_v2")
	}
	return nil
}

func (s *Server) checkEvaluationCancellation(ctx context.Context, job routerprofile.EvaluationJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, _, err := s.routerProfiles.GetEvaluationJob(ctx, job.PoolID, job.ID)
	if err != nil {
		return err
	}
	if current.CancellationRequested {
		return context.Canceled
	}
	return nil
}
