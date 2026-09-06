package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	routerprofile "github.com/opencsgs/semantic-router"
)

func TestActiveRouterProfileRoutesMultiTurnAndFallsBackOnOODAndDrift(t *testing.T) {
	vector := []float64{0, 0}
	var embeddedInput string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "profile-embedding" {
			t.Fatalf("embedding model = %q", request.Model)
		}
		embeddedInput = request.Input
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{map[string]any{"embedding": vector}},
		})
	}))
	defer gateway.Close()

	s := newTestServerWithConfig(t, &config.Config{
		ServerURL: gateway.URL, AIGatewayURL: gateway.URL, OpenCSGAPIKey: "test-key",
	})
	s.cloud = cloud.NewService(gateway.URL)
	pool := config.ProviderPool{
		ID: "pool-active", Policy: config.ProviderPoolPolicySemantic,
		Members: []config.ProviderPoolMember{
			{ID: "small", Source: "cloud", Model: "small", Weight: 100},
			{ID: "large", Source: "cloud", Model: "large", Weight: 100},
		},
	}
	profile := testActiveRouterProfile(pool)
	s.routerProfileMu.Lock()
	s.routerProfileCache[pool.ID] = &profile
	s.routerProfileMu.Unlock()

route := s.providerPoolSemanticRouter(pool)(t.Context(), providerPoolSemanticInput{
			messages: []routerprofile.Message{
				{Role: "system", Content: "be precise"},
				{Role: "user", Content: "first question"},
				{Role: "assistant", Content: "ignored answer"},
				{Role: "user", Content: "follow up"},
			},
		})
		if embeddedInput != "System: be precise\nUser: first question\nUser: follow up" {
			t.Fatalf("routing input = %q", embeddedInput)
		}
		if !route.Applied || route.MemberID != "small" || route.ClusterID != "cluster-small" ||
			route.ProfileID != profile.ID || route.ProfileVersion != profile.Version {
			t.Fatalf("dynamic route = %+v", route)
		}

		vector = []float64{5, 5}
		route = s.providerPoolSemanticRouter(pool)(t.Context(), semanticInputFromPrompt("far away"))
		if !route.OOD || !route.Fallback || route.FallbackReason != routerprofile.FallbackOutOfDistribution ||
			route.MemberID != "large" || !route.Applied {
			t.Fatalf("OOD route = %+v", route)
		}

		drifted := pool
		drifted.Members = drifted.Members[:1]
		route = s.providerPoolSemanticRouter(drifted)(t.Context(), semanticInputFromPrompt("drift"))
		if !route.Fallback || route.FallbackReason != semanticFallbackMemberDrift {
		t.Fatalf("member drift route = %+v", route)
	}
}

func TestNearestRouterProfileClusterHonorsConfiguredMetric(t *testing.T) {
	profile := testActiveRouterProfile(config.ProviderPool{
		ID: "pool",
		Members: []config.ProviderPoolMember{
			{ID: "small", Source: "cloud", Model: "small", Weight: 100},
			{ID: "large", Source: "cloud", Model: "large", Weight: 100},
		},
	})
	index, distance, err := profile.Profile.NearestCluster([]float64{0.9, 0.9})
	if err != nil || index != 1 || distance < 0.019 || distance > 0.021 {
		t.Fatalf("squared route index=%d distance=%v err=%v", index, distance, err)
	}
	profile.Profile.Distance = routerprofile.DistanceCosine
	index, distance, err = profile.Profile.NearestCluster([]float64{1, 1})
	if err != nil || index != 1 || distance > 1e-12 {
		t.Fatalf("cosine route index=%d distance=%v err=%v", index, distance, err)
	}
}

