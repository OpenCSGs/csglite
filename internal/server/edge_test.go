package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/ee/edge"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/model"
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
	if !strings.Contains(rec.Body.String(), "does not provide the edge serving API (HTTP 404)") || strings.Contains(rec.Body.String(), "404 page not found") {
		t.Fatalf("body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/edge/applications", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	writeEdgeError(rec, req, edge.ErrGatewayUnreachable)
	if !strings.Contains(rec.Body.String(), "未提供端侧服务接口") {
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

func TestEdgeSharesModelServiceWithClusterBackend(t *testing.T) {
	useIsolatedStorageHome(t)
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)

	bus := cluster.NewMemoryBus()
	master := newClusterTestServer(t, bus)
	worker := newClusterTestServer(t, bus)
	const repo = "acme/chat-model"
	mustSaveLocalModel(t, worker.cfg.ModelDir, &model.LocalModel{
		Namespace: "acme", Name: "chat-model", Format: model.FormatGGUF, PipelineTag: "text-generation",
		Size: 1 << 20, Files: []string{"model.gguf"}, DownloadedAt: time.Now(),
	})
	modelID := worker.localInferenceModelID(repo)
	worker.mu.Lock()
	worker.engines[engineCacheKey(repo, engineModeChat)] = &managedEngine{
		engine: &fakeChatCompletionEngine{resp: api.OpenAIChatResponse{
			ID: "completion", Model: modelID,
			Choices: []api.OpenAIChoice{{Message: &api.Message{Role: "assistant", Content: "from worker"}}},
		}},
		numParallel: 2, nGPULayers: -1, lastUsed: time.Now(), keepAlive: -1,
	}
	worker.mu.Unlock()
	_, token, err := master.cluster.CreateCluster("edge test")
	if err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "cluster discovery", func() bool { return len(worker.cluster.DiscoveredNodes()) == 1 })
	if _, err := worker.cluster.Join(t.Context(), token, ""); err != nil {
		t.Fatal(err)
	}

	host := edgeHost{master}
	workerSource := cluster.SourceNodePrefix + worker.cluster.Identity().UUID
	clusterWait(t, "available worker model", func() bool {
		return master.cluster.ModelSourceState(t.Context(), modelID, workerSource) == "configured"
	})
	for _, item := range host.Models(t.Context()) {
		if item.Name == modelID {
			t.Fatalf("unpublished cluster model appeared in the sharing catalog: %+v", item)
		}
	}
	pool := config.ProviderPool{
		ID: "edge-service", Name: "Edge service", Model: modelID, Enabled: true, Policy: "fixed", Category: "chat",
		Members: []config.ProviderPoolMember{{ID: "worker", Source: workerSource, Model: modelID}},
	}
	if err := config.SaveProviderPools([]config.ProviderPool{pool}); err != nil {
		t.Fatal(err)
	}
	gatewayID := edge.ModelServiceID(pool.ID)
	catalog := map[string]edge.Model{}
	for _, item := range host.Models(t.Context()) {
		catalog[item.ID] = item
	}
	if got := catalog[gatewayID]; !got.Ready || got.Source != edge.ModelSourceGateway || got.Name != modelID {
		t.Fatalf("AI Gateway model was not listed: %+v", catalog)
	}

	handler := host.InferenceHandler()
	send := func(id, source string, stream bool) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"model": id, "source": source, "stream": stream,
			"messages": []map[string]string{{"role": "user", "content": "hello"}},
		})
		return gatewayRequest(handler, http.MethodPost, "/v1/chat/completions", string(body), "")
	}
	for _, stream := range []bool{false, true} {
		rec := send(gatewayID, "", stream)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from worker") ||
			(stream && !strings.Contains(rec.Body.String(), "[DONE]")) ||
			rec.Header().Get(cluster.NodeHeader) != worker.cluster.Identity().UUID ||
			rec.Header().Get(providerPoolMemberSourceHeader) != workerSource {
			t.Fatalf("model service stream=%t: %d %v %s", stream, rec.Code, rec.Header(), rec.Body)
		}
	}
	if rec := send(gatewayID, "local", false); rec.Code != http.StatusBadRequest {
		t.Fatalf("model service accepted a conflicting caller source: %d %s", rec.Code, rec.Body)
	}
	if rec := send("edge:cluster:"+repo, "", false); rec.Code != http.StatusNotFound {
		t.Fatalf("direct cluster ID was accepted: %d %s", rec.Code, rec.Body)
	}
	if rec := send(repo, cluster.SourceCluster, false); rec.Code != http.StatusBadRequest {
		t.Fatalf("legacy ID accepted a source override: %d %s", rec.Code, rec.Body)
	}
	if got, source, err := host.modelRoute(repo, "chat/completions"); err != nil || got != repo || source != "local" {
		t.Fatalf("legacy local route = %q, %q, %v", got, source, err)
	}
	pool.Model = "renamed-service"
	if err := config.SaveProviderPools([]config.ProviderPool{pool}); err != nil {
		t.Fatal(err)
	}
	if rec := send(gatewayID, "", false); rec.Code != http.StatusOK || rec.Header().Get(providerPoolMemberSourceHeader) != workerSource {
		t.Fatalf("renaming the service broke the application: %d %s", rec.Code, rec.Body)
	}

	master.cluster.Stop()
	worker.cluster.Stop()
	for i := 0; i < 6; i++ {
		master.cluster.Directory().MarkFailure(worker.cluster.Identity().UUID, errors.New("offline"))
	}
	if rec := send(gatewayID, "", false); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("service remained available after worker went offline: %d %s", rec.Code, rec.Body)
	}
}

