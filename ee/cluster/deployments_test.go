// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type deploymentTestHost struct {
	*fakeHost
	runtimeMu   sync.Mutex
	running     map[string]bool
	lastStarted Deployment
	starts      atomic.Int32
	prepares    atomic.Int32
	deletes     atomic.Int32
	actions     atomic.Int32
	block       chan struct{}
	fail        atomic.Bool
	healthErr   error // guarded by runtimeMu
	engines     []EngineStatus
}

func (h *deploymentTestHost) Engines() []EngineStatus { return h.engines }

func (h *deploymentTestHost) DeploymentHealth(Deployment) error {
	h.runtimeMu.Lock()
	defer h.runtimeMu.Unlock()
	return h.healthErr
}

func (h *deploymentTestHost) PrepareDeployment(ctx context.Context, d Deployment, report func(DeploymentStatus)) (int64, error) {
	h.prepares.Add(1)
	if h.block != nil {
		select {
		case <-h.block:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if h.fail.Load() {
		return 0, errors.New("test download failure")
	}
	report(DeploymentStatus{Phase: "downloading", CompletedBytes: 1024, TotalBytes: 1024})
	return 1024, nil
}
func (h *deploymentTestHost) StartDeployment(ctx context.Context, d Deployment, _ func(DeploymentStatus)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.runtimeMu.Lock()
	defer h.runtimeMu.Unlock()
	h.running[d.ID] = true
	h.lastStarted = d
	h.starts.Add(1)
	return nil
}
func (h *deploymentTestHost) StopDeployment(d Deployment) error {
	h.runtimeMu.Lock()
	defer h.runtimeMu.Unlock()
	delete(h.running, d.ID)
	return nil
}
func (h *deploymentTestHost) DeploymentRunning(d Deployment) bool {
	h.runtimeMu.Lock()
	defer h.runtimeMu.Unlock()
	return h.running[d.ID]
}

func deploymentTestNode(t *testing.T) (*Manager, *deploymentTestHost) {
	t.Helper()
	h := &deploymentTestHost{fakeHost: &fakeHost{name: "worker", limit: 0}, running: map[string]bool{}}
	m, err := New(Options{Dir: t.TempDir(), Host: h, Discoverer: NopDiscoverer{}, ListenAddr: "127.0.0.1:0", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	if _, _, err := m.CreateCluster("deployments"); err != nil {
		t.Fatal(err)
	}
	return m, h
}
func testDeployment(m *Manager) Deployment {
	return Deployment{ID: uuid.NewString(), ControllerUUID: m.identity.UUID, Generation: 1, Desired: "running", CreatedAt: time.Now().UTC(), DeploymentSpec: DeploymentSpec{Name: "Qwen", NodeUUID: m.identity.UUID, Repository: "Qwen/demo", ArtifactSource: "opencsg", Revision: "commit-123", Kind: "chat", NumCtx: 4096, NumParallel: 1, NGPULayers: -1}}
}
func waitDeployment(t *testing.T, m *Manager, id, phase string) {
	t.Helper()
	waitPublic(t, func() bool { d, ok := m.deployments.get(false, id); return ok && d.Status.Phase == phase })
}

func TestDeploymentConsentGenerationAndPersistence(t *testing.T) {
	m, h := deploymentTestNode(t)
	d := testDeployment(m)
	controller := uuid.NewString()
	_, err := m.store.Upsert(Member{UUID: controller, Name: "controller", CertFingerprint: "test-fingerprint"}, true)
	if err != nil {
		t.Fatal(err)
	}
	d.ControllerUUID = controller
	if _, err := m.receiveDeployment(controller, d); err == nil {
		t.Fatal("unmanaged worker accepted assignment")
	}
	_, err = m.UpdateSettings(func(s *Settings) { s.ManagedBy = controller })
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(m.opts.Dir)
	if err != nil || reopened.Settings().ManagedBy != controller {
		t.Fatal("management consent did not persist", err)
	}
	if _, err := m.receiveDeployment(controller, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "ready")
	if !m.DeploymentReady(d.ModelID()) {
		t.Fatal("ready deployment not available")
	}
	for range 3 {
		if _, err := m.receiveDeployment(controller, d); err != nil {
			t.Fatal(err)
		}
	}
	if h.starts.Load() != 1 {
		t.Fatal("idempotent assignment started duplicate runtime")
	}
	conflict := d
	conflict.Desired = "stopped"
	if _, err := m.receiveDeployment(controller, conflict); err == nil {
		t.Fatal("same-generation conflict accepted")
	}
	conflict = d
	conflict.Generation++
	conflict.Revision = "different"
	if _, err := m.receiveDeployment(controller, conflict); err == nil {
		t.Fatal("revision changed in place")
	}
	d.Generation++
	d.Desired = "stopped"
	if _, err := m.receiveDeployment(controller, d); err != nil {
		t.Fatal(err)
	}
	if m.DeploymentReady(d.ModelID()) {
		t.Fatal("stop did not immediately fence inference")
	}
	waitDeployment(t, m, d.ID, "stopped")
	old := d
	old.Generation--
	old.Desired = "running"
	if _, err := m.receiveDeployment(controller, old); err == nil {
		t.Fatal("stale assignment restarted stopped deployment")
	}
	store, err := openDeploymentStore(m.opts.Dir)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := store.get(false, d.ID)
	if !ok || receipt.Generation != 2 || receipt.Status.Phase != "stopped" {
		t.Fatalf("receipt was not durable: %+v", receipt)
	}
	_, err = m.UpdateSettings(func(s *Settings) { s.ManagedBy = "" })
	if err != nil {
		t.Fatal(err)
	}
	d.Generation++
	d.Desired = "running"
	if _, err := m.receiveDeployment(controller, d); err == nil {
		t.Fatal("revoked controller retained authority")
	}
}

func TestDeploymentStopCancelsDownloadAndRetryIsExplicit(t *testing.T) {
	m, h := deploymentTestNode(t)
	h.block = make(chan struct{})
	d := testDeployment(m)
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { return h.prepares.Load() == 1 })
	d.Generation++
	d.Desired = "stopped"
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "stopped")
	if h.starts.Load() != 0 {
		t.Fatal("cancelled download started a runtime")
	}
	close(h.block)
	h.fail.Store(true)
	d.Generation++
	d.Desired = "running"
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "failed")
	prepares := h.prepares.Load()
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if h.prepares.Load() != prepares {
		t.Fatal("failed deployment retried without a new generation")
	}
	h.fail.Store(false)
	d.Generation++
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "ready")
}

