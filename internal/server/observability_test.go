package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/correlation"
	"github.com/opencsgs/csglite/internal/observability"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestClusterUsageSourceRetainsExactNodeIdentity(t *testing.T) {
	s := &Server{}
	for _, source := range []string{"cluster", "node:worker-a", "node:worker-b"} {
		resolved, kind, _ := s.resolveAPIUsageSource(context.Background(), "deployment/runtime", source)
		if resolved != source || kind != apiUsageSourceCluster {
			t.Fatalf("cluster route %q resolved as %q (%q)", source, resolved, kind)
		}
	}
}

func TestRequestPricingSnapshotKnownAndUnknown(t *testing.T) {
	s := &Server{}
	s.rememberModelPricing([]api.ModelInfo{{
		Source: "cloud", Model: "priced",
		Pricing: &api.ModelPricing{
			InputTokenPrice:  &api.ModelTokenPrice{Currency: "USD", PricePerMillion: 2},
			OutputTokenPrice: &api.ModelTokenPrice{Currency: "USD", PricePerMillion: 4},
		},
	}})
	snapshot, cost := s.requestPricingSnapshot("cloud", "priced", 1000, 500)
	if !snapshot.Known || snapshot.Currency != "USD" || cost != 0.004 {
		t.Fatalf("known snapshot=%+v cost=%v", snapshot, cost)
	}
	snapshot, cost = s.requestPricingSnapshot("cloud", "unknown", 1000, 500)
	if snapshot.Known || cost != 0 {
		t.Fatalf("unknown snapshot=%+v cost=%v", snapshot, cost)
	}
}

func TestObservabilityMiddlewareCapturesTextGenerationAndRedactsSecrets(t *testing.T) {
	s := newTestServer(t)
	handler := s.observabilityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observationFromContext(r.Context()).setUsage("test/model", "local", "local", "", 7, 3, nil)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok","access_token":"response-secret","usage":{"prompt_tokens":7,"prompt_tokens_details":{"cached_tokens":5}}}`))
	}))
	inlineSecret := "gk_" + strings.Repeat("x", 26)
	body := `{"model":"test/model","stream":true,"api_key":"request-secret","messages":[{"role":"user","content":"hello ` + inlineSecret + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set(correlation.RequestIDHeader, "gateway-request")
	req.Header.Set(correlation.B3TraceIDHeader, "463ac35c9f6413ad48485a3953bb6124")
	req.Header.Set(observabilityTraceIDHeader, "trace-client")
	req.Header.Set(observabilityThreadIDHeader, "thread-client")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Header().Get(observabilityTraceIDHeader) != "trace-client" {
		t.Fatalf("trace header = %q", recorder.Header().Get(observabilityTraceIDHeader))
	}
	if recorder.Header().Get(correlation.RequestIDHeader) != "gateway-request" ||
		recorder.Header().Get(correlation.B3TraceIDHeader) != "463ac35c9f6413ad48485a3953bb6124" {
		t.Fatalf("standard correlation headers = %v", recorder.Header())
	}
	s.observabilityMu.RLock()
	page, err := s.observability.ListRequests(req.Context(), observability.RequestFilter{})
	s.observabilityMu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("captured requests = %d, want 1", page.Total)
	}
	s.observabilityMu.RLock()
	detail, err := s.observability.GetRequest(req.Context(), page.Items[0].ID)
	s.observabilityMu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if detail.TraceID != "trace-client" || detail.ThreadID != "thread-client" || !detail.Stream {
		t.Fatalf("unexpected trace metadata: %+v", detail)
	}
	if detail.RequestID != "gateway-request" || detail.B3TraceID != "463ac35c9f6413ad48485a3953bb6124" {
		t.Fatalf("unexpected standard correlation metadata: %+v", detail)
	}
	if detail.InputTokens != 7 || detail.OutputTokens != 3 || detail.FirstTokenLatencyMS < 0 {
		t.Fatalf("unexpected usage metadata: %+v", detail)
	}
	if detail.CacheReadInputTokens != 5 || detail.CacheEligibleTokens != 7 {
		t.Fatalf("unexpected cache usage metadata: %+v", detail)
	}
	if strings.Contains(detail.RequestBody, "request-secret") || strings.Contains(detail.RequestBody, inlineSecret) ||
		strings.Contains(detail.ResponseBody, "response-secret") {
		t.Fatalf("sensitive values were persisted: request=%s response=%s", detail.RequestBody, detail.ResponseBody)
	}
	if !strings.Contains(detail.RequestBody, "[REDACTED]") || !strings.Contains(detail.ResponseBody, "[REDACTED]") {
		t.Fatalf("redaction marker missing: request=%s response=%s", detail.RequestBody, detail.ResponseBody)
	}
}