func TestServerRouterProfileActivationSwapAndRollbackRefreshCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)
	pool := config.ProviderPool{
		ID: "pool-lifecycle", Name: "Lifecycle", Model: "routed",
		Enabled: true, Policy: config.ProviderPoolPolicySemantic,
		Members: []config.ProviderPoolMember{
			{ID: "small", Source: "cloud", Model: "small", Weight: 100},
			{ID: "large", Source: "cloud", Model: "large", Weight: 100},
		},
	}
	if err := config.SaveProviderPools([]config.ProviderPool{pool}); err != nil {
		t.Fatal(err)
	}
	store, err := routerprofile.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := testActiveRouterProfile(pool)
	first.ID, first.Version = "profile-1", 1
	first.Profile.SourceJobID = "job-1"
	first.Profile.MatrixFingerprint = "matrix-1"
	second := testActiveRouterProfile(pool)
	second.ID, second.Version = "profile-2", 2
	second.Profile.SourceJobID = "job-2"
	second.Profile.MatrixFingerprint = "matrix-2"
	for _, profile := range []routerprofile.Profile{first, second} {
		if _, err := store.CreateProfile(t.Context(), profile); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{routerProfiles: store, routerProfileCache: make(map[string]*routerprofile.Profile)}
	if _, err := s.ActivateRouterProfile(t.Context(), pool.ID, first.ID, "test", "initial"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateRouterProfile(t.Context(), pool.ID, second.ID, "test", "swap"); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.activeRouterProfile(pool.ID); active == nil || active.ID != second.ID {
		t.Fatalf("active after swap = %+v", active)
	}
	if _, err := s.RollbackRouterProfile(t.Context(), pool.ID, second.ID, "test", "rollback"); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.activeRouterProfile(pool.ID); active == nil || active.ID != first.ID {
		t.Fatalf("active after rollback = %+v", active)
	}
}

func TestValidateRouterProfileAllowsOptimizerCandidateSubset(t *testing.T) {
	pool := config.ProviderPool{
		ID: "pool-subset",
		Members: []config.ProviderPoolMember{
			{ID: "small", Source: "cloud", Model: "small", Weight: 100},
			{ID: "large", Source: "cloud", Model: "large", Weight: 100},
			{ID: "unused", Source: "cloud", Model: "unused", Weight: 100},
		},
	}
	profile := testActiveRouterProfile(pool)
	if err := validateRouterProfileForPool(pool, profile, false); err != nil {
		t.Fatalf("optimizer candidate subset should remain compatible: %v", err)
	}
}

func TestActiveRouterProfileV2RoutesAndFallsBackSafely(t *testing.T) {
	vector := []float64{1, 0}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{map[string]any{"embedding": vector}},
		})
	}))
	defer gateway.Close()
	s := newTestServerWithConfig(t, &config.Config{
		ServerURL: gateway.URL, AIGatewayURL: gateway.URL, OpenCSGAPIKey: "test-key",
	})
	s.cloud = cloud.NewService(gateway.URL)
	pool := config.ProviderPool{
		ID: "pool-v2", Policy: config.ProviderPoolPolicySemantic,
		Members: []config.ProviderPoolMember{
			{ID: "a", Source: "cloud", Model: "a", Weight: 100},
			{ID: "b", Source: "cloud", Model: "b", Weight: 100},
			{ID: "unrelated", Source: "cloud", Model: "other", Weight: 100},
		},
	}
	profilePool := pool
	profilePool.Members = append([]config.ProviderPoolMember(nil), pool.Members[:2]...)
	profile := testActiveRouterProfileV2(t, profilePool)
	s.routerProfileCache[pool.ID] = &profile
	route := s.providerPoolSemanticRouter(pool)(t.Context(), semanticInputFromPrompt("route"))
	if !route.Applied || route.MemberID != "a" || route.ProfileSchemaVersion != 2 ||
		route.RouterAlgorithm != routerprofile.RouterAlgorithmPairwiseV2 {
		t.Fatalf("V2 route = %+v", route)
	}
	changed := pool
	changed.Members = append([]config.ProviderPoolMember(nil), pool.Members...)
	changed.Members[0].Model = "changed-a"
	route = s.providerPoolSemanticRouter(changed)(t.Context(), semanticInputFromPrompt("changed"))
	if !route.Fallback || route.FallbackReason != semanticFallbackMemberDrift {
		t.Fatalf("changed selected candidate route = %+v", route)
	}
	profile.ProfileV2.Calibration.Policy.Thresholds.MinimumConfidence = 1
	if err := profile.ProfileV2.Seal(); err != nil {
		t.Fatal(err)
	}
	s.routerProfileCache[pool.ID] = &profile