func TestDeploymentRuntimeFailureIsDurableAndNeedsExplicitRetry(t *testing.T) {
	m, h := deploymentTestNode(t)
	d := testDeployment(m)
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "ready")
	h.runtimeMu.Lock()
	h.healthErr = errors.New("insufficient memory: compute backend failed")
	h.runtimeMu.Unlock()
	if m.DeploymentReady(d.ModelID()) {
		t.Fatal("a poisoned runtime still accepted inference before the next receipt")
	}
	waitDeployment(t, m, d.ID, "failed")
	waitPublic(t, func() bool { return !h.DeploymentRunning(d) })
	receipt, _ := m.deployments.get(false, d.ID)
	if !strings.Contains(receipt.Status.Error, "insufficient memory") {
		t.Fatal("runtime failure reason was lost", receipt.Status)
	}
	if len(m.deploymentModels()) != 0 {
		t.Fatal("failed deployment was still advertised for routing")
	}
	store, err := openDeploymentStore(m.opts.Dir)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := store.get(false, d.ID)
	if persisted.Status.Phase != "failed" || persisted.Status.Error != receipt.Status.Error {
		t.Fatal("runtime failure was not durable")
	}
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if h.starts.Load() != 1 {
		t.Fatal("failed runtime automatically reloaded the same generation")
	}
	h.runtimeMu.Lock()
	h.healthErr = nil
	h.runtimeMu.Unlock()
	d.Generation++
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "ready")
	if h.starts.Load() != 2 || !m.DeploymentReady(d.ModelID()) {
		t.Fatal("an explicit retry did not recover the deployment")
	}
}

func TestDeploymentInventoryKeepsSameRepositoryInstancesDistinct(t *testing.T) {
	m, _ := deploymentTestNode(t)
	one := testDeployment(m)
	two := testDeployment(m)
	two.Name = "second"
	for _, d := range []Deployment{one, two} {
		if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
			t.Fatal(err)
		}
		waitDeployment(t, m, d.ID, "ready")
	}
	models := m.Models(context.Background())
	if len(models) != 2 || models[0].ID == models[1].ID {
		t.Fatalf("deployment replicas collapsed by repository: %+v", models)
	}
}