func TestObservabilityMiddlewareKeepsFallbackUsageWhenStreamReportsZeros(t *testing.T) {
	s := newTestServer(t)
	handler := s.observabilityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observationFromContext(r.Context()).setUsage("pool/model", "pool:test", "pool", "test", 37, 0, nil)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0,\"total_tokens\":0,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\ndata: [DONE]\n\n"))
	}))
	req := httptest.NewRequest(http.MethodPost, "/providers/test/v1/chat/completions",
		strings.NewReader(`{"model":"pool/model","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	s.observabilityMu.RLock()
	page, err := s.observability.ListRequests(req.Context(), observability.RequestFilter{})
	s.observabilityMu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("captured requests = %d, want 1", page.Total)
	}
	s.observabilityMu.RLock()
	detail, err := s.observability.GetRequest(req.Context(), page.Items[0].ID)
	s.observabilityMu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if detail.InputTokens != 37 || detail.OutputTokens != 0 {
		t.Fatalf("fallback usage was overwritten: %+v", detail)
	}
}

func TestObservationResponseCacheUsage(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		input    int64
		output   int64
		read     int64
		creation int64
		eligible int64
	}{
		{
			name:     "openai chat",
			body:     `{"usage":{"prompt_tokens":100,"completion_tokens":12,"total_tokens":112,"prompt_tokens_details":{"cached_tokens":75}}}`,
			input:    100,
			output:   12,
			read:     75,
			eligible: 100,
		},
		{
			name:     "responses streaming",
			body:     "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":80,\"output_tokens\":7,\"total_tokens\":87,\"input_tokens_details\":{\"cached_tokens\":20,\"cache_write_tokens\":10}}}}\n\ndata: [DONE]\n",
			input:    80,
			output:   7,
			read:     20,
			creation: 10,
			eligible: 80,
		},
		{
			name:     "anthropic streaming",
			body:     "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1,\"cache_read_input_tokens\":60,\"cache_creation_input_tokens\":30}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":8}}\n",
			input:    10,
			output:   8,
			read:     60,
			creation: 30,
			eligible: 100,
		},
		{
			name:     "moonshot top level cache",
			body:     `{"usage":{"prompt_tokens":10,"completion_tokens":4,"cached_tokens":60}}`,
			input:    10,
			output:   4,
			read:     60,
			eligible: 70,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage := observationResponseUsageFromBodies([]byte(test.body))
			if usage.inputTokens != test.input || usage.outputTokens != test.output ||
				usage.readTokens != test.read || usage.creationTokens != test.creation || usage.eligibleTokens != test.eligible {
				t.Fatalf("usage = %+v, want input=%d output=%d read=%d creation=%d eligible=%d",
					usage, test.input, test.output, test.read, test.creation, test.eligible)
			}
		})
	}
}

func TestObservationResponseUsageReadsFinalUsageFromTruncatedTail(t *testing.T) {
	var writer observationResponseWriter
	writer.capture([]byte("data: " + strings.Repeat("x", observabilityBodyLimit) + "\n"))
	writer.capture([]byte("data: {\"usage\":{\"prompt_tokens\":79953,\"completion_tokens\":5082,\"total_tokens\":85035,\"prompt_tokens_details\":{\"cached_tokens\":79616}}}\n"))

	usage := observationResponseUsageFromBodies(writer.body.Bytes(), writer.usageTail)
	if usage.inputTokens != 79953 || usage.outputTokens != 5082 || usage.readTokens != 79616 || usage.eligibleTokens != 79953 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestObservabilityMiddlewareIgnoresManagementRoutes(t *testing.T) {
	s := newTestServer(t)
	handler := s.observabilityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(`{"model":"embed"}`))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	s.observabilityMu.RLock()
	page, err := s.observability.ListRequests(req.Context(), observability.RequestFilter{})
	s.observabilityMu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("captured management requests = %d, want 0", page.Total)
	}
}

func TestObservabilityRecordsDecisionServicesAcrossListeners(t *testing.T) {
	s := newNativeDecisionTestServer(t)
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)
	s.engines["org/laya"] = &managedEngine{engine: &fakeSystemOneEngine{}, numCtx: 8192, numParallel: 1}
	pool := config.ProviderPool{ID: "laya-service", Name: "Laya", Model: "public-laya", Enabled: true, Policy: "fixed",
		Members: []config.ProviderPoolMember{{ID: "member", Source: "local", Model: "org/laya"}}}
	if err := config.SaveProviderPools([]config.ProviderPool{pool}); err != nil {
		t.Fatal(err)
	}
	_, key, err := s.apiKeys.Create("decision-observation")
	if err != nil {
		t.Fatal(err)
	}
	for listener, handler := range map[string]http.Handler{"console": s.routes(), "desktop": s.externalAPIRoutes(), "public": s.publicClusterInferenceRoutes()} {
		for _, valid := range []bool{true, false} {
			name := listener + "/success"
			body := `{"model":"public-laya","input":"I need a refund.","questions":[{"type":"predicate","name":"refund","instructions":"Refund requested?"}]}`
			wantStatus := http.StatusOK
			if !valid {
				name = listener + "/failure"
				body = `{"model":"public-laya","input":"I need a refund.","questions":[{"type":"invalid","name":"refund"}]}`
				wantStatus = http.StatusBadRequest
			}
			t.Run(name, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodPost, "/ai-gateway/v1/decisions", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer "+key)
				r.Header.Set(observabilityTraceIDHeader, "decision-trace")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != wantStatus {
					t.Fatalf("status=%d body=%s", w.Code, w.Body)
				}
				id := w.Header().Get(observabilityRequestIDHeader)
				if id == "" {
					t.Fatal("decision request has no observation ID")
				}
				record, err := s.observability.GetRequest(r.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				if record.Protocol != "decisions" || record.Model != pool.Model || record.Path != r.URL.Path || record.StatusCode != wantStatus || record.TraceID != "decision-trace" {
					t.Fatalf("unexpected decision observation: %+v", record)
				}
				if record.PoolID != pool.ID || record.MemberModel != "org/laya" || record.ActualMemberID != "member" {
					t.Fatalf("missing decision routing metadata: %+v", record)
				}
				if valid && (record.InputTokens != 40 || record.OutputTokens != 0 || !strings.Contains(record.RequestBody, "questions") || !strings.Contains(record.ResponseBody, "probability")) {
					t.Fatalf("missing decision payload or usage: %+v", record)
				}
				if !valid && (record.Status != "failed" || record.ErrorMessage == "") {
					t.Fatalf("missing decision failure: %+v", record)
				}
				if record.FirstTokenLatencyMS != 0 {
					t.Fatal("decision response time was reported as first-token latency")
				}
			})
		}
	}
}

func TestObservedDecisionRoutes(t *testing.T) {
	for _, path := range []string{"/v1/decide", "/v1/decisions", "/ai-gateway/v1/decisions", "/providers/laya/v1/decisions"} {
		t.Run(path, func(t *testing.T) {
			if !isObservedRequest(httptest.NewRequest(http.MethodPost, path, nil)) || observationProtocol(path) != "decisions" {
				t.Fatal("decision route excluded from observations")
			}
			if isObservedRequest(httptest.NewRequest(http.MethodGet, path, nil)) {
				t.Fatal("observed a non-inference GET")
			}
		})
	}
}

func TestObservabilityHandlersListAndClear(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"model":"test/model"}`))
	handler := s.observabilityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observationFromContext(r.Context()).setUsage("test/model", "local", "local", "", 2, 1, nil)
		_, _ = w.Write([]byte(`{"message":{"content":"ok"}}`))
	}))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	listRequest := httptest.NewRequest(http.MethodGet, "/api/observability/requests?limit=10", nil)
	listRecorder := httptest.NewRecorder()
	s.handleObservabilityRequests(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var response struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 {
		t.Fatalf("list total = %d, want 1", response.Total)
	}

	clearRecorder := httptest.NewRecorder()
	s.handleObservabilityClear(clearRecorder, httptest.NewRequest(http.MethodDelete, "/api/observability", nil))
	if clearRecorder.Code != http.StatusOK {
		t.Fatalf("clear status = %d body=%s", clearRecorder.Code, clearRecorder.Body.String())
	}
}

