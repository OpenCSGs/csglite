package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	routerprofile "github.com/opencsgs/semantic-router"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestProviderPoolRouterStatusReturnsLiveCountsWithoutSuggestion(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{{ID: "member", Source: "cloud", Model: "candidate"}})
	createEvaluationSnapshots(t, s, 2)

	req := httptest.NewRequest(http.MethodGet, "/api/provider-pools/evaluation-pool/router/status", nil)
	req.SetPathValue("id", "evaluation-pool")
	resp := httptest.NewRecorder()
	s.handleProviderPoolRouterStatus(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var status api.ProviderPoolRouterStatus
	if err := json.Unmarshal(resp.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.QualifiedQueryCount != 2 || status.NewQueryCount != 2 || status.PendingSuggestion != nil {
		t.Fatalf("router status counts = %+v", status)
	}
}

func TestProviderPoolRouterEvaluationHandlersAreScopedAndLightweight(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{{ID: "member", Source: "cloud", Model: "candidate"}})
	createEvaluationSnapshots(t, s, providerPoolRouterMinimumHistoryRecords)
	s.evaluationCatalogLoader = evaluationCatalogFixture

	body := `{"judge_model":"judge","max_queries":1,"repeats":1,"max_output_tokens":10,"request_timeout_seconds":5,"budget_currency":"USD","budget_amount":10}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/provider-pools/evaluation-pool/router/evaluations", strings.NewReader(body))
	createReq.SetPathValue("id", "evaluation-pool")
	createResp := httptest.NewRecorder()
	s.handleProviderPoolRouterEvaluationCreate(createResp, createReq)
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	jobID, _ := created["id"].(string)
	if jobID == "" || created["evaluation_mode"] != routerprofile.EvaluationModeListwiseV2 {
		t.Fatalf("missing job ID: %v", created)
	}
	if created["direct_candidate_calls"] != float64(1) || created["judge_calls"] != float64(1) ||
		created["max_judge_calls"] != float64(providerPoolJudgeMaxAttempts) ||
		created["judge_prompt_tokens"] == nil || created["max_judge_token_exposure"] == nil ||
		created["estimate_currency"] != "USD" {
		t.Fatalf("missing persisted job preview summary: %v", created)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/provider-pools/evaluation-pool/router/evaluations/"+jobID, nil)
	getReq.SetPathValue("id", "evaluation-pool")
	getReq.SetPathValue("jobID", jobID)
	getResp := httptest.NewRecorder()
	s.handleProviderPoolRouterEvaluationGet(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	if bytes.Contains(getResp.Body.Bytes(), []byte("candidate_response")) ||
		bytes.Contains(getResp.Body.Bytes(), []byte(`"cells"`)) {
		t.Fatalf("lightweight job leaked evaluation cells: %s", getResp.Body.String())
	}

	crossReq := httptest.NewRequest(http.MethodGet, "/api/provider-pools/missing/router/evaluations/"+jobID, nil)
	crossReq.SetPathValue("id", "missing")
	crossReq.SetPathValue("jobID", jobID)
	crossResp := httptest.NewRecorder()
	s.handleProviderPoolRouterEvaluationGet(crossResp, crossReq)
	if crossResp.Code != http.StatusNotFound {
		t.Fatalf("cross-pool status = %d body=%s", crossResp.Code, crossResp.Body.String())
	}
}

func TestProviderPoolRouterEvaluationRequiresTwentyHistoryRecords(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{{ID: "member", Source: "cloud", Model: "candidate"}})
	createEvaluationSnapshots(t, s, providerPoolRouterMinimumHistoryRecords-1)
	s.evaluationCatalogLoader = evaluationCatalogFixture

	body := `{"judge_model":"judge","max_queries":1,"repeats":1,"max_output_tokens":10,"request_timeout_seconds":5,"budget_currency":"USD","budget_amount":10,"allow_unknown_pricing":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/provider-pools/evaluation-pool/router/evaluations", strings.NewReader(body))
	req.SetPathValue("id", "evaluation-pool")
	resp := httptest.NewRecorder()
	s.handleProviderPoolRouterEvaluationCreate(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "at least 20 historical records") {
		t.Fatalf("create status = %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestConcurrentProviderPoolEvaluationPOSTHasSingleWinner(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{{ID: "member", Source: "cloud", Model: "candidate"}})
	createEvaluationSnapshots(t, s, providerPoolRouterMinimumHistoryRecords)
	s.evaluationCatalogLoader = evaluationCatalogFixture
	const workers = 8
	statuses := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := `{"judge_model":"judge","max_queries":1,"repeats":1,"max_output_tokens":10,"request_timeout_seconds":5,"budget_currency":"USD","budget_amount":10,"allow_unknown_pricing":false}`
			req := httptest.NewRequest(http.MethodPost, "/api/provider-pools/evaluation-pool/router/evaluations", strings.NewReader(body))
			req.SetPathValue("id", "evaluation-pool")
			resp := httptest.NewRecorder()
			s.handleProviderPoolRouterEvaluationCreate(resp, req)
			statuses <- resp.Code
		}()
	}
	wg.Wait()
	close(statuses)
	accepted, conflicts := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected status %d", status)
		}
	}
	if accepted != 1 || conflicts != workers-1 {
		t.Fatalf("accepted/conflicts = %d/%d", accepted, conflicts)
	}
}

