package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestProviderPoolCRUD(t *testing.T) {
	s := newTestServer(t)
	create := httptest.NewRequest(http.MethodPost, "/api/provider-pools", strings.NewReader(`{
		"name":"Code Pool",
		"model":"code-pool",
		"members":[{"id":"local-code","source":"local","model":"Qwen/Qwen3-Coder"}]
	}`))
	w := httptest.NewRecorder()
	s.handleProviderPoolCreate(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	var created api.ProviderPool
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Model != "code-pool" || !created.Enabled ||
		created.Policy != config.ProviderPoolPolicyPriorityWeight ||
		len(created.Members) != 1 || created.Members[0].Weight != 100 {
		t.Fatalf("created pool = %#v", created)
	}

	w = httptest.NewRecorder()
	s.handleProviderPoolsList(w, httptest.NewRequest(http.MethodGet, "/api/provider-pools", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", w.Code, w.Body.String())
	}
	var listed api.ProviderPoolsResponse
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Pools) != 1 || listed.Pools[0].ID != created.ID {
		t.Fatalf("listed pools = %#v", listed.Pools)
	}

	update := httptest.NewRequest(http.MethodPut, "/api/provider-pools/"+created.ID, strings.NewReader(`{
		"name":"Updated Pool",
		"enabled":false,
		"members":[{"id":"cloud-code","source":"cloud","model":"glm-5"}]
	}`))
	update.SetPathValue("id", created.ID)
	w = httptest.NewRecorder()
	s.handleProviderPoolUpdate(w, update)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", w.Code, w.Body.String())
	}
	var updated api.ProviderPool
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Name != "Updated Pool" || updated.Enabled ||
		updated.Policy != config.ProviderPoolPolicyPriorityWeight ||
		updated.Members[0].Source != "cloud" {
		t.Fatalf("updated pool = %#v", updated)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/provider-pools/"+created.ID, nil)
	deleteReq.SetPathValue("id", created.ID)
	w = httptest.NewRecorder()
	s.handleProviderPoolDelete(w, deleteReq)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", w.Code, w.Body.String())
	}
	if pools := config.GetProviderPools(); len(pools) != 0 {
		t.Fatalf("pools after delete = %#v", pools)
	}
}