func TestEdgeRoutesOpenWithoutLicense(t *testing.T) {
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
		if rec.Code == http.StatusForbidden || strings.Contains(rec.Body.String(), "feature_not_licensed") {
			t.Fatalf("%s %s without a license = %d %s; the feature is not license-gated", req.method, req.path, rec.Code, rec.Body.String())
		}
	}
	if got := s.edgeModelLimit(); got != license.QuotaMaxEdgeModels.CommunityValue {
		t.Fatalf("edge model limit without a license = %d, want %d", got, license.QuotaMaxEdgeModels.CommunityValue)
	}
}

// edgeQuotaHost points the connector at a stub gateway.
type edgeQuotaHost struct{ gateway string }

func (h edgeQuotaHost) InferenceHandler() http.Handler      { return http.NotFoundHandler() }
func (h edgeQuotaHost) Models(context.Context) []edge.Model { return nil }
func (h edgeQuotaHost) ActiveRequests() int                 { return 0 }
func (h edgeQuotaHost) Cloud() (string, string)             { return h.gateway, "user-token" }
func (h edgeQuotaHost) Version() string                     { return "test" }

func TestEdgeSubmitEnforcesCommunityModelLimit(t *testing.T) {
	var apps atomic.Value
	apps.Store(`{"data":[{"status":"approved"}],"total":1}`)
	var submitted atomic.Bool
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/edge/applications" {
			_, _ = w.Write([]byte(apps.Load().(string)))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/edge/nodes" {
			_, _ = w.Write([]byte(`{"data":{"token":"node-token"}}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/edge/applications" {
			submitted.Store(true)
			_, _ = w.Write([]byte(`{"data":{"id":2}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gateway.Close()

	s := newTestServer(t)
	m, err := edge.New(edgeQuotaHost{gateway: gateway.URL}, t.TempDir(), "test-node")
	if err != nil {
		t.Fatal(err)
	}
	s.edge = m

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/edge/applications", strings.NewReader(`{"model_name":"m","local_model_id":"1"}`))
	req.Header.Set("Accept-Language", "zh-CN")
	s.handleEdgeSubmit(rec, req, m)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "最多贡献 1 个模型") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if submitted.Load() {
		t.Fatal("the application reached the gateway past the limit")
	}

	// A withdrawn application no longer holds the slot.
	apps.Store(`{"data":[{"status":"withdrawn"}],"total":1}`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/edge/applications", strings.NewReader(`{"model_name":"m","local_model_id":"1"}`))
	s.handleEdgeSubmit(rec, req, m)
	if rec.Code != http.StatusOK || !submitted.Load() {
		t.Fatalf("status = %d %s, want the submission to go through", rec.Code, rec.Body.String())
	}
}