func TestProviderPoolRouterHandlersEnforceJSONAndHideVectors(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{{ID: "member", Source: "cloud", Model: "candidate"}})

	badReq := httptest.NewRequest(http.MethodPost, "/api/provider-pools/evaluation-pool/router/evaluations/preview",
		strings.NewReader(`{"judge_model":"judge","unknown":true}`))
	badReq.SetPathValue("id", "evaluation-pool")
	badResp := httptest.NewRecorder()
	s.handleProviderPoolRouterEvaluationPreview(badResp, badReq)
	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d body=%s", badResp.Code, badResp.Body.String())
	}

	profile := validRouterProfile("evaluation-pool", providerPoolMemberFingerprint(config.GetProviderPools()[0]))
	profile.Profile.Clusters[0].Center = []float64{0.125, 0.25}
	if _, err := s.routerProfiles.CreateProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/provider-pools/evaluation-pool/router/profiles/"+profile.ID, nil)
	req.SetPathValue("id", "evaluation-pool")
	req.SetPathValue("profileID", profile.ID)
	resp := httptest.NewRecorder()
	s.handleProviderPoolRouterProfileGet(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("profile status = %d body=%s", resp.Code, resp.Body.String())
	}
	if bytes.Contains(resp.Body.Bytes(), []byte(`"center"`)) || bytes.Contains(resp.Body.Bytes(), []byte("0.125")) {
		t.Fatalf("profile detail leaked embedding vectors: %s", resp.Body.String())
	}
}

func TestProviderPoolRouterV2DetailIsStructuredAndRedacted(t *testing.T) {
	members := []config.ProviderPoolMember{
		{ID: "a", Source: "cloud", Model: "a"},
		{ID: "b", Source: "cloud", Model: "b"},
	}
	s := evaluationTestServer(t, members)
	pool := config.GetProviderPools()[0]
	profile := testActiveRouterProfileV2(t, pool)
	if _, err := s.routerProfiles.CreateProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/provider-pools/evaluation-pool/router/profiles/"+profile.ID, nil)
	req.SetPathValue("id", "evaluation-pool")
	req.SetPathValue("profileID", profile.ID)
	resp := httptest.NewRecorder()
	s.handleProviderPoolRouterProfileGet(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("profile status = %d body=%s", resp.Code, resp.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	summary, ok := detail["v2"].(map[string]any)
	if !ok || summary["model_type"] != routerprofile.PairwiseModelBT ||
		summary["query_group_count"] != float64(24) || summary["round_count"] != float64(20) {
		t.Fatalf("missing bounded V2 summary: %v", detail)
	}
	for _, forbidden := range []string{`"artifact"`, `"samples"`, `"trees"`, `"nodes"`, `"candidate_response"`, `"judge_reason"`} {
		if bytes.Contains(resp.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("V2 profile leaked %s: %s", forbidden, resp.Body.String())
		}
	}

	listed := providerPoolRouterProfileAPI(profile, pool, "", false)
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if listed.V2 != nil || bytes.Contains(encoded, []byte(`"v2"`)) {
		t.Fatalf("lightweight profile list included V2 detail: %s", encoded)
	}
	missingFallbackPool := pool
	missingFallbackPool.Members = missingFallbackPool.Members[:1]
	blocked := providerPoolRouterProfileAPI(profile, missingFallbackPool, "", false)
	if blocked.ActivationAllowed || blocked.ActivationBlockedReason != "missing_safe_fallback" {
		t.Fatalf("missing fallback activation state = %+v", blocked)
	}
	addedPool := pool
	addedPool.Members = append(append([]config.ProviderPoolMember(nil), pool.Members...),
		config.ProviderPoolMember{ID: "unrelated", Source: "cloud", Model: "other"})
	withDrift := providerPoolRouterProfileAPI(profile, addedPool, "", false)
	if !withDrift.ActivationAllowed || !withDrift.MemberCompatible ||
		!withDrift.MemberFingerprintDrift || withDrift.ActivationBlockedReason != "" {
		t.Fatalf("unrelated member addition state = %+v", withDrift)
	}
	changedPool := pool
	changedPool.Members = append([]config.ProviderPoolMember(nil), pool.Members...)
	changedPool.Members[0].Model = "changed-a"
	blocked = providerPoolRouterProfileAPI(profile, changedPool, "", false)
	if blocked.ActivationAllowed || blocked.MemberCompatible ||
		blocked.ActivationBlockedReason != "candidate_members_incompatible" {
		t.Fatalf("changed selected candidate state = %+v", blocked)
	}
}

func TestProviderPoolRouterActivationRequiresAudit(t *testing.T) {
	s := evaluationTestServer(t, []config.ProviderPoolMember{{ID: "member", Source: "cloud", Model: "candidate"}})
	req := httptest.NewRequest(http.MethodPost, "/api/provider-pools/evaluation-pool/router/profiles/profile/activate",
		strings.NewReader(`{"actor":"local-ui","reason":""}`))
	req.SetPathValue("id", "evaluation-pool")
	req.SetPathValue("profileID", "profile")
	resp := httptest.NewRecorder()
	s.handleProviderPoolRouterProfileActivate(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("missing-audit status = %d body=%s", resp.Code, resp.Body.String())
	}
}

func validRouterProfile(poolID, fingerprint string) routerprofile.Profile {
	value := testActiveRouterProfile(config.ProviderPool{
		ID: poolID,
		Members: []config.ProviderPoolMember{
			{ID: "small", Source: "cloud", Model: "small"},
			{ID: "large", Source: "cloud", Model: "large"},
		},
	})
	value.ID, value.Version, value.MemberFingerprint, value.CreatedBy = "profile-handler", 1, fingerprint, "test"
	return value
}