route = s.providerPoolSemanticRouter(pool)(t.Context(), semanticInputFromPrompt("fallback"))
		if !route.Applied || !route.Fallback || route.MemberID != "b" ||
			route.FallbackReason != routerprofile.FallbackLowConfidence {
		t.Fatalf("V2 safe fallback = %+v", route)
	}
}

func TestValidateRouterProfileV2ActivationSafety(t *testing.T) {
	pool := config.ProviderPool{
		ID: "pool-v2", Members: []config.ProviderPoolMember{
			{ID: "a", Source: "cloud", Model: "a"}, {ID: "b", Source: "cloud", Model: "b"},
		},
	}
	profile := testActiveRouterProfileV2(t, pool)
	profile.ProfileV2.Calibration.Feasible = false
	profile.ProfileV2.Calibration.QualityFeasible = false
	profile.ProfileV2.Validation.Feasible = false
	profile.ProfileV2.Validation.ActivationAllowed = false
	profile.ProfileV2.Validation.State = "infeasible"
	if err := profile.ProfileV2.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := validateRouterProfileForPool(pool, profile, true); err == nil {
		t.Fatal("infeasible V2 profile was activatable")
	}

	profile = testActiveRouterProfileV2(t, pool)
	profile.ProfileV2.Calibration.MemberDistribution = []routerprofile.CalibrationMemberDistribution{
		{MemberID: "a", Count: 20, Fraction: 1},
	}
	profile.ProfileV2.Calibration.CollapsedSingleMember = true
	profile.ProfileV2.Calibration.CollapsedMemberID = "a"
	profile.ProfileV2.Validation.CollapsedSingleMember = true
	profile.ProfileV2.Validation.CollapsedQualityPassed = true
	if err := profile.ProfileV2.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := validateRouterProfileForPool(pool, profile, true); err != nil {
		t.Fatalf("conservatively safe collapsed V2 profile was rejected: %v", err)
	}

	added := pool
	added.Members = append(append([]config.ProviderPoolMember(nil), pool.Members...),
		config.ProviderPoolMember{ID: "unrelated", Source: "cloud", Model: "other"})
	if err := validateRouterProfileForPool(added, profile, true); err != nil {
		t.Fatalf("unrelated member addition blocked V2 activation: %v", err)
	}
	changed := pool
	changed.Members = append([]config.ProviderPoolMember(nil), pool.Members...)
	changed.Members[0].Model = "changed-a"
	if err := validateRouterProfileForPool(changed, profile, true); err == nil {
		t.Fatal("changed selected candidate was activatable")
	}
	removed := pool
	removed.Members = append([]config.ProviderPoolMember(nil), pool.Members[1:]...)
	if err := validateRouterProfileForPool(removed, profile, true); err == nil {
		t.Fatal("removed selected candidate was activatable")
	}
	missingFallback := pool
	missingFallback.Members = append([]config.ProviderPoolMember(nil), pool.Members[:1]...)
	if err := validateRouterProfileForPool(missingFallback, profile, true); err == nil {
		t.Fatal("profile with missing safe fallback was activatable")
	}
}

