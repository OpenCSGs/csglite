package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

type countedDeploymentEngine struct {
	fakeEngine
	closed atomic.Int32
}

func (e *countedDeploymentEngine) Close() error { e.closed.Add(1); return nil }

type unhealthyDeploymentEngine struct {
	countedDeploymentEngine
}

func (*unhealthyDeploymentEngine) RuntimeError() error {
	return errors.New("llama-server inference failed: insufficient memory")
}

func TestDeploymentDoesNotPublishRuntimeWithFatalStartupError(t *testing.T) {
	_, h, d := deploymentRuntimeFixture(t)
	engine := &unhealthyDeploymentEngine{}
	old := loadEngineWithProgress
	t.Cleanup(func() { loadEngineWithProgress = old })
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		return engine, nil
	}
	if err := h.StartDeployment(context.Background(), d, func(cluster.DeploymentStatus) {}); err == nil || !strings.Contains(err.Error(), "insufficient memory") {
		t.Fatal("fatal startup failure was not returned", err)
	}
	if h.DeploymentRunning(d) || engine.closed.Load() != 1 {
		t.Fatal("poisoned runtime was published or leaked")
	}
}

type healthSystemOneEngine struct {
	fakeSystemOneEngine
	broken atomic.Bool
}

func (e *healthSystemOneEngine) RuntimeError() error {
	if e.broken.Load() {
		return errors.New("llama-server inference failed: insufficient memory")
	}
	return nil
}

func TestPrepareOpenCSGEmbeddingDeploymentDownloadsDefaultBranch(t *testing.T) {
	const repo = "Qwen/Qwen3-Embedding-0.6B"
	var downloads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models/" + repo:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"path": repo, "default_branch": "main"}})
		case "/api/v1/models/" + repo + "/tree":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"type": "file", "name": "model.safetensors", "path": "model.safetensors", "size": 4}}})
		case "/api/v1/models/" + repo + "/raw/model.safetensors":
			downloads.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": "test"})
		default:
			t.Errorf("unexpected model request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	s := newTestServer(t)
	s.cfg.ServerURL = upstream.URL
	// This four-byte fixture tests revision handling, not disk capacity. The
	// production 50 GB reserve can exceed a Windows CI runner's available space.
	if _, err := s.cluster.UpdateSettings(func(settings *cluster.Settings) { settings.DiskReserveGB = 0 }); err != nil {
		t.Fatal(err)
	}
	h := &clusterHost{s: s}
	d := cluster.Deployment{ID: uuid.NewString(), DeploymentSpec: cluster.DeploymentSpec{
		Name: "Qwen embedding", Repository: repo, ArtifactSource: "opencsg", Revision: "main", Kind: "embedding",
	}}
	for range 2 {
		size, err := h.PrepareDeployment(context.Background(), d, func(cluster.DeploymentStatus) {})
		if err != nil || size != 4 {
			t.Fatalf("PrepareDeployment: size=%d, error=%v", size, err)
		}
	}
	if downloads.Load() != 1 {
		t.Fatalf("model downloaded %d times, want one shared snapshot", downloads.Load())
	}
	lm, err := s.manager.Get(repo)
	if err != nil {
		t.Fatal(err)
	}
	if lm.ArtifactSource != "opencsg" || lm.RequestedRevision != "main" || lm.ResolvedRevision != "main" {
		t.Fatalf("downloaded snapshot metadata: %+v", lm)
	}
	d.Revision = "different-commit"
	if _, err := h.PrepareDeployment(context.Background(), d, func(cluster.DeploymentStatus) {}); err == nil {
		t.Fatal("mismatched deployment revision reused the shared snapshot")
	}
}

func deploymentRuntimeFixture(t *testing.T) (*Server, *clusterHost, cluster.Deployment) {
	t.Helper()
	s := newTestServer(t)
	h := &clusterHost{s: s}
	d := cluster.Deployment{ID: uuid.NewString(), Generation: 1, Desired: "running", DeploymentSpec: cluster.DeploymentSpec{Name: "test", Repository: "acme/test", ArtifactSource: "opencsg", Revision: "commit-1", Kind: "chat", NumCtx: 4096, NumParallel: 2, NGPULayers: -1}}
	base := s.cfg.ModelDir
	lm := &model.LocalModel{Namespace: "acme", Name: "test", Repository: "acme/test", Format: model.FormatGGUF, Size: 4, Files: []string{"model.gguf"}, ArtifactSource: "opencsg", RequestedRevision: "commit-1", PipelineTag: "text-generation"}
	if err := model.SaveManifest(base, lm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "acme", "test", "model.gguf"), []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	return s, h, d
}

