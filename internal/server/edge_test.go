package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/ee/edge"
	"github.com/opencsgs/csglite/internal/observability"
	"github.com/opencsgs/csglite/pkg/api"
)

func TestObservabilityMiddlewareKeepsOnlyMetadataForEdgeRequests(t *testing.T) {
	s := newTestServer(t)
	handler := s.observabilityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observationFromContext(r.Context()).setUsage("test/model", "local", "local", "", 11, 4, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"remote answer"}}],"usage":{"prompt_tokens":11,"completion_tokens":4}}`))
	}))
	body := `{"model":"test/model","messages":[{"role":"user","content":"remote caller prompt"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), edgeRequestContextKey{}, "gw-req-1"))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	s.observabilityMu.RLock()
	page, err := s.observability.ListRequests(context.Background(), observability.RequestFilter{APIKeyID: edgeAPIKeyID})
	s.observabilityMu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("edge requests = %d, want 1", page.Total)
	}
	s.observabilityMu.RLock()
	detail, err := s.observability.GetRequest(context.Background(), page.Items[0].ID)
	s.observabilityMu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if detail.RequestBody != "" || detail.ResponseBody != "" {
		t.Fatalf("edge request content was persisted: request=%q response=%q", detail.RequestBody, detail.ResponseBody)
	}
	if detail.RequestID != "gw-req-1" || detail.APIKeyName != edgeAPIKeyName {
		t.Fatalf("unexpected edge metadata: %+v", detail)
	}
	if detail.InputTokens != 11 || detail.OutputTokens != 4 {
		t.Fatalf("edge usage = %d/%d, want 11/4", detail.InputTokens, detail.OutputTokens)
	}
}

func TestEdgeInferenceHandlerRefusesNonInferencePaths(t *testing.T) {
	s := newTestServer(t)
	h := edgeHost{s}.InferenceHandler()
	for _, path := range []string{"/api/ps", "/api/apps/shell", "/v1/models"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestWriteEdgeErrorRewritesGatewayNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/edge/applications", nil)
	writeEdgeError(rec, req, edge.ErrGatewayUnreachable)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Cannot connect to the CSGHub AI gateway.") || strings.Contains(rec.Body.String(), "404 page not found") {
		t.Fatalf("body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/edge/applications", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	writeEdgeError(rec, req, edge.ErrGatewayUnreachable)
	if !strings.Contains(rec.Body.String(), "不能连接 CSGHub AI 网关") {
		t.Fatalf("zh body = %s", rec.Body.String())
	}
}

func TestEdgeLocalAPIRequiresManager(t *testing.T) {
	s := newTestServer(t)
	s.edge = nil
	rec := httptest.NewRecorder()
	s.withEdge(s.handleEdgeStatus)(rec, httptest.NewRequest(http.MethodGet, "/api/edge", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestEdgeServableModel(t *testing.T) {
	cases := []struct {
		info api.ModelInfo
		want bool
	}{
		{api.ModelInfo{Category: "language_model", PipelineTag: "text-generation"}, true},
		{api.ModelInfo{PipelineTag: "image-text-to-text"}, true},
		{api.ModelInfo{Category: "embedding", PipelineTag: "feature-extraction"}, false},
		{api.ModelInfo{Category: "image_generation", PipelineTag: "text-to-image"}, false},
		{api.ModelInfo{Category: "language_model", DecisionOnly: true}, false},
	}
	for _, c := range cases {
		if got := edgeServableModel(c.info); got != c.want {
			t.Errorf("edgeServableModel(%+v) = %v, want %v", c.info, got, c.want)
		}
	}
}

func TestEdgeRoutesRequireEnterpriseLicense(t *testing.T) {
	s := newTestServer(t)
	routes := s.routes()
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/edge"},
		{http.MethodPut, "/api/edge"},
		{http.MethodGet, "/api/edge/applications"},
		{http.MethodPost, "/api/edge/applications"},
		{http.MethodPost, "/api/edge/applications/1/withdraw"},
		{http.MethodGet, "/api/edge/earnings"},
		{http.MethodGet, "/api/edge/balance"},
		{http.MethodGet, "/api/edge/statements"},
	} {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(req.method, req.path, strings.NewReader("{}")))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "feature_not_licensed") {
			t.Fatalf("%s %s without a license = %d %s, want 403 feature_not_licensed", req.method, req.path, rec.Code, rec.Body.String())
		}
	}
	if (edgeHost{s}).Licensed() {
		t.Fatal("the edge connector must not run without a license")
	}
}