func TestProviderPoolRejectsInvalidPolicy(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/provider-pools", strings.NewReader(`{
		"name":"Invalid","model":"invalid","policy":"random",
		"members":[{"id":"member","source":"local","model":"model"}]
	}`))
	w := httptest.NewRecorder()
	s.handleProviderPoolCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestProviderPoolPolicyCapabilities(t *testing.T) {
	assertSemantic := func(t *testing.T, capabilities []api.ProviderPoolPolicyCapability, available bool, reason string) {
		t.Helper()
		semantic := semanticPolicyCapability(capabilities)
		if semantic.Available != available || semantic.Reason != reason || !semantic.Experimental {
			t.Fatalf("semantic capability = %#v", semantic)
		}
	}
	assertSemantic(t, providerPoolPolicyCapabilitiesFor(false, nil, nil), false, "opencsg_login_required")
	assertSemantic(t, providerPoolPolicyCapabilitiesFor(true, nil, nil), false, "required_embedding_model_unavailable")
	assertSemantic(t, providerPoolPolicyCapabilitiesFor(true, []api.ModelInfo{{
		Model: semanticEmbeddingModel, PipelineTag: "feature-extraction",
	}}, nil), true, "")
}

func TestProviderPoolSemanticAcceptsArbitraryMembersWithEmbeddingCapability(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("catalog path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{{
				"id": semanticEmbeddingModel, "task": "feature-extraction",
			}},
		})
	}))
	defer gateway.Close()
	s := newTestServerWithConfig(t, &config.Config{
		ServerURL: gateway.URL, AIGatewayURL: gateway.URL, OpenCSGAPIKey: "test-key",
	})
	s.cloud = cloud.NewService(gateway.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/provider-pools", strings.NewReader(`{
		"name":"Arbitrary Semantic",
		"model":"arbitrary-semantic",
		"policy":"semantic",
		"members":[{"id":"custom","source":"cloud","model":"organization/custom-model"}]
	}`))
	w := httptest.NewRecorder()
	s.handleProviderPoolCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	var pool api.ProviderPool
	if err := json.NewDecoder(w.Body).Decode(&pool); err != nil {
		t.Fatal(err)
	}
	if pool.Policy != config.ProviderPoolPolicySemantic || len(pool.Members) != 1 ||
		pool.Members[0].Model != "organization/custom-model" {
		t.Fatalf("created arbitrary semantic pool = %+v", pool)
	}
}

func TestProviderPoolSemanticCredentialAcceptsLoginOrAPIKey(t *testing.T) {
	tests := []struct {
		name string
		auth cloudAuthStatus
		want bool
	}{
		{name: "missing"},
		{name: "login", auth: cloudAuthStatus{Authenticated: true}, want: true},
		{name: "api key", auth: cloudAuthStatus{HasAPIKey: true}, want: true},
		{name: "both", auth: cloudAuthStatus{Authenticated: true, HasAPIKey: true}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasProviderPoolSemanticCredential(test.auth); got != test.want {
				t.Fatalf("credential available = %v, want %v", got, test.want)
			}
		})
	}
}

func TestProviderPoolRejectsDuplicateModelAndUnavailableProvider(t *testing.T) {
	s := newTestServer(t)
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "existing", Name: "Existing", Model: "shared-model", Enabled: true,
		Members: []config.ProviderPoolMember{{ID: "local", Source: "local", Model: "model"}},
	}}); err != nil {
		t.Fatalf("save pool: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/provider-pools", strings.NewReader(`{
		"name":"Other",
		"model":"shared-model",
		"members":[{"id":"missing","source":"provider:missing","model":"gpt"}]
	}`))
	w := httptest.NewRecorder()
	s.handleProviderPoolCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if len(config.GetProviderPools()) != 1 {
		t.Fatal("invalid pool was saved")
	}
}

func TestProviderPoolRejectsExistingProviderModelAndDoesNotHijackRouting(t *testing.T) {
	s := newTestServer(t)
	if err := config.SaveProviders([]config.ThirdPartyProvider{{
		ID: "provider1", Name: "Provider", BaseURL: "https://example.com/v1", APIKey: "secret", Enabled: true,
	}}); err != nil {
		t.Fatalf("save provider: %v", err)
	}
	if err := config.ReplaceProviderModelAllowlist("provider1", []string{"shared-model"}); err != nil {
		t.Fatalf("save provider models: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/provider-pools", strings.NewReader(`{
		"name":"Conflicting Pool",
		"model":"shared-model",
		"members":[{"id":"provider","source":"provider:provider1","model":"shared-model"}]
	}`))
	w := httptest.NewRecorder()
	s.handleProviderPoolCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}

	// Old or manually-edited configuration can still contain a conflict. In
	// that case unscoped requests must retain the existing model route.
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "conflict", Name: "Conflict", Model: "shared-model", Enabled: true,
		Members: []config.ProviderPoolMember{{ID: "local", Source: "local", Model: "fallback-model"}},
	}}); err != nil {
		t.Fatalf("save conflicting pool: %v", err)
	}
	eng, err := s.getChatEngine(t.Context(), "shared-model", "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatalf("get existing provider engine: %v", err)
	}
	if _, ok := eng.(*providerPoolEngine); ok {
		t.Fatal("provider pool hijacked an existing provider model route")
	}
}

func TestProviderPoolRoutesToFallbackMember(t *testing.T) {
	s := newTestServer(t)
	var receivedModel string
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"busy"}}`, http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		receivedModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer working.Close()
	if err := config.SaveProviders([]config.ThirdPartyProvider{
		{ID: "first", Name: "First", BaseURL: failing.URL + "/v1", APIKey: "one", Enabled: true},
		{ID: "second", Name: "Second", BaseURL: working.URL + "/v1", APIKey: "two", Enabled: true},
	}); err != nil {
		t.Fatalf("save providers: %v", err)
	}
	if err := config.SaveProviderPools([]config.ProviderPool{{
		ID: "code", Name: "Code", Model: "code-pool", Enabled: true,
		Members: []config.ProviderPoolMember{
			{ID: "first", Source: "provider:first", Model: "upstream-first", Priority: 0, Weight: 10},
			{ID: "second", Source: "provider:second", Model: "upstream-second", Priority: 1, Weight: 1},
		},
	}}); err != nil {
		t.Fatalf("save pools: %v", err)
	}
	eng, err := s.getChatEngine(t.Context(), "code-pool", "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatalf("get pool engine: %v", err)
	}
	proxy, ok := eng.(inference.ChatCompletionProxier)
	if !ok {
		t.Fatal("pool engine does not proxy chat completions")
	}
	resp, err := proxy.ChatCompletion(t.Context(), map[string]any{
		"model": "code-pool", "messages": []any{},
	})
	if err != nil {
		t.Fatalf("chat completion: %v", err)
	}
	resp.Body.Close()
	if receivedModel != "upstream-second" {
		t.Fatalf("fallback received model = %q, want upstream-second", receivedModel)
	}
	if got := resp.Header.Get(providerPoolMemberSourceHeader); got != "provider:second" {
		t.Fatalf("member source header = %q, want provider:second", got)
	}
	if got := resp.Header.Get(providerPoolMemberModelHeader); got != "upstream-second" {
		t.Fatalf("member model header = %q, want upstream-second", got)
	}
	if got := resp.Header.Get(providerPoolFallbackCountHeader); got != "1" {
		t.Fatalf("fallback count header = %q, want 1", got)
	}
}