func TestDeploymentRuntimeIsolationAndDrain(t *testing.T) {
	s, h, d := deploymentRuntimeFixture(t)
	if size, err := h.PrepareDeployment(context.Background(), d, func(cluster.DeploymentStatus) {}); err != nil || size != 4 {
		t.Fatal(size, err)
	}
	engine := &countedDeploymentEngine{}
	old := loadEngineWithProgress
	t.Cleanup(func() { loadEngineWithProgress = old })
	loadEngineWithProgress = func(dir string, lm *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, ctx, parallel, layers int, _, _, _ string) (inference.Engine, error) {
		if lm.FullName() != d.ModelID() || ctx != 4096 || parallel != 2 || layers != -1 {
			t.Fatal("deployment runtime options/alias not preserved")
		}
		if filepath.Dir(filepath.Dir(dir)) != s.cfg.ModelDir {
			t.Fatal("runtime did not use the model library", dir)
		}
		return engine, nil
	}
	userEngine := &countedDeploymentEngine{}
	s.engines[engineCacheKey("acme/test", engineModeChat)] = &managedEngine{engine: userEngine, keepAlive: -1}
	if err := h.StartDeployment(context.Background(), d, func(cluster.DeploymentStatus) {}); err != nil {
		t.Fatal(err)
	}
	if !h.DeploymentRunning(d) {
		t.Fatal("deployment was not started")
	}
	assertListed := func(want bool) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handlePs(rec, httptest.NewRequest(http.MethodGet, "/api/ps", nil))
		var response api.PsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != 200 {
			t.Fatalf("running models: %d %s", rec.Code, rec.Body)
		}
		found := false
		for _, item := range response.Models {
			if item.Model == d.ModelID() {
				found = true
				if item.DisplayName != d.Name || item.Repository != d.Repository || item.Size != 4 || item.Status != "running" {
					t.Fatalf("managed runtime metadata: %+v", item)
				}
			}
		}
		if found != want {
			t.Fatalf("managed runtime listed = %v, want %v", found, want)
		}
	}
	assertListed(true)
	s.setEngineKeepAlive(d.ModelID(), 0)
	if s.engines[engineCacheKey(d.ModelID(), engineModeChat)].keepAlive != -1 {
		t.Fatal("request keep_alive overrode managed runtime lifetime")
	}
	if _, err := s.getOrLoadEngine(d.ModelID()); err == nil {
		t.Fatal("unassigned deployment accepted inference")
	}
	release := s.beginEngineUse(d.ModelID(), engineModeChat, engine)
	if err := h.StopDeployment(d); err == nil {
		t.Fatal("stop interrupted active inference")
	}
	if engine.closed.Load() != 0 {
		t.Fatal("busy engine closed")
	}
	assertListed(true) // Draining still occupies the local runtime until requests finish.
	release()
	if err := h.StopDeployment(d); err != nil {
		t.Fatal(err)
	}
	if engine.closed.Load() != 1 || userEngine.closed.Load() != 0 {
		t.Fatal("stop did not isolate runtime ownership")
	}
	assertListed(false)
	if _, err := h.deploymentModelManager(d).Get(deploymentArtifactID(d)); err != nil {
		t.Fatal("stop deleted model cache", err)
	}
	if _, err := s.getOrLoadEngine(d.ModelID()); err == nil {
		t.Fatal("stopped deployment cold-loaded itself")
	}
}

func TestDeploymentCancellationClosesRuntimeBeforePublication(t *testing.T) {
	_, h, d := deploymentRuntimeFixture(t)
	engine := &countedDeploymentEngine{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := loadEngineWithProgress
	t.Cleanup(func() { loadEngineWithProgress = old })
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		cancel()
		return engine, nil
	}
	if err := h.StartDeployment(ctx, d, func(cluster.DeploymentStatus) {}); err == nil {
		t.Fatal("cancelled runtime published")
	}
	if h.DeploymentRunning(d) || engine.closed.Load() != 1 {
		t.Fatal("cancelled load leaked an engine")
	}
}