func TestDeploymentValidationRejectsUnsafeArtifactsAndLimits(t *testing.T) {
	m, _ := deploymentTestNode(t)
	base := testDeployment(m).DeploymentSpec
	for _, mutate := range []func(*DeploymentSpec){
		func(d *DeploymentSpec) { d.Repository = "../model" }, func(d *DeploymentSpec) { d.Repository = "a/../../b" },
		func(d *DeploymentSpec) { d.ArtifactSource = "http://untrusted" }, func(d *DeploymentSpec) { d.NumCtx = -1 },
		func(d *DeploymentSpec) { d.NumParallel = 999 }, func(d *DeploymentSpec) { d.Quant = "../../weights" },
		func(d *DeploymentSpec) { d.Revision = "" }, func(d *DeploymentSpec) { d.NodeUUID = "../../node" },
	} {
		d := base
		mutate(&d)
		if validateDeployment(d) == nil {
			t.Fatalf("accepted invalid spec: %+v", d)
		}
	}
}

func TestDeploymentRestartRecoversOwnedAndAssignedState(t *testing.T) {
	m, h := deploymentTestNode(t)
	if m.store.Settings().ManagedBy != "" {
		t.Fatal("legacy/default node became managed")
	}
	d := testDeployment(m)
	if err := m.deployments.update(true, d.ID, func(record *Deployment, _ bool) error { *record = d; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "ready")
	m.Stop()
	if h.DeploymentRunning(d) {
		t.Fatal("shutdown leaked runtime")
	}
	restarted, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	if owned, ok := restarted.deployments.get(true, d.ID); !ok || owned.Generation != d.Generation || owned.DeploymentSpec != d.DeploymentSpec {
		t.Fatal("controller desired state lost on restart")
	}
	if restarted.DeploymentReady(d.ModelID()) {
		t.Fatal("disk receipt advertised a nonexistent runtime")
	}
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	waitDeployment(t, restarted, d.ID, "ready")
	if h.starts.Load() != 2 {
		t.Fatal("restart did not recover runtime exactly once")
	}
}

func TestDeploymentAcrossPublicTunnelJoinControlAndLeave(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	master, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{name: "master", limit: 2}, Mode: "gateway", PublicListenAddr: "127.0.0.1:0", PublicInference: http.NotFoundHandler(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if err = master.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer master.Stop()
	_, token, err := master.CreateCluster("deployment-test")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(master.publicRoutes(master.publicHub()))
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	host := &leaveDeploymentHost{deploymentTestHost: &deploymentTestHost{fakeHost: &fakeHost{name: "worker", limit: 2}, running: map[string]bool{}}}
	worker, err := New(Options{Dir: t.TempDir(), Host: host, Mode: "worker", GatewayURL: server.URL, TLSCAFile: caFile,
		ListenAddr: "127.0.0.1:0", Discoverer: NopDiscoverer{}, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer worker.Stop()
	if _, err = worker.Join(ctx, token, server.URL); err != nil {
		t.Fatal(err)
	}
	refresh := func() bool {
		st, addr, e := master.publicStatus(worker.identity.UUID)
		if e != nil {
			return false
		}
		master.dir.MarkSuccess(worker.identity.UUID, addr, st)
		return true
	}
	waitPublic(t, refresh)
	spec := testDeployment(worker).DeploymentSpec
	// Joining alone establishes control; no managed_by setting is required.
	if worker.store.Settings().ManagedBy != "" || !worker.deploymentAuthorized(master.identity.UUID) {
		t.Fatal("membership did not establish Master control")
	}
	assignment := testDeployment(worker)
	assignment.ControllerUUID = uuid.NewString()
	raw, _ := json.Marshal(assignment)
	response, err := master.publicHub().AssignDeployment(ctx, worker.identity.UUID, raw)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatal("tunnel accepted an assignment from another controller", response.StatusCode)
	}
	waitPublic(t, func() bool {
		return refresh() && master.candidateStatus(worker.identity.UUID).ManagedBy == master.identity.UUID
	})
	d, err := master.createDeployment(spec)
	if err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, worker, d.ID, "ready")
	waitPublic(t, func() bool { return refresh() && master.RemoteHasModel(d.ModelID()) })
	resp, err := master.RouteRaw(ctx, d.ModelID(), "cluster", "/v1/chat/completions", []byte(`{"model":"`+d.ModelID()+`"}`), http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "worker") {
		t.Fatalf("deployment inference not routed: %s", body)
	}
	if worker.ListenPort() != 0 {
		t.Fatal("worker opened an inbound listener")
	}
	// A legacy attempt to clear authorization cannot opt out of the Master.
	if _, err = worker.UpdateSettings(func(s *Settings) { s.ManagedBy = "" }); err != nil {
		t.Fatal(err)
	}
	if !worker.DeploymentReady(d.ModelID()) {
		t.Fatal("legacy authorization clearing revoked membership control")
	}
	if _, err = worker.UpdateSettings(func(s *Settings) { s.State = NodeStateMaintenance }); err != nil {
		t.Fatal(err)
	}
	if worker.DeploymentReady(d.ModelID()) || !worker.deploymentAuthorized(master.identity.UUID) {
		t.Fatal("maintenance must pause work without revoking Master control")
	}
	waitDeployment(t, worker, d.ID, "blocked")
	if host.DeploymentRunning(d) {
		t.Fatal("maintenance deployment is still running")
	}
	if _, err = worker.UpdateSettings(func(s *Settings) { s.State = NodeStateActive }); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, worker, d.ID, "ready")

	waitPublic(t, func() bool { return refresh() && master.candidateStatus(worker.identity.UUID).ModelManagement })
	if err := master.sendModelAction(ctx, worker.identity.UUID, ModelAction{Action: "unload", Model: "local-qwen", Repository: "Qwen/demo", ArtifactSource: "opencsg"}); err != nil {
		t.Fatal(err)
	}
	if host.actions.Load() != 1 {
		t.Fatal("model action did not reach worker")
	}
	forbidden := httptest.NewRequest("POST", "http://worker/cluster/v1/models/action", strings.NewReader(`{"model":"local-qwen"}`))
	if _, err := master.publicHub().RoundTrip(worker.identity.UUID, forbidden); err == nil {
		t.Fatal("inference proxy accepted model management")
	}
	spec.Name = "delete-me"
	removed, err := master.createDeployment(spec)
	if err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, worker, removed.ID, "ready")
	if _, err := master.changeDeployment(removed.ID, "deleted"); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, worker, removed.ID, "deleted")
	if host.DeploymentRunning(removed) || host.deletes.Load() != 1 {
		t.Fatal("tunnel delete did not remove drained deployment")
	}
	// The private control path is not admitted by the public inference transport.
	req := httptest.NewRequest("POST", "http://worker"+deploymentPeerPath, strings.NewReader(string(raw)))
	if _, err = master.publicHub().RoundTrip(worker.identity.UUID, req); err == nil {
		t.Fatal("inference transport accepted management path")
	}
	host.drainBusy.Store(true)
	if err := worker.Leave(ctx); err != nil {
		t.Fatal(err)
	}
	if host.DeploymentRunning(d) || host.unloads.Load() == 0 {
		t.Fatal("public leave returned before unloading its busy managed runtime")
	}
	if worker.deploymentAuthorized(master.identity.UUID) || worker.DeploymentReady(d.ModelID()) || worker.LocalStatus(ctx).ManagedBy != "" {
		t.Fatal("leaving retained Master control")
	}
	waitDeployment(t, worker, d.ID, "blocked")
	left := worker.View(ctx)
	if left.Mode != "" || left.GatewayURL != "" || left.Cluster != nil || left.Active {
		t.Fatalf("departed worker did not return to standalone mode: mode=%q, joined=%v, active=%v", left.Mode, left.Cluster != nil, left.Active)
	}
	info, _, err := worker.CreateCluster("standalone")
	if err != nil || info.Topology != "" {
		t.Fatalf("former worker could not create a LAN cluster: %v", err)
	}
}