func TestProfileFallbackV1UsesFallbackMemberID(t *testing.T) {
	pool := config.ProviderPool{
		ID: "pool-v1-fallback",
		Members: []config.ProviderPoolMember{
			{ID: "small", Source: "cloud", Model: "small"},
			{ID: "large", Source: "cloud", Model: "large"},
		},
	}
	profile := testActiveRouterProfile(pool)
	route := profileFallback(pool, profile, routerprofile.FallbackEmbedding)
	if !route.Fallback || !route.Applied || route.MemberID != "large" {
		t.Fatalf("V1 fallback = %+v", route)
	}

	missing := pool
	missing.Members = missing.Members[:1]
	route = profileFallback(missing, profile, routerprofile.FallbackEmbedding)
	if !route.Fallback || route.Applied || route.MemberID != "" {
		t.Fatalf("V1 fallback missing member = %+v", route)
	}
}

func testActiveRouterProfile(pool config.ProviderPool) routerprofile.Profile {
	artifact := routerprofile.RouterProfileV1{
		SchemaVersion:     routerprofile.SchemaVersionV1,
		MatrixFingerprint: "matrix",
		RoutingText:       routerprofile.RoutingTextConfig{Version: "routing-v1", MaxTokens: 256},
		Embedding:         routerprofile.EmbeddingConfig{Model: "profile-embedding", Revision: "revision", Dimensions: 2},
		Distance:          routerprofile.DistanceSquaredEuclidean,
		Candidates: []routerprofile.CandidateBindingV1{
			{MemberID: "small", Source: "cloud", Model: "small", Weight: 1, Cost: 0.1},
			{MemberID: "large", Source: "cloud", Model: "large", Weight: 1, Cost: 0.2},
		},
		Clusters: []routerprofile.ClusterV1{
			{
				ID: "cluster-small", Center: []float64{0, 0}, TargetMemberID: "small",
				Target: routerprofile.Target{Source: "cloud", Model: "small"}, SampleCount: 10,
				DistanceQuantile: routerprofile.DistanceQuantiles{}, OODThreshold: 0.1,
			},
			{
				ID: "cluster-large", Center: []float64{1, 1}, TargetMemberID: "large",
				Target: routerprofile.Target{Source: "cloud", Model: "large"}, SampleCount: 10,
				DistanceQuantile: routerprofile.DistanceQuantiles{}, OODThreshold: 0.1,
			},
		},
		Weights: routerprofile.RoutingWeights{Quality: 0.8, Cost: 0.2}, CostUnit: "USD_per_million_tokens",
		Judge:          routerprofile.JudgeProvenance{Model: "judge", PromptVersion: "v1"},
		SourceRevision: "observations", SourceJobID: "job", GeneratedAt: time.Now().UTC(),
		Evaluation: routerprofile.EvaluationSummary{
			QueryCount: 10, CellCount: 20, TrialCount: 20, Repeats: 1,
			ResponseOutcomes: map[string]int{"judged": 20}, WinRate: 0.5,
			Spend: 1, TotalCost: 1, Currency: "USD", CostUnit: "monetary_cost:USD",
			MonetarySpendKnown: true, TrainQueryCount: 8, HeldOutQueryCount: 2, CVFoldCount: 2,
			TrainUtility: 0.5, TrainQuality: 0.5, TrainCost: 0.5,
			HeldOutUtility: 0.5, HeldOutQuality: 0.5, HeldOutCost: 0.5,
			SemanticDifferentiation: true,
		},
		FallbackMemberID: "large",
		Metadata: routerprofile.ProfileMetadata{
			OptimizerVersion: "test", Seed: 1, MaxCandidateMembers: routerprofile.MaxOptimizerCandidates,
			SemanticDifferentiation: true,
		},
	}
	return routerprofile.Profile{
		PoolID: pool.ID, ID: "profile-active", Version: 2,
		MemberFingerprint: providerPoolMemberFingerprint(pool), Profile: artifact,
	}
}