func TestDeploymentRejectsMismatchedCachedRevision(t *testing.T) {
	_, h, d := deploymentRuntimeFixture(t)
	d.Revision = "commit-2"
	if _, err := h.PrepareDeployment(context.Background(), d, func(cluster.DeploymentStatus) {}); err == nil {
		t.Fatal("mismatched revision reused")
	}
}

func TestDeploymentLANToUnifiedGateway(t *testing.T) {
	useIsolatedStorageHome(t)
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)
	bus := cluster.NewMemoryBus()
	master := newClusterTestServer(t, bus)
	worker := newClusterTestServer(t, bus)
	_, token, err := master.cluster.CreateCluster("deployments")
	if err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "worker discovery", func() bool { return len(worker.cluster.DiscoveredNodes()) == 1 })
	if _, err := worker.cluster.Join(context.Background(), token, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.cluster.UpdateSettings(func(s *cluster.Settings) {
		s.ManagedBy = master.cluster.Identity().UUID
		s.AcceptWork = false // Prepare the fixture before allowing execution.
	}); err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "management authorization", func() bool {
		rt, ok := master.cluster.Directory().Get(worker.cluster.Identity().UUID)
		return ok && rt.Status != nil && rt.Status.ManagedBy == master.cluster.Identity().UUID
	})
	engine := &fakeChatCompletionEngine{resp: api.OpenAIChatResponse{ID: "cmpl-managed", Object: "chat.completion", Choices: []api.OpenAIChoice{{Index: 0, Message: &api.Message{Role: "assistant", Content: "managed worker reply"}}}}}
	old := loadEngineWithProgress
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		return engine, nil
	}
	t.Cleanup(func() { worker.cluster.Stop(); loadEngineWithProgress = old })
	routes := master.routes()
	rec := httptest.NewRecorder()
	body := `{"name":"managed-test","node_uuid":"` + worker.cluster.Identity().UUID + `","repository":"acme/test","revision":"commit-1"}`
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cluster/deployments", strings.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	var d cluster.Deployment
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	// Seed the artifact cache; this test exercises the real controller, mTLS,
	// runtime adapter and gateway, without fetching gigabytes of weights.
	base := filepath.Join(worker.cfg.StorageDir(), "cluster", "deployment-models", d.ID)
	lm := &model.LocalModel{Namespace: "acme", Name: "test", Repository: "acme/test", Format: model.FormatGGUF, Size: 4, Files: []string{"model.gguf"}, ArtifactSource: "opencsg", RequestedRevision: "commit-1", PipelineTag: "text-generation"}
	if err := model.SaveManifest(base, lm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "acme", "test", "model.gguf"), []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.cluster.UpdateSettings(func(s *cluster.Settings) { s.AcceptWork = true }); err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "ready deployment on worker", func() bool { return worker.cluster.DeploymentReady(d.ModelID()) })
	clusterWait(t, "deployment inventory on master", func() bool { return master.cluster.RemoteHasModel(d.ModelID()) })
	rec = httptest.NewRecorder()
	body = `{"name":"managed-service","model":"managed-service","policy":"fixed","category":"chat","members":[{"id":"managed","source":"node:` + worker.cluster.Identity().UUID + `","model":"` + d.ModelID() + `"}]}`
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/provider-pools", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ai-gateway/v1/chat/completions", strings.NewReader(`{"model":"managed-service","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "managed worker reply") {
		t.Fatalf("gateway %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/cluster/deployments/"+d.ID, strings.NewReader(`{"desired":"stopped"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("stop %d %s", rec.Code, rec.Body)
	}
	clusterWait(t, "stop receipt", func() bool { return !worker.cluster.DeploymentReady(d.ModelID()) })
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ai-gateway/v1/chat/completions", strings.NewReader(`{"model":"managed-service","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code == http.StatusOK {
		t.Fatal("gateway restarted a stopped deployment")
	}
}