func (h *deploymentTestHost) DeleteDeployment(d Deployment) (string, error) {
	if h.DeploymentRunning(d) {
		return "", errors.New("runtime not drained")
	}
	h.deletes.Add(1)
	return "", nil
}
func (h *deploymentTestHost) ManageLocalModel(_ context.Context, action ModelAction) error {
	h.actions.Add(1)
	return nil
}

func TestDeploymentDeleteCancelsPreparationAndFencesResurrection(t *testing.T) {
	m, h := deploymentTestNode(t)
	h.block = make(chan struct{})
	d := testDeployment(m)
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { return h.prepares.Load() == 1 })
	d.Generation++
	d.Desired = "deleted"
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "deleted")
	if h.starts.Load() != 0 || h.deletes.Load() != 1 {
		t.Fatal("delete raced preparation or ran twice")
	}
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	if m.DeploymentReady(d.ModelID()) {
		t.Fatal("deleted runtime remained routable")
	}
	reopened, err := openDeploymentStore(m.opts.Dir)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := reopened.get(false, d.ID)
	if receipt.Status.Phase != "deleted" {
		t.Fatal("delete receipt lost on restart")
	}
	d.Generation++
	d.Desired = "running"
	if _, err := m.receiveDeployment(m.identity.UUID, d); err == nil {
		t.Fatal("deleted ID was resurrected")
	}
	if err := m.performModelAction(context.Background(), "unknown-controller", ModelAction{Action: "delete", Model: "m", Repository: "org/m", ArtifactSource: "opencsg"}); err == nil || h.actions.Load() != 0 {
		t.Fatal("unauthorized model deletion accepted")
	}
}