func TestObservabilityFacetsHandler(t *testing.T) {
	s := newTestServer(t)
	requests := []struct {
		model  string
		source string
	}{
		{"test/model-a", "local"},
		{"test/model-a", "local"},
		{"test/model-b", "cloud"},
	}
	for _, item := range requests {
		req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"model":"`+item.model+`"}`))
		model, source := item.model, item.source
		handler := s.observabilityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			observationFromContext(r.Context()).setUsage(model, source, source, "", 1, 1, nil)
			_, _ = w.Write([]byte(`{"message":{"content":"ok"}}`))
		}))
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	recorder := httptest.NewRecorder()
	s.handleObservabilityFacets(recorder, httptest.NewRequest(http.MethodGet, "/api/observability/facets", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("facets status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Models []api.ObservabilityFacetValue `json:"models"`
		Routes []api.ObservabilityFacetValue `json:"routes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Models) != 2 {
		t.Fatalf("model facets = %+v, want 2 distinct models", response.Models)
	}
	if len(response.Routes) != 2 {
		t.Fatalf("route facets = %+v, want 2 distinct routes", response.Routes)
	}
	byValue := make(map[string]api.ObservabilityFacetValue)
	for _, route := range response.Routes {
		byValue[route.Value] = route
	}
	if byValue["local"].Count != 2 || byValue["cloud"].Count != 1 {
		t.Fatalf("route counts = %+v, want local=2 cloud=1", response.Routes)
	}
}