func testActiveRouterProfileV2(t *testing.T, pool config.ProviderPool) routerprofile.Profile {
	t.Helper()
	samples := make([]routerprofile.PairwiseSample, 20)
	for i := range samples {
		samples[i] = routerprofile.PairwiseSample{
			RoundID: "round-" + string(rune('a'+i)), QueryID: "query-" + string(rune('a'+i)),
			Embedding: []float64{1, 0}, MemberAID: "a", MemberBID: "b",
			OutcomeClassID: routerprofile.PreferenceClassAAcceptableBNot, Confidence: 1,
		}
	}
	artifact := routerprofile.RouterProfileV2{
		SchemaVersion: routerprofile.SchemaVersionV2, ProfileAlgorithm: routerprofile.ProfileAlgorithmV2,
		RouterAlgorithm: routerprofile.RouterAlgorithmPairwiseV2, PoolID: pool.ID,
		MemberFingerprint: providerPoolMemberFingerprint(pool), EvidenceFingerprint: "evidence",
		RoutingText: routerprofile.RoutingTextConfig{Version: "routing-text-v1", MaxTokens: 256},
		Embedding:   routerprofile.EmbeddingConfig{Model: "profile-embedding", Revision: "revision", Dimensions: 2},
		Distance:    routerprofile.DistanceCosine,
		Candidates: []routerprofile.CandidateBindingV2{
			{MemberID: "a", Source: "cloud", Model: "a", Weight: 1},
			{MemberID: "b", Source: "cloud", Model: "b", Weight: 1},
		},
		Learner: routerprofile.PairwiseLearnerArtifact{
			ModelType: routerprofile.PairwiseModelBT, Seed: 1, Members: []string{"a", "b"},
			EmbeddingDims: 2, BT: &routerprofile.PairwiseBTArtifact{
				Bandwidth: .2, PriorStrength: 2, Samples: samples,
			},
			Diagnostics: routerprofile.PairwiseDiagnostics{
				RoundCount: 20, ObservationCount: 20,
				ClassSupport:      map[string]int{routerprofile.PreferenceClassAAcceptableBNot: 20},
				MemberPairSupport: map[string]int{"a\x00b": 20},
			},
		},
		Calibration: routerprofile.PairwiseCalibrationArtifact{
			TargetQualityRetention: routerprofile.DefaultTargetQualityRetention,
			QualityMetric:          routerprofile.CalibrationQualityMetric,
			CostBasis:              routerprofile.CalibrationCostBasis,
			UncertaintyMethod:      routerprofile.CalibrationUncertaintyMethod,
			SearchObjective:        routerprofile.CalibrationSearchObjective, ConfidenceLevel: .95,
			Policy: routerprofile.PairwiseRoutingPolicy{
				SafeFallbackMemberID: "b",
				Thresholds:           routerprofile.PairwiseRoutingThresholds{MinimumSimilarity: -1},
			},
			Baseline:       routerprofile.CalibrationQualityCost{Quality: .8},
			Routed:         routerprofile.CalibrationQualityCost{Quality: .8},
			PointRetention: 1, ConservativeRetention: 1, RetentionLowerBound: 0,
			MemberDistribution: []routerprofile.CalibrationMemberDistribution{
				{MemberID: "a", Count: 10, Fraction: .5},
				{MemberID: "b", Count: 10, Fraction: .5},
			},
			Coverage: 1, PairwiseMetrics: routerprofile.PairwiseMetrics{},
			QueryGroupCount: 24, RoundCount: 20, MinimumQueryGroups: 24,
			Feasible: true, QualityFeasible: true,
		},
		Validation: routerprofile.RouterProfileV2Validation{
			Feasible: true, ActivationAllowed: true, State: "valid",
		},
		Judge:       routerprofile.JudgeProvenance{Model: "judge", PromptVersion: "v2"},
		SourceJobID: "job-v2", GeneratedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := artifact.Seal(); err != nil {
		t.Fatal(err)
	}
	return routerprofile.Profile{
		PoolID: pool.ID, ID: "profile-v2", Version: 3, SchemaVersion: routerprofile.SchemaVersionV2,
		MemberFingerprint: artifact.MemberFingerprint, ProfileV2: &artifact,
	}
}