func TestDeploymentDeleteAndFilteredHistoryPreserveTombstone(t *testing.T) {
	m, _ := deploymentTestNode(t)
	d := testDeployment(m)
	if err := m.deployments.update(true, d.ID, func(record *Deployment, _ bool) error { *record = d; return nil }); err != nil {
		t.Fatal(err)
	}
	deleteRequest := func() Deployment {
		r := httptest.NewRequest(http.MethodDelete, "/api/cluster/deployments/"+d.ID, nil)
		r.SetPathValue("id", d.ID)
		w := httptest.NewRecorder()
		m.Admin().HandleDeploymentDelete(w, r)
		if w.Code != http.StatusAccepted {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
		var result Deployment
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	deleted := deleteRequest()
	if deleted.Desired != "deleted" {
		t.Fatal("delete did not set durable intent")
	}
	if again := deleteRequest(); again.Generation != deleted.Generation {
		t.Fatal("repeated deletion changed generation")
	}
	// Pending cleanup remains visible; only an acknowledged deletion is hidden.
	for _, phase := range []string{"pending", "deleted"} {
		if err := m.deployments.update(true, d.ID, func(record *Deployment, _ bool) error { record.Status.Phase = phase; return nil }); err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{"", "?include_deleted=true", "?include_deleted=false"} {
			w := httptest.NewRecorder()
			m.Admin().HandleDeployments(w, httptest.NewRequest(http.MethodGet, "/api/cluster/deployments"+query, nil))
			var body struct {
				Deployments []Deployment `json:"deployments"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			hidden := phase == "deleted" && query == "?include_deleted=false"
			if (len(body.Deployments) == 0) != hidden {
				t.Fatalf("phase=%s query=%s: %s", phase, query, w.Body.String())
			}
		}
	}
	reopened, err := openDeploymentStore(m.opts.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if record, ok := reopened.get(true, d.ID); !ok || record.Desired != "deleted" {
		t.Fatal("listing removed the durable tombstone")
	}
	if _, err := m.changeDeployment(d.ID, "running"); err == nil {
		t.Fatal("deleted deployment restarted")
	}
}

type idleDeploymentTestHost struct {
	*deploymentTestHost
	expire atomic.Bool
}

func (h *idleDeploymentTestHost) ExpireDeployment(d Deployment, _ time.Time) (bool, error) {
	if !h.expire.Load() {
		return false, nil
	}
	return true, h.StopDeployment(d)
}

func TestDeploymentIdleReceiptPersistsUntilExplicitStart(t *testing.T) {
	h := &idleDeploymentTestHost{deploymentTestHost: &deploymentTestHost{
		fakeHost: &fakeHost{name: "worker"}, running: map[string]bool{},
	}}
	m, err := New(Options{Dir: t.TempDir(), Host: h, Discoverer: NopDiscoverer{}, ListenAddr: "127.0.0.1:0", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	if _, _, err := m.CreateCluster("deployments"); err != nil {
		t.Fatal(err)
	}
	spec := testDeployment(m).DeploymentSpec
	spec.KeepAlive = " 300s "
	d, err := m.createDeployment(spec)
	if err != nil || d.KeepAlive != "5m0s" {
		t.Fatalf("create keep-alive: %q, %v", d.KeepAlive, err)
	}
	if _, err := m.receiveDeployment(m.identity.UUID, d); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "ready")
	h.expire.Store(true)
	m.wakeDeployments()
	waitDeployment(t, m, d.ID, "idle")
	if m.DeploymentReady(d.ModelID()) || len(m.deploymentModels()) != 0 {
		t.Fatal("idle deployment remained routable")
	}
	store, err := openDeploymentStore(m.opts.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved, _ := store.get(false, d.ID); saved.Status.Phase != "idle" || saved.KeepAlive != d.KeepAlive {
		t.Fatalf("idle receipt lost after reload: %+v", saved)
	}
	if receipt, err := m.receiveDeployment(m.identity.UUID, d); err != nil || receipt.Status.Phase != "idle" {
		t.Fatalf("same generation should remain idle: %+v, %v", receipt.Status, err)
	}
	h.expire.Store(false)
	restart, err := m.changeDeployment(d.ID, "running")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.receiveDeployment(m.identity.UUID, restart); err != nil {
		t.Fatal(err)
	}
	waitDeployment(t, m, d.ID, "ready")
	if h.starts.Load() != 2 {
		t.Fatalf("deployment started %d times", h.starts.Load())
	}
}