func TestNativeDecisionDeploymentLANToUnifiedGateway(t *testing.T) {
	useIsolatedStorageHome(t)
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviderPools)
	bus := cluster.NewMemoryBus()
	master := newClusterTestServer(t, bus)
	worker := newClusterTestServer(t, bus)
	_, token, err := master.cluster.CreateCluster("deployments")
	if err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "worker discovery", func() bool { return len(worker.cluster.DiscoveredNodes()) == 1 })
	if _, err := worker.cluster.Join(context.Background(), token, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.cluster.UpdateSettings(func(s *cluster.Settings) {
		s.ManagedBy = master.cluster.Identity().UUID
		s.AcceptWork = false // Prepare the fixture before allowing execution.
	}); err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "management authorization", func() bool {
		rt, ok := master.cluster.Directory().Get(worker.cluster.Identity().UUID)
		return ok && rt.Status != nil && rt.Status.ManagedBy == master.cluster.Identity().UUID
	})
	engine := &healthSystemOneEngine{}
	old := loadEngineWithProgress
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		return engine, nil
	}
	t.Cleanup(func() { worker.cluster.Stop(); loadEngineWithProgress = old })
	routes := master.routes()
	rec := httptest.NewRecorder()
	body := `{"name":"managed-test","node_uuid":"` + worker.cluster.Identity().UUID + `","repository":"acme/test","revision":"commit-1"}`
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cluster/deployments", strings.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	var d cluster.Deployment
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	// Seed the artifact cache; this test exercises the real controller, mTLS,
	// runtime adapter and gateway, without fetching gigabytes of weights.
	base := filepath.Join(worker.cfg.StorageDir(), "cluster", "deployment-models", d.ID)
	lm := &model.LocalModel{Namespace: "acme", Name: "test", Repository: "acme/test", Format: model.FormatSafeTensors, Size: 4, Files: []string{"model.safetensors"}, ArtifactSource: "opencsg", RequestedRevision: "commit-1", PipelineTag: "text-generation"}
	if err := model.SaveManifest(base, lm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "acme", "test", "model.safetensors"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"config.json":          `{"model_type":"laya","architectures":["LayaTypedDecisions"]}`,
		"encoder/config.json":  `{"architectures":["ModernBertModel"]}`,
		"rl_agent_config.json": `{"head_layers":2}`,
		"README.md":            "---\ntags:\n- typed-decisions\n---\n",
	} {
		path := filepath.Join(base, "acme", "test", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := worker.cluster.UpdateSettings(func(s *cluster.Settings) { s.AcceptWork = true }); err != nil {
		t.Fatal(err)
	}
	clusterWait(t, "ready deployment on worker", func() bool { return worker.cluster.DeploymentReady(d.ModelID()) })
	clusterWait(t, "deployment inventory on master", func() bool { return master.cluster.RemoteHasModel(d.ModelID()) })
	rec = httptest.NewRecorder()
	body = `{"name":"managed-service","model":"managed-service","policy":"fixed","category":"chat","members":[{"id":"managed","source":"node:` + worker.cluster.Identity().UUID + `","model":"` + d.ModelID() + `"}]}`
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/provider-pools", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish %d %s", rec.Code, rec.Body)
	}
	var service api.ProviderPool
	if err := json.Unmarshal(rec.Body.Bytes(), &service); err != nil || service.Category != "decision" || !service.DecisionOnly || len(service.InferenceProtocols) != 1 || service.InferenceProtocols[0] != "decisions" {
		t.Fatalf("lost native model capabilities: %s %v", rec.Body, err)
	}
	if lm, err := worker.resolveDecisionModel(d.ModelID()); err != nil || lm.Repository != d.Repository {
		t.Fatal("managed decision alias did not resolve", lm, err)
	}
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ai-gateway/v1/decisions", strings.NewReader(`{"model":"managed-service","input":"I need a refund.","questions":[{"type":"predicate","name":"refund","instructions":"Refund requested?"}]}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"probability":0.9`) {
		t.Fatalf("gateway %d %s", rec.Code, rec.Body)
	}
	engine.broken.Store(true)
	clusterWait(t, "runtime failure on worker", func() bool {
		for _, receipt := range worker.cluster.LocalDeployments() {
			if receipt.ID == d.ID {
				return receipt.Status.Phase == "failed" && strings.Contains(receipt.Status.Error, "insufficient memory")
			}
		}
		return false
	})
	clusterWait(t, "runtime failure receipt on Master", func() bool {
		status := httptest.NewRecorder()
		routes.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/cluster/deployments", nil))
		var view struct {
			Deployments []cluster.Deployment `json:"deployments"`
		}
		if json.Unmarshal(status.Body.Bytes(), &view) != nil {
			return false
		}
		for _, receipt := range view.Deployments {
			if receipt.ID == d.ID {
				return receipt.Status.Phase == "failed" && strings.Contains(receipt.Status.Error, "insufficient memory")
			}
		}
		return false
	})
	clusterWait(t, "failed model removed from routing", func() bool { return !master.cluster.RemoteHasModel(d.ModelID()) })
	engine.broken.Store(false)
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/cluster/deployments/"+d.ID, strings.NewReader(`{"desired":"running"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry %d %s", rec.Code, rec.Body)
	}
	clusterWait(t, "explicit retry recovers runtime", func() bool { return worker.cluster.DeploymentReady(d.ModelID()) })
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/cluster/deployments/"+d.ID, strings.NewReader(`{"desired":"stopped"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("stop %d %s", rec.Code, rec.Body)
	}
	clusterWait(t, "stop receipt", func() bool { return !worker.cluster.DeploymentReady(d.ModelID()) })
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ai-gateway/v1/decisions", strings.NewReader(`{"model":"managed-service","input":"I need a refund.","questions":[{"type":"predicate","name":"refund","instructions":"Refund requested?"}]}`)))
	if rec.Code == http.StatusOK {
		t.Fatal("gateway restarted a stopped deployment")
	}
}

