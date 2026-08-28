package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	routerprofile "github.com/opencsgs/semantic-router"
	"github.com/opencsgs/csglite/pkg/api"
)

type evaluationFakeEngine struct {
	model string
	call  func(context.Context, map[string]interface{}) (*http.Response, error)
}

func (e *evaluationFakeEngine) ModelName() string { return e.model }
func (e *evaluationFakeEngine) Close() error      { return nil }
func (e *evaluationFakeEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}
func (e *evaluationFakeEngine) Chat(context.Context, []inference.Message, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}
func (e *evaluationFakeEngine) ChatCompletion(ctx context.Context, body map[string]interface{}) (*http.Response, error) {
	return e.call(ctx, body)
}

func TestProviderPoolEvaluationPreviewPricingLimitsAndUnknownMembers(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{
		{ID: "known", Source: "cloud", Model: "candidate-known"},
		{ID: "unknown", Source: "local", Model: "candidate-unknown"},
	})
	createEvaluationSnapshots(t, s, 2)
	s.evaluationCatalogLoader = func(context.Context) ([]api.ModelInfo, error) {
		return []api.ModelInfo{
			evaluationModelInfo("cloud", "candidate-known", 1, 2),
			{Source: "local", Model: "candidate-unknown", PipelineTag: "text-generation"},
			evaluationModelInfo("cloud", "judge", 3, 4),
		}, nil
	}
	preview, err := s.PreviewProviderPoolEvaluation(t.Context(), ProviderPoolEvaluationRequest{
		PoolID: "evaluation-pool", EvaluationMode: routerprofile.EvaluationModeAbsoluteV1,
		JudgeModel: "judge", MaxQueries: 2, Repeats: 2,
		MaxOutputTokens: 10, RequestTimeoutSeconds: 5, BudgetCurrency: "USD", BudgetAmount: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.EligibleSnapshotCount != 2 || preview.DirectCandidateCalls != 8 || preview.JudgeCalls != 8 {
		t.Fatalf("preview counts = %+v", preview)
	}
	if preview.MaxJudgeCalls != 24 || preview.MaxTotalCalls != 32 {
		t.Fatalf("preview retry call bounds = %+v", preview)
	}
	if len(preview.UnknownPriceMembers) != 1 || preview.UnknownPriceMembers[0].Model != "candidate-unknown" {
		t.Fatalf("unknown members = %+v", preview.UnknownPriceMembers)
	}
	if !preview.RequiresUnknownPricingConsent {
		t.Fatal("unknown pricing did not require explicit consent")
	}
	request := ProviderPoolEvaluationRequest{
		PoolID: "evaluation-pool", EvaluationMode: routerprofile.EvaluationModeAbsoluteV1,
		JudgeModel: "judge", MaxQueries: 1, Repeats: 1,
		MaxOutputTokens: 10, RequestTimeoutSeconds: 5, BudgetCurrency: "USD", BudgetAmount: 100,
	}
	if _, err := s.CreateProviderPoolEvaluationJob(t.Context(), request); err == nil {
		t.Fatal("unknown-priced job was accepted without consent")
	}
	request.AllowUnknownPricing = true
	job, err := s.CreateProviderPoolEvaluationJob(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !job.AllowUnknownPricing {
		t.Fatal("unknown pricing consent was not persisted")
	}
	if preview.KnownEstimatedCost <= 0 || preview.MaxTokenExposure <= 0 ||
		preview.Limits.MaxQueries != routerprofile.MaxEvaluationQueries {
		t.Fatalf("preview pricing or limits = %+v", preview)
	}
	_, err = s.PreviewProviderPoolEvaluation(t.Context(), ProviderPoolEvaluationRequest{
		PoolID: "evaluation-pool", JudgeModel: "judge", MaxQueries: 101, Repeats: 1,
		MaxOutputTokens: 10, BudgetCurrency: "USD",
	})
	if err == nil {
		t.Fatal("query hard limit was not enforced")
	}
}

func TestProviderPoolEvaluationCurrencyNormalization(t *testing.T) {
	for input, want := range map[string]string{
		"CNY": "￥",
		"rmb": "￥",
		"¥":   "￥",
		"￥":   "￥",
		"usd": "USD",
		"$":   "USD",
	} {
		if got := normalizeProviderPoolEvaluationCurrency(input); got != want {
			t.Fatalf("normalize currency %q = %q, want %q", input, got, want)
		}
	}
	request := normalizeProviderPoolEvaluationRequest(ProviderPoolEvaluationRequest{
		PoolID: "pool", JudgeModel: "judge", MaxQueries: 1, Repeats: 1,
		MaxOutputTokens: 1, RequestTimeoutSeconds: 1, BudgetCurrency: "EUR",
	})
	if err := validateProviderPoolEvaluationRequest(request); err == nil {
		t.Fatal("unsupported evaluation currency was accepted")
	}
}

func TestProviderPoolEvaluationTargetsMemberDirectlyAndJudgeIsAnonymous(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{
		{ID: "member", Source: "provider:test", Model: "secret-candidate"},
	})
	createEvaluationSnapshots(t, s, 1)
	s.evaluationCatalogLoader = func(context.Context) ([]api.ModelInfo, error) {
		return []api.ModelInfo{
			evaluationModelInfo("provider:test", "secret-candidate", 1, 1),
			evaluationModelInfo("cloud", "judge", 1, 1),
		}, nil
	}
	var mu sync.Mutex
	var targets, prompts []string
	var payloads [][]map[string]interface{}
	s.evaluationEngineFactory = func(_ context.Context, model, source string) (inference.Engine, error) {
		return &evaluationFakeEngine{model: model, call: func(_ context.Context, body map[string]interface{}) (*http.Response, error) {
			messages := body["messages"].([]map[string]interface{})
			prompt := messages[0]["content"].(string)
			mu.Lock()
			targets = append(targets, source+":"+model)
			prompts = append(prompts, prompt)
			payloads = append(payloads, messages)
			mu.Unlock()
			if model == "judge" {
				return evaluationResponse(`{"score":0.8,"reason":"correct"}`, 30, 8), nil
			}
			return evaluationResponse("candidate answer", 6, 4), nil
		}}, nil
	}
	job := createAndClaimEvaluationJob(t, s)
	s.runProviderPoolEvaluation(t.Context(), job)
	stored, cells, err := s.GetProviderPoolEvaluationJob(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.EvaluationMode != routerprofile.EvaluationModeAbsoluteV1 ||
		stored.Status != routerprofile.JobSucceeded || len(cells) != 1 ||
		cells[0].Status != routerprofile.CellSucceeded || cells[0].JudgeScore != 0.8 {
		t.Fatalf("job/cells = %+v / %+v", stored, cells)
	}
	if len(targets) != 2 || targets[0] != "provider:test:secret-candidate" || targets[1] != "cloud:judge" {
		t.Fatalf("direct targets = %v", targets)
	}
	if strings.Contains(prompts[1], "secret-candidate") || strings.Contains(prompts[1], "provider:test") {
		t.Fatalf("judge prompt leaked candidate identity: %q", prompts[1])
	}
	wantRoles := []string{"system", "user", "assistant", "user"}
	if len(payloads[0]) != len(wantRoles) {
		t.Fatalf("candidate payload messages = %#v", payloads[0])
	}
	for index, role := range wantRoles {
		if payloads[0][index]["role"] != role {
			t.Fatalf("candidate payload role %d = %#v, want %q", index, payloads[0][index]["role"], role)
		}
	}
	judgePrompt := payloads[1][0]["content"].(string)
	for _, context := range []string{"SYSTEM: production instruction", "ASSISTANT: prior answer", "USER: redacted user query", "candidate answer"} {
		if !strings.Contains(judgePrompt, context) {
			t.Fatalf("judge prompt missing %q: %q", context, judgePrompt)
		}
	}
	if cells[0].PromptTokens != 6 || cells[0].OutputTokens != 4 ||
		!cells[0].PriceKnown || cells[0].EstimatedCost <= 0 ||
		cells[0].JudgeModel != "judge" || cells[0].JudgePromptVersion != providerPoolJudgePromptVersion {
		t.Fatalf("structured cell = %+v", cells[0])
	}
	profile, err := s.routerProfiles.GetProfileBySourceJob(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Profile.SourceJobID != job.ID || profile.Profile.MatrixFingerprint == "" {
		t.Fatalf("generated candidate profile = %+v", profile)
	}
	if _, err := s.routerProfiles.ActiveProfile(t.Context(), job.PoolID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("candidate profile was unexpectedly activated: %v", err)
	}
	s.runProviderPoolEvaluation(t.Context(), job)
	if len(targets) != 2 {
		t.Fatalf("rerunning completed optimization repeated inference calls: %v", targets)
	}
}

func TestProviderPoolEvaluationLegacyFallbackAndBoundedJudgeContext(t *testing.T) {
	legacy := providerPoolEvaluationMessages(routerprofile.QuerySnapshot{RedactedQuery: "legacy final query"})
	if len(legacy) != 1 || legacy[0] != (routerprofile.Message{Role: "user", Content: "legacy final query"}) {
		t.Fatalf("legacy replay messages = %+v", legacy)
	}
	context := renderProviderPoolEvaluationMessages([]routerprofile.Message{
		{Role: "system", Content: strings.Repeat("system ", providerPoolJudgeContextBytes)},
		{Role: "assistant", Content: "assistant context"},
		{Role: "user", Content: "final query"},
	}, providerPoolJudgeContextBytes)
	if len(context) > providerPoolJudgeContextBytes {
		t.Fatalf("judge context exceeded bound: %d", len(context))
	}
	if !strings.HasPrefix(context, "SYSTEM: ") {
		t.Fatalf("judge context did not preserve role rendering: %q", context[:min(len(context), 80)])
	}
	if !strings.Contains(context, "ASSISTANT: assistant context") || !strings.HasSuffix(context, "USER: final query") {
		t.Fatalf("judge context lost recent conversation: %q", context[len(context)-min(len(context), 120):])
	}
}

func TestProviderPoolEvaluationCandidateFailureIsExplicitZeroObservation(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{
		{ID: "member", Source: "cloud", Model: "candidate"},
	})
	createEvaluationSnapshots(t, s, 1)
	s.evaluationCatalogLoader = evaluationCatalogFixture
	candidateCalls, judgeCalls := 0, 0
	s.evaluationEngineFactory = func(_ context.Context, model, _ string) (inference.Engine, error) {
		return &evaluationFakeEngine{model: model, call: func(context.Context, map[string]interface{}) (*http.Response, error) {
			if model == "judge" {
				judgeCalls++
				return evaluationResponse(`{"score":1,"reason":"unexpected"}`, 1, 1), nil
			}
			candidateCalls++
			return evaluationResponse("", 12, 64), nil
		}}, nil
	}
	job := createAndClaimEvaluationJob(t, s)
	s.runProviderPoolEvaluation(t.Context(), job)
	stored, cells, err := s.GetProviderPoolEvaluationJob(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != routerprofile.JobSucceeded || candidateCalls != 1 || judgeCalls != 0 || len(cells) != 1 {
		t.Fatalf("candidate failure job/calls = %+v %d/%d", stored, candidateCalls, judgeCalls)
	}
	cell := cells[0]
	if cell.Status != routerprofile.CellSucceeded || cell.Outcome != "candidate_error" ||
		cell.QualitySource != routerprofile.QualitySourceCandidateFailure || cell.CandidateAvailable ||
		cell.JudgeScore != 0 || cell.JudgeReason == "" || cell.CandidateError == "" {
		t.Fatalf("candidate failure cell = %+v", cell)
	}
	profile, err := s.routerProfiles.GetProfileBySourceJob(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Profile.Evaluation.ResponseOutcomes["candidate_error"] != 1 {
		t.Fatalf("outcome summary = %+v", profile.Profile.Evaluation.ResponseOutcomes)
	}
}

func TestProviderPoolEvaluationJudgeRetriesStrictJSONAndAccountsBudget(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{
		{ID: "member", Source: "cloud", Model: "candidate"},
	})
	createEvaluationSnapshots(t, s, 1)
	s.evaluationCatalogLoader = evaluationCatalogFixture
	request := ProviderPoolEvaluationRequest{
		PoolID: "evaluation-pool", EvaluationMode: routerprofile.EvaluationModeAbsoluteV1,
		JudgeModel: "judge", MaxQueries: 1, Repeats: 1,
		MaxOutputTokens: 64, RequestTimeoutSeconds: 5, BudgetCurrency: "USD",
	}
	preview, err := s.PreviewProviderPoolEvaluation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.BudgetAmount = preview.KnownEstimatedCost
	var candidateCalls, judgeCalls int
	s.evaluationEngineFactory = func(_ context.Context, model, _ string) (inference.Engine, error) {
		return &evaluationFakeEngine{model: model, call: func(_ context.Context, body map[string]interface{}) (*http.Response, error) {
			if model == "candidate" {
				candidateCalls++
				return evaluationResponse("candidate answer", 6, 4), nil
			}
			judgeCalls++
			if body["temperature"] != 0 {
				t.Fatalf("judge temperature = %#v", body["temperature"])
			}
			format, _ := body["response_format"].(map[string]interface{})
			thinking, _ := body["thinking"].(map[string]interface{})
			if format["type"] != "json_object" || thinking["type"] != "disabled" ||
				body["max_tokens"] != providerPoolJudgeMaxTokens {
				t.Fatalf("judge strict request = %#v", body)
			}
			switch judgeCalls {
			case 1:
				return evaluationResponse("", 20, 0), nil
			case 2:
				return evaluationResponse(`{"score":`, 20, 2), nil
			default:
				return evaluationResponse(`{"score":0.75,"reason":"good"}`, 20, 6), nil
			}
		}}, nil
	}
	created, err := s.CreateProviderPoolEvaluationJob(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.routerProfiles.ClaimNextEvaluationJobGlobal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != created.ID {
		t.Fatalf("claimed %q, want %q", job.ID, created.ID)
	}
	s.runProviderPoolEvaluation(t.Context(), job)
	stored, cells, err := s.GetProviderPoolEvaluationJob(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != routerprofile.JobSucceeded || candidateCalls != 1 || judgeCalls != 3 ||
		cells[0].JudgeAttemptCount != 3 || cells[0].JudgeScore != .75 ||
		cells[0].JudgeTotalTokens != 68 || cells[0].EstimatedCost > request.BudgetAmount+1e-12 {
		t.Fatalf("retry result = %+v calls=%d/%d budget=%f", cells[0], candidateCalls, judgeCalls, request.BudgetAmount)
	}
}

func TestProviderPoolEvaluationResumeMalformedJudgeAndMemberDrift(t *testing.T) {
	t.Run("resume candidate boundary and malformed judge", func(t *testing.T) {
		s := evaluationTestServer(t, []config.ProviderPoolMember{
			{ID: "member", Source: "cloud", Model: "candidate"},
		})
		createEvaluationSnapshots(t, s, 1)
		s.evaluationCatalogLoader = evaluationCatalogFixture
		job := createAndClaimEvaluationJob(t, s)
		_, cells, err := s.routerProfiles.GetEvaluationJob(t.Context(), job.PoolID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		cell := cells[0]
		cell.Status = routerprofile.CellCandidateSucceeded
		cell.CandidateResponse = "durable answer"
		cell.Outcome = "candidate_completed"
		if err := s.routerProfiles.UpdateEvaluationCell(t.Context(), cell); err != nil {
			t.Fatal(err)
		}
		candidateCalls, judgeCalls := 0, 0
		s.evaluationEngineFactory = func(_ context.Context, model, _ string) (inference.Engine, error) {
			return &evaluationFakeEngine{model: model, call: func(context.Context, map[string]interface{}) (*http.Response, error) {
				if model == "candidate" {
					candidateCalls++
					return evaluationResponse("repeated", 1, 1), nil
				}
				judgeCalls++
				return evaluationResponse("```json\n{}\n```", 2, 2), nil
			}}, nil
		}
		s.runProviderPoolEvaluation(t.Context(), job)
		_, cells, err = s.routerProfiles.GetEvaluationJob(t.Context(), job.PoolID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if candidateCalls != 0 || judgeCalls != providerPoolJudgeMaxAttempts ||
			cells[0].Status != routerprofile.CellCandidateSucceeded ||
			cells[0].Outcome != "judge_error" || cells[0].JudgeError == "" ||
			cells[0].JudgeAttemptCount != providerPoolJudgeMaxAttempts {
			t.Fatalf("resume calls/cell = %d/%d %+v", candidateCalls, judgeCalls, cells[0])
		}
		stored, _, err := s.routerProfiles.GetEvaluationJob(t.Context(), job.PoolID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != routerprofile.JobFailed || !strings.Contains(stored.Error, "candidate response remains resumable") {
			t.Fatalf("judge exhaustion did not fail job clearly: %+v", stored)
		}
	})

	t.Run("resume judge only with durable attempt cap", func(t *testing.T) {
		s := evaluationTestServer(t, []config.ProviderPoolMember{
			{ID: "member", Source: "cloud", Model: "candidate"},
		})
		createEvaluationSnapshots(t, s, 1)
		s.evaluationCatalogLoader = evaluationCatalogFixture
		job := createAndClaimEvaluationJob(t, s)
		_, cells, err := s.routerProfiles.GetEvaluationJob(t.Context(), job.PoolID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		cell := cells[0]
		cell.Status = routerprofile.CellCandidateSucceeded
		cell.CandidateResponse = "durable answer"
		cell.CandidateAvailable = true
		cell.Outcome = "candidate_completed"
		cell.JudgeAttemptCount = 2
		if err := s.routerProfiles.UpdateEvaluationCell(t.Context(), cell); err != nil {
			t.Fatal(err)
		}
		candidateCalls, judgeCalls := 0, 0
		s.evaluationEngineFactory = func(_ context.Context, model, _ string) (inference.Engine, error) {
			return &evaluationFakeEngine{model: model, call: func(context.Context, map[string]interface{}) (*http.Response, error) {
				if model == "candidate" {
					candidateCalls++
					return evaluationResponse("repeated", 1, 1), nil
				}
				judgeCalls++
				return evaluationResponse(`{"score":0.6,"reason":"resumed"}`, 3, 2), nil
			}}, nil
		}
		s.runProviderPoolEvaluation(t.Context(), job)
		stored, cells, err := s.routerProfiles.GetEvaluationJob(t.Context(), job.PoolID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != routerprofile.JobSucceeded || candidateCalls != 0 || judgeCalls != 1 ||
			cells[0].JudgeAttemptCount != 3 || cells[0].JudgeScore != .6 {
			t.Fatalf("resumed judge = %+v calls=%d/%d cell=%+v", stored, candidateCalls, judgeCalls, cells[0])
		}
	})

	t.Run("member drift", func(t *testing.T) {
		s := evaluationTestServer(t, []config.ProviderPoolMember{
			{ID: "member", Source: "cloud", Model: "candidate"},
		})
		createEvaluationSnapshots(t, s, 1)
		s.evaluationCatalogLoader = evaluationCatalogFixture
		job := createAndClaimEvaluationJob(t, s)
		if err := config.SaveProviderPools([]config.ProviderPool{{
			ID: "evaluation-pool", Name: "Evaluation", Model: "public-pool", Enabled: true,
			Members: []config.ProviderPoolMember{{ID: "changed", Source: "cloud", Model: "other"}},
		}}); err != nil {
			t.Fatal(err)
		}
		s.runProviderPoolEvaluation(t.Context(), job)
		stored, _, err := s.routerProfiles.GetEvaluationJob(t.Context(), job.PoolID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != routerprofile.JobFailed || !strings.Contains(stored.Error, "fingerprint") {
			t.Fatalf("drifted job = %+v", stored)
		}
	})
}

func TestProviderPoolEvaluationCancellationAndBudget(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{
		{ID: "member", Source: "cloud", Model: "candidate"},
	})
	createEvaluationSnapshots(t, s, 1)
	s.evaluationCatalogLoader = evaluationCatalogFixture
	if _, err := s.CreateProviderPoolEvaluationJob(t.Context(), ProviderPoolEvaluationRequest{
		PoolID: "evaluation-pool", JudgeModel: "judge", MaxQueries: 1, Repeats: 1,
		MaxOutputTokens: 64, BudgetCurrency: "USD", BudgetAmount: 0,
	}); err == nil {
		t.Fatal("known over-budget job was accepted")
	}
	job := createAndClaimEvaluationJob(t, s)
	started := make(chan struct{})
	s.evaluationEngineFactory = func(_ context.Context, model, _ string) (inference.Engine, error) {
		return &evaluationFakeEngine{model: model, call: func(ctx context.Context, _ map[string]interface{}) (*http.Response, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}}, nil
	}
	runCtx, cancel := context.WithCancel(t.Context())
	s.setRouterEvaluationCurrent(job.PoolID, job.ID, cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runProviderPoolEvaluation(runCtx, job)
	}()
	<-started
	if err := s.CancelProviderPoolEvaluationJob(t.Context(), job.PoolID, job.ID); err != nil {
		t.Fatal(err)
	}
	<-done
	stored, _, err := s.routerProfiles.GetEvaluationJob(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != routerprofile.JobCancelled || !stored.CancellationRequested {
		t.Fatalf("cancelled job = %+v", stored)
	}
}

func TestProviderPoolListwisePreviewStrictParseAndFourClasses(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{
		{ID: "member-a", Source: "cloud", Model: "candidate"},
		{ID: "member-b", Source: "cloud", Model: "candidate"},
		{ID: "member-c", Source: "cloud", Model: "candidate"},
	})
	createEvaluationSnapshots(t, s, 2)
	s.evaluationCatalogLoader = evaluationCatalogFixture
	preview, err := s.PreviewProviderPoolEvaluation(t.Context(), ProviderPoolEvaluationRequest{
		PoolID: "evaluation-pool", JudgeModel: "judge", MaxQueries: 2, Repeats: 2,
		MaxOutputTokens: 64, RequestTimeoutSeconds: 5, BudgetCurrency: "USD", BudgetAmount: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.EvaluationMode != routerprofile.EvaluationModeListwiseV2 ||
		preview.DirectCandidateCalls != 12 || preview.JudgeCalls != 4 ||
		preview.MaxJudgeCalls != 12 || preview.MaxTotalCalls != 24 {
		t.Fatalf("listwise preview = %+v", preview)
	}
	if preview.JudgePromptTokens <= 0 || preview.MaxJudgeTokenExposure <= preview.JudgePromptTokens ||
		preview.KnownJudgeEstimatedCost <= 0 ||
		preview.KnownEstimatedCost < preview.KnownJudgeEstimatedCost {
		t.Fatalf("listwise combined judge estimate = %+v", preview)
	}

	references := []routerprofile.RoundCandidateReference{
		{CellID: "cell-a", CandidateMemberID: "a", Alias: "A"},
		{CellID: "cell-b", CandidateMemberID: "b", Alias: "B"},
	}
	valid := `{"candidates":[{"alias":"A","rank":1,"score":0.9,"acceptable":true,"reason":"good"},{"alias":"B","rank":2,"score":0.1,"acceptable":false,"reason":"bad"}]}`
	if _, err := parseProviderPoolListwiseJudge(valid, references); err != nil {
		t.Fatalf("valid strict result: %v", err)
	}
	for _, invalid := range []string{
		`{"candidates":[{"alias":"A","rank":1,"score":0.9,"acceptable":true,"reason":"good"}]}`,
		`{"candidates":[{"alias":"A","rank":1,"score":0.9,"acceptable":true,"reason":"good"},{"alias":"A","rank":2,"score":0.1,"acceptable":false,"reason":"bad"}]}`,
		`{"candidates":[{"alias":"A","rank":1,"score":0.9,"acceptable":true,"reason":"good"},{"alias":"C","rank":2,"score":0.1,"acceptable":false,"reason":"bad"}]}`,
		`{"candidates":[{"alias":"A","rank":1,"score":0.9,"acceptable":true,"reason":"good","extra":1},{"alias":"B","rank":2,"score":0.1,"acceptable":false,"reason":"bad"}]}`,
	} {
		if _, err := parseProviderPoolListwiseJudge(invalid, references); err == nil {
			t.Fatalf("invalid listwise result accepted: %s", invalid)
		}
	}

	round := routerprofile.EvaluationRound{
		PoolID: "pool", JobID: "job", ID: "round", QuerySnapshotID: "query",
		CandidateReferences: []routerprofile.RoundCandidateReference{
			{CellID: "cell-a", CandidateMemberID: "a", Alias: "A"},
			{CellID: "cell-b", CandidateMemberID: "b", Alias: "B"},
			{CellID: "cell-c", CandidateMemberID: "c", Alias: "C"},
			{CellID: "cell-d", CandidateMemberID: "d", Alias: "D"},
		},
		Result: routerprofile.ListwiseResult{Candidates: []routerprofile.ListwiseCandidateResult{
			{Alias: "A", Rank: 1, Score: .9, Acceptable: true, Reason: "a"},
			{Alias: "B", Rank: 3, Score: .2, Acceptable: false, Reason: "b"},
			{Alias: "C", Rank: 4, Score: .1, Acceptable: false, Reason: "c"},
			{Alias: "D", Rank: 2, Score: .8, Acceptable: true, Reason: "d"},
		}},
	}
	preferences, err := expandProviderPoolListwisePreferences(round)
	if err != nil {
		t.Fatal(err)
	}
	classes := make(map[string]bool)
	for _, preference := range preferences {
		classes[preference.OutcomeClassID] = true
		if preference.MemberAID >= preference.MemberBID {
			t.Fatalf("preference orientation is not canonical: %+v", preference)
		}
	}
	for _, classID := range []string{
		routerprofile.PreferenceClassAAcceptableBNot,
		routerprofile.PreferenceClassNeitherAcceptable,
		routerprofile.PreferenceClassBothAcceptable,
		routerprofile.PreferenceClassANotBAcceptable,
	} {
		if !classes[classID] {
			t.Fatalf("missing preference class %q in %+v", classID, preferences)
		}
	}
}

func TestProviderPoolListwiseEvaluationAnonymousUnavailableAndDurableRetry(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{
		{ID: "member-a", Source: "cloud", Model: "secret-a"},
		{ID: "member-b", Source: "cloud", Model: "secret-b"},
		{ID: "member-c", Source: "cloud", Model: "secret-c"},
	})
	createEvaluationSnapshots(t, s, 1)
	s.evaluationCatalogLoader = func(context.Context) ([]api.ModelInfo, error) {
		return []api.ModelInfo{
			evaluationModelInfo("cloud", "secret-a", 1, 1),
			evaluationModelInfo("cloud", "secret-b", 1, 1),
			evaluationModelInfo("cloud", "secret-c", 1, 1),
			evaluationModelInfo("cloud", "judge", 1, 1),
		}, nil
	}
	created, err := s.CreateProviderPoolEvaluationJob(t.Context(), ProviderPoolEvaluationRequest{
		PoolID: "evaluation-pool", JudgeModel: "judge", MaxQueries: 1, Repeats: 1,
		MaxOutputTokens: 64, RequestTimeoutSeconds: 5, BudgetCurrency: "USD", BudgetAmount: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.routerProfiles.ClaimNextEvaluationJobGlobal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rounds, err := s.routerProfiles.ListEvaluationRounds(t.Context(), job.PoolID, job.ID)
	if err != nil || len(rounds) != 1 {
		t.Fatalf("rounds = %+v err=%v", rounds, err)
	}
	cells, err := s.routerProfiles.ListEvaluationCells(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	memberByCell := make(map[string]string)
	for _, cell := range cells {
		memberByCell[cell.ID] = cell.CandidateMemberID
	}
	candidateCalls, judgeCalls := 0, 0
	var judgePrompt string
	s.evaluationEngineFactory = func(_ context.Context, model, _ string) (inference.Engine, error) {
		return &evaluationFakeEngine{model: model, call: func(_ context.Context, body map[string]interface{}) (*http.Response, error) {
			if model != "judge" {
				candidateCalls++
				if model == "secret-b" {
					return evaluationResponse("", 4, 0), nil
				}
				return evaluationResponse("anonymous answer", 4, 3), nil
			}
			judgeCalls++
			judgePrompt = body["messages"].([]map[string]interface{})[0]["content"].(string)
			if judgeCalls == 1 {
				return evaluationResponse(`{"candidates":[]}`, 20, 2), nil
			}
			results := make([]map[string]interface{}, 0, len(rounds[0].CandidateReferences))
			rank := 1
			var unavailable routerprofile.RoundCandidateReference
			for _, reference := range rounds[0].CandidateReferences {
				if memberByCell[reference.CellID] == "member-b" {
					unavailable = reference
					continue
				}
				results = append(results, map[string]interface{}{
					"alias": reference.Alias, "rank": rank, "score": .8 - float64(rank)/10,
					"acceptable": rank == 1, "reason": "available",
				})
				rank++
			}
			results = append(results, map[string]interface{}{
				"alias": unavailable.Alias, "rank": len(rounds[0].CandidateReferences),
				"score": 0, "acceptable": false, "reason": "unavailable",
			})
			payload, _ := json.Marshal(map[string]interface{}{"candidates": results})
			return evaluationResponse(string(payload), 20, 10), nil
		}}, nil
	}
	s.runProviderPoolEvaluation(t.Context(), job)
	stored, finalCells, err := s.GetProviderPoolEvaluationJob(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != created.ID || stored.EvaluationMode != routerprofile.EvaluationModeListwiseV2 ||
		stored.Status != routerprofile.JobFailed || candidateCalls != 3 || judgeCalls != 2 ||
		!strings.Contains(stored.Error, "minimum is") {
		t.Fatalf("listwise job=%+v calls=%d/%d", stored, candidateCalls, judgeCalls)
	}
	if strings.Contains(judgePrompt, "secret-a") || strings.Contains(judgePrompt, "secret-b") ||
		strings.Contains(judgePrompt, "secret-c") || !strings.Contains(judgePrompt, "[UNAVAILABLE]") {
		t.Fatalf("listwise judge prompt leaked identity or omitted unavailable marker: %q", judgePrompt)
	}
	unavailableCount := 0
	for _, cell := range finalCells {
		if !cell.CandidateAvailable {
			unavailableCount++
			if cell.CandidateResponse != "" {
				t.Fatalf("unavailable answer was persisted as content: %+v", cell)
			}
		}
	}
	preferences, err := s.routerProfiles.ListEvaluationPreferences(t.Context(), job.PoolID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unavailableCount != 1 || len(preferences) != 3 {
		t.Fatalf("unavailable/preferences = %d/%+v", unavailableCount, preferences)
	}
	if _, err := s.routerProfiles.GetProfileBySourceJob(t.Context(), job.PoolID, job.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("infeasible listwise job fabricated a profile: %v", err)
	}
}

func evaluationTestServer(t *testing.T, members []config.ProviderPoolMember) *Server {
	t.Helper()
	s := newTestServer(t)
	s.cfg.OpenCSGAPIKey = "test-key"
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "evaluation-pool", Name: "Evaluation", Model: "public-pool", Enabled: true, Members: members,
	}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func createEvaluationSnapshots(t *testing.T, s *Server, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		if _, err := s.routerProfiles.CreateQuerySnapshot(t.Context(), routerprofile.QuerySnapshot{
			PoolID: "evaluation-pool", ID: "snapshot-" + string(rune('a'+i)),
			QueryHash: "hash-" + string(rune('a'+i)), RedactedQuery: "redacted user query",
			RedactedMessages: []routerprofile.Message{
				{Role: "system", Content: "production instruction"},
				{Role: "user", Content: "earlier user question"},
				{Role: "assistant", Content: "prior answer"},
				{Role: "user", Content: "redacted user query"},
			},
			RoutingText: "redacted user query", Embedding: []float64{1, float64(i + 1)},
			EmbeddingModel: semanticEmbeddingModel, Split: "train",
			SplitGroup: "group-" + string(rune('a'+i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func createAndClaimEvaluationJob(t *testing.T, s *Server) routerprofile.EvaluationJob {
	t.Helper()
	created, err := s.CreateProviderPoolEvaluationJob(t.Context(), ProviderPoolEvaluationRequest{
		PoolID: "evaluation-pool", EvaluationMode: routerprofile.EvaluationModeAbsoluteV1,
		JudgeModel: "judge", MaxQueries: 1, Repeats: 1,
		MaxOutputTokens: 64, RequestTimeoutSeconds: 5, BudgetCurrency: "USD", BudgetAmount: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.routerProfiles.ClaimNextEvaluationJobGlobal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != created.ID {
		t.Fatalf("claimed job %q, want %q", claimed.ID, created.ID)
	}
	return claimed
}

func evaluationCatalogFixture(context.Context) ([]api.ModelInfo, error) {
	return []api.ModelInfo{
		evaluationModelInfo("cloud", "candidate", 1, 1),
		evaluationModelInfo("cloud", "judge", 1, 1),
	}, nil
}

func evaluationModelInfo(source, model string, input, output float64) api.ModelInfo {
	return api.ModelInfo{
		Source: source, Model: model, PipelineTag: "text-generation",
		Pricing: &api.ModelPricing{
			InputTokenPrice:  &api.ModelTokenPrice{Currency: "USD", PricePerMillion: input},
			OutputTokenPrice: &api.ModelTokenPrice{Currency: "USD", PricePerMillion: output},
		},
	}
}

func evaluationResponse(content string, input, output int) *http.Response {
	body, _ := json.Marshal(map[string]interface{}{
		"choices": []map[string]interface{}{{
			"message": map[string]interface{}{"content": content}, "finish_reason": "stop",
		}},
		"usage": map[string]interface{}{
			"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output,
		},
	})
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(bytes.NewReader(body)),
	}
}