func TestDeploymentCannotBeStoppedThroughLocalModelAPI(t *testing.T) {
	s, _, d := deploymentRuntimeFixture(t)
	engine := &countedDeploymentEngine{}
	s.engines[engineCacheKey(d.ModelID(), engineModeChat)] = &managedEngine{engine: engine, keepAlive: -1}
	for _, path := range []string{"/api/stop", "/api/load"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"`+d.ModelID()+`"}`))
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || engine.closed.Load() != 0 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestDeleteDeploymentRemovesSharedArtifactWhenUnused(t *testing.T) {
	s, h, d := deploymentRuntimeFixture(t)
	other := filepath.Join(s.cfg.StorageDir(), "cluster", "deployment-models", uuid.NewString())
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	s.engines[d.ModelID()] = &managedEngine{engine: &countedDeploymentEngine{}}
	if _, err := h.DeleteDeployment(d); err == nil {
		t.Fatal("deleted a loaded deployment")
	}
	if err := h.StopDeployment(d); err != nil {
		t.Fatal(err)
	}
	if _, err := h.DeleteDeployment(d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.manager.Get(deploymentArtifactID(d)); !os.IsNotExist(err) {
		t.Fatal("shared cache retained", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("another deployment removed", err)
	}
	d.ID = "../.."
	if _, err := h.DeleteDeployment(d); err == nil {
		t.Fatal("unsafe cache path accepted")
	}
}

func TestRemoteModelActionsRespectIdentityAndBusyRequests(t *testing.T) {
	s := newTestServer(t)
	h := &clusterHost{s: s}
	lm := &model.LocalModel{Namespace: "acme", Name: "test", Repository: "acme/test", ArtifactSource: "opencsg", Format: model.FormatGGUF, Files: []string{"model.gguf"}}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatal(err)
	}
	engine := &countedDeploymentEngine{}
	s.engines[lm.FullName()] = &managedEngine{engine: engine, activeRequests: 1}
	action := cluster.ModelAction{Model: s.localInferenceModelID(lm.FullName()), Repository: lm.Repository, ArtifactSource: "opencsg", Action: "delete"}
	if err := h.ManageLocalModel(context.Background(), action); err == nil || engine.closed.Load() != 0 {
		t.Fatal("busy request terminated")
	}
	s.engines[lm.FullName()].activeRequests = 0
	wrong := action
	wrong.ArtifactSource = "huggingface"
	if err := h.ManageLocalModel(context.Background(), wrong); err == nil {
		t.Fatal("different registry artifact removed")
	}
	action.Action = "unload"
	if err := h.ManageLocalModel(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if _, err := s.manager.Get(lm.FullName()); err != nil {
		t.Fatal("unload deleted files")
	}
	action.Action = "delete"
	if err := h.ManageLocalModel(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if _, err := s.manager.Get(lm.FullName()); err == nil {
		t.Fatal("delete retained files")
	}
}

type blockingLifecycleImageEngine struct {
	fakeImageEngine
	started chan struct{}
	finish  chan struct{}
}

func (e *blockingLifecycleImageEngine) Generate(ctx context.Context, req api.OpenAIImagesGenerationRequest) (*api.OpenAIImagesGenerationResponse, error) {
	close(e.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.finish:
		return &api.OpenAIImagesGenerationResponse{}, nil
	}
}
func TestRemoteImageUnloadWaitsForGeneration(t *testing.T) {
	s := newTestServer(t)
	h := &clusterHost{s: s}
	lm := &model.LocalModel{Namespace: "acme", Name: "image", Repository: "acme/image", ArtifactSource: "opencsg", PipelineTag: "text-to-image"}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatal(err)
	}
	engine := &blockingLifecycleImageEngine{started: make(chan struct{}), finish: make(chan struct{})}
	s.imageEngines[lm.FullName()] = &managedImageEngine{engine: engine}
	done := make(chan error, 1)
	go func() {
		_, err := s.generateLocalImage(context.Background(), engine, api.OpenAIImagesGenerationRequest{Model: lm.FullName()})
		done <- err
	}()
	<-engine.started
	action := cluster.ModelAction{Action: "unload", Model: lm.FullName(), Repository: lm.Repository, ArtifactSource: "opencsg"}
	if err := h.ManageLocalModel(context.Background(), action); err == nil {
		t.Fatal("unloaded running image request")
	}
	s.evictExpired(time.Now())
	s.mu.RLock()
	present := s.imageEngines[lm.FullName()] != nil
	s.mu.RUnlock()
	if !present {
		t.Fatal("evicted image engine mid-generation")
	}
	close(engine.finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := h.ManageLocalModel(context.Background(), action); err != nil {
		t.Fatal(err)
	}
}

func reopenWithDeploymentReceipts(t *testing.T, s *Server, assignments []cluster.Deployment) {
	t.Helper()
	if s.cluster == nil {
		t.Fatal("missing cluster manager")
	}
	s.cluster.Stop()
	receipts := map[string]cluster.Deployment{}
	for _, d := range assignments {
		receipts[d.ID] = d
	}
	payload, err := json.Marshal(map[string]any{"version": 1, "owned": map[string]cluster.Deployment{}, "assignments": receipts})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.cfg.StorageDir(), "cluster", "deployments.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	s.cluster = newClusterManager(s, s.cfg.StorageDir())
	if s.cluster == nil {
		t.Fatal("failed to reopen deployment receipts")
	}
}

func TestStoppedDeploymentMigratesToConfiguredLibraryAtStartup(t *testing.T) {
	s := newTestServer(t)
	d := cluster.Deployment{ID: uuid.NewString(), ControllerUUID: s.cluster.Identity().UUID, Generation: 2, Desired: "stopped", Status: cluster.DeploymentStatus{Phase: "stopped"}, DeploymentSpec: cluster.DeploymentSpec{Name: "migrate", NodeUUID: s.cluster.Identity().UUID, Repository: "acme/test", ArtifactSource: "huggingface", Revision: "main", Kind: "chat"}}
	legacyRoot := filepath.Join(s.cfg.StorageDir(), "cluster", "deployment-models", d.ID)
	legacyDir := model.RegistryModelDir(legacyRoot, "huggingface", "acme", "test")
	lm := &model.LocalModel{Namespace: "acme", Name: "test", Repository: "acme/test", ArtifactSource: "huggingface", RequestedRevision: "main", ResolvedRevision: "sha-one", Format: model.FormatGGUF, Size: 4, Files: []string{"model.gguf"}, RemovedSourceWeights: []string{"model.safetensors"}}
	if err := model.SaveManifestInDir(legacyDir, lm); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(legacyDir, "model.gguf")
	if err := os.WriteFile(oldFile, []byte("GGUF"), 0644); err != nil {
		t.Fatal(err)
	}
	// On Windows, os.Stat loads the file ID lazily through its original path.
	// Read it through a handle before migration, then close the handle so the
	// directory can be renamed and SameFile never needs the removed old path.
	originalFile, err := os.Open(oldFile)
	if err != nil {
		t.Fatal(err)
	}
	before, statErr := originalFile.Stat()
	closeErr := originalFile.Close()
	if statErr != nil {
		t.Fatal(statErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	reopenWithDeploymentReceipts(t, s, []cluster.Deployment{d})
	dir, err := s.manager.ModelPath(lm.FullName())
	if err != nil {
		t.Fatal(err)
	}
	if dir != model.RegistryModelDir(s.cfg.ModelDir, "huggingface", "acme", "test") {
		t.Fatal("migration ignored configured ModelDir", dir)
	}
	after, err := os.Stat(filepath.Join(dir, "model.gguf"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("same-filesystem migration copied or lost weights", err)
	}
	weights, err := os.ReadFile(filepath.Join(dir, "model.gguf"))
	if err != nil || string(weights) != "GGUF" {
		t.Fatal("migration changed weight contents", err)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatal("legacy cache directory retained", err)
	}
	h := &clusterHost{s: s}
	if size, err := h.PrepareDeployment(context.Background(), d, func(cluster.DeploymentStatus) {}); err != nil || size != 4 {
		t.Fatal("cached deployment did not reuse library", size, err)
	}
	listed, err := s.manager.List()
	if err != nil || len(listed) != 1 || listed[0].FullName() != lm.FullName() {
		t.Fatal("migrated model missing from ordinary library", err)
	}
	rec := httptest.NewRecorder()
	s.handleModelManifestForID(rec, lm.FullName())
	if rec.Code != 200 {
		t.Fatalf("ordinary model details unavailable: %d %s", rec.Code, rec.Body)
	}
}

func TestSharedModelDeletionPreservesOtherDeploymentAndRuntime(t *testing.T) {
	s, h, d := deploymentRuntimeFixture(t)
	d.NodeUUID = s.cluster.Identity().UUID
	d.ControllerUUID = d.NodeUUID
	d.Desired = "stopped"
	d.Status = cluster.DeploymentStatus{Phase: "stopped"}
	other := d
	other.ID = uuid.NewString()
	other.Name = "second"
	reopenWithDeploymentReceipts(t, s, []cluster.Deployment{d, other})
	for _, item := range h.LocalStatus(context.Background()).Models {
		if item.ID == s.localInferenceModelID(deploymentArtifactID(d)) {
			t.Fatal("managed weights advertised as an independently routable cache model")
		}
	}
	detail, err := h.DeleteDeployment(d)
	if err != nil || detail == "" {
		t.Fatal("shared weights were not retained", detail, err)
	}
	if _, err := s.manager.Get(deploymentArtifactID(d)); err != nil {
		t.Fatal("other deployment lost weights", err)
	}
	rec := httptest.NewRecorder()
	s.handleDelete(rec, httptest.NewRequest(http.MethodDelete, "/api/delete", strings.NewReader(`{"id":"acme/test"}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("local deletion bypassed deployment reference: %d %s", rec.Code, rec.Body)
	}
	other.Desired = "deleted"
	other.Status.Phase = "deleted"
	reopenWithDeploymentReceipts(t, s, []cluster.Deployment{d, other})
	engine := &countedDeploymentEngine{}
	s.engines["acme/test"] = &managedEngine{engine: engine}
	if _, err := h.DeleteDeployment(d); err == nil {
		t.Fatal("deleted files used by local runtime")
	}
	if engine.closed.Load() != 0 {
		t.Fatal("local runtime was stopped by another deployment")
	}
	delete(s.engines, "acme/test")
	if _, err := h.DeleteDeployment(d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.manager.Get(deploymentArtifactID(d)); !os.IsNotExist(err) {
		t.Fatal("unreferenced artifact not removed", err)
	}
}
