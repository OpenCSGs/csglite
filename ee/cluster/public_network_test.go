// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bytes"
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
)

func waitPublic(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if fn() {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("public cluster did not become ready")
		}
	}
}

func assertWorkerGatewayOnline(t *testing.T, worker *Manager, gateway string, online bool) {
	t.Helper()
	waitPublic(t, func() bool {
		view := worker.View(context.Background())
		for _, node := range view.Members {
			if node.UUID == gateway {
				wantHealth := HealthDown
				wantCount := 1
				if online {
					wantHealth = HealthHealthy
					wantCount = 2
				}
				return node.Online == online && node.Health == wantHealth &&
					node.Status == nil && node.StatusScope == "connectivity_only" && node.LastSeen != nil &&
					worker.Summary(context.Background()).OnlineCount == wantCount
			}
		}
		return false
	})
}

func TestPublicClusterReusesMembershipAndScheduler(t *testing.T) {
	t.Run("HTTPS", func(t *testing.T) { testPublicClusterTransport(t, false) })
	t.Run("HTTP", func(t *testing.T) { testPublicClusterTransport(t, true) })
}

func testPublicClusterTransport(t *testing.T, allowHTTP bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{name: "gateway", limit: 2}, Mode: "gateway", AllowHTTP: allowHTTP, PublicListenAddr: "127.0.0.1:0", PublicInference: http.NotFoundHandler(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Stop()
	if err = gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	info, token, err := gateway.CreateCluster("public test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Topology != "public" || info.GatewayUUID != gateway.identity.UUID {
		t.Fatal("public cluster authority was not persisted")
	}
	var server *httptest.Server
	var caFile string
	if allowHTTP {
		server = httptest.NewServer(gateway.publicRoutes(gateway.publicHub()))
	} else {
		server = httptest.NewTLSServer(gateway.publicRoutes(gateway.publicHub()))
		caFile = filepath.Join(t.TempDir(), "gateway-ca.pem")
		if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	defer server.Close()
	dir := t.TempDir()
	host := &fakeHost{name: "outbound-worker", limit: 2, models: []ModelStatus{{ID: "shared-model", Size: 1 << 20, Loaded: true, Slots: 2}, {ID: "private-model", Size: 1 << 20, Loaded: true, Slots: 1}}}
	opts := Options{Dir: dir, Host: host, Mode: "worker", AllowHTTP: allowHTTP, GatewayURL: server.URL, TLSCAFile: caFile, ListenAddr: "192.0.2.1:11438", Logf: t.Logf}
	worker, err := New(opts)
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
	if worker.ListenPort() != 0 || worker.peerServer != nil {
		t.Fatal("outbound worker opened an inbound listener")
	}
	if worker.store.JoinTokenHash() != "" {
		t.Fatal("gateway disclosed its enrollment key to a worker")
	}
	waitPublic(t, func() bool { _, _, err := gateway.publicStatus(worker.identity.UUID); return err == nil })
	assertWorkerGatewayOnline(t, worker, gateway.identity.UUID, true)
	if gateway.RemoteHasModel("private-model") || gateway.RemoteHasModel("shared-model") {
		t.Fatal("worker shared models without explicit consent")
	}
	if _, err = worker.UpdateSettings(func(s *Settings) { s.SharedModels = []string{"shared-model"} }); err != nil {
		t.Fatal(err)
	}
	if !gateway.RemoteHasModel("shared-model") {
		t.Fatal("sharing returned before the gateway directory was updated")
	}

	if gateway.RemoteHasModel("private-model") {
		t.Fatal("private model leaked into directory")
	}
	resp, err := gateway.RouteRaw(ctx, "shared-model", "cluster", "/v1/chat/completions", []byte(`{"model":"shared-model","source":"cluster"}`), http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), "outbound-worker") || host.served.Load() != 1 {
		t.Fatalf("response = %s", data)
	}
	if resp.Header.Get(NodeHeader) != worker.identity.UUID {
		t.Fatal("executing node header missing")
	}
	for _, shared := range []bool{false, true, false, true} {
		if _, err := worker.UpdateSettings(func(s *Settings) {
			s.SharedModels = nil
			if shared {
				s.SharedModels = []string{"shared-model"}
			}
		}); err != nil {
			t.Fatal(err)
		}
		if gateway.RemoteHasModel("shared-model") != shared {
			t.Fatalf("gateway directory not synchronized after shared=%v", shared)
		}
		ex := gateway.Explain(ctx, "shared-model", 10, 10, "")
		eligible := false
		for _, c := range ex.Candidates {
			if c.UUID == worker.identity.UUID {
				eligible = c.Eligible
			}
		}
		if eligible != shared {
			t.Fatalf("gateway scheduler not synchronized after shared=%v: %+v", shared, ex)
		}
	}

	for _, state := range []NodeState{NodeStateMaintenance, NodeStateDrain, NodeStateActive} {
		req := httptest.NewRequest(http.MethodPost, "/api/cluster/nodes/worker/state", strings.NewReader(`{"state":"`+string(state)+`"}`))
		req.SetPathValue("uuid", worker.identity.UUID)
		w := httptest.NewRecorder()
		gateway.Admin().HandleNodeState(w, req)
		if w.Code != 200 {
			t.Fatalf("remote state %s: %d %s", state, w.Code, w.Body.String())
		}
		var settings Settings
		if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil || settings.State != state || worker.store.Settings().State != state {
			t.Fatalf("state not applied: %s", w.Body.String())
		}
		st := gateway.candidateStatus(worker.identity.UUID)
		if st.State != state || st.AcceptWork != (state == NodeStateActive) {
			t.Fatalf("scheduler stale after state %s: %+v", state, st)
		}
		if len(st.Net.Addrs) != 0 {
			t.Fatal("state receipt exposed worker network addresses")
		}
		if _, ok := st.Model("private-model"); ok {
			t.Fatal("state receipt leaked a private model")
		}
	}

	// The existing signed node identity authenticates reconnection; a captured
	// connection proof cannot be reused, even while its timestamp is valid.
	proof, err := worker.publicProof(gateway.identity.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if err = gateway.authorizePublic(ctx, worker.identity.UUID, proof); err != nil {
		t.Fatal(err)
	}
	if err = gateway.authorizePublic(ctx, worker.identity.UUID, proof); err == nil {
		t.Fatal("replayed node proof accepted")
	}
	// A third CE node is refused by the existing membership quota.
	extra, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{limit: 2}, Mode: "worker", AllowHTTP: allowHTTP, GatewayURL: server.URL, TLSCAFile: caFile, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Stop()
	_, err = extra.Join(ctx, token, "")
	var pe *peerError
	if !errors.As(err, &pe) || pe.Status != 403 || pe.Body.Code != "feature_not_licensed" {
		t.Fatalf("third node result: %v", err)
	}
	// A dropped tunnel changes the worker view immediately, then its normal
	// retry loop restores online state without re-enrollment.
	gateway.publicHub().Disconnect(worker.identity.UUID)
	assertWorkerGatewayOnline(t, worker, gateway.identity.UUID, false)
	assertWorkerGatewayOnline(t, worker, gateway.identity.UUID, true)
	// The worker reconnects after a process restart without registering again.
	worker.Stop()
	assertWorkerGatewayOnline(t, worker, gateway.identity.UUID, false)
	waitPublic(t, func() bool { _, ok := gateway.publicHub().Snapshot(worker.identity.UUID); return !ok })
	restarted, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	if err = restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { _, _, err := gateway.publicStatus(restarted.identity.UUID); return err == nil })
	assertWorkerGatewayOnline(t, restarted, gateway.identity.UUID, true)
	if len(gateway.store.Members()) != 1 {
		t.Fatal("reconnection duplicated membership")
	}
	removeNode := func(nodeUUID string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, "/api/cluster/nodes/"+nodeUUID, nil)
		req.SetPathValue("uuid", nodeUUID)
		rec := httptest.NewRecorder()
		gateway.Admin().HandleNodeRemove(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("remove node: %d %s", rec.Code, rec.Body)
		}
		var view ClusterView
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if len(view.Members) != 1 || view.Members[0].UUID != gateway.identity.UUID {
			t.Fatalf("unexpected membership after removal: %+v", view.Members)
		}
	}
	removeNode(restarted.identity.UUID)
	assertWorkerGatewayOnline(t, restarted, gateway.identity.UUID, false)
	if _, ok := gateway.publicHub().Snapshot(restarted.identity.UUID); ok {
		t.Fatal("removed member remained connected")
	}
	proof, err = restarted.publicProof(gateway.identity.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.authorizePublic(ctx, restarted.identity.UUID, proof) == nil {
		t.Fatal("removed identity was still admitted")
	}

	newToken := gateway.store.JoinToken()
	if newToken == token {
		t.Fatal("removing an online worker did not rotate the join token")
	}
	if _, err := extra.Join(ctx, newToken, ""); err != nil {
		t.Fatal(err)
	}
	uuid := extra.identity.UUID
	extra.Stop()
	waitPublic(t, func() bool {
		_, connected := gateway.publicHub().Snapshot(uuid)
		return !connected
	})
	removeNode(uuid)
	if _, ok := gateway.store.Member(uuid); ok || gateway.store.JoinToken() == newToken {
		t.Fatal("offline removal did not revoke membership and rotate the join token")
	}
}

func TestPublicModeConfigurationAndSharingPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create("lan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(Options{Dir: dir, Host: &fakeHost{}, Mode: "gateway", PublicInference: http.NotFoundHandler()}); err == nil {
		t.Fatal("implicitly converted existing LAN cluster")
	}
	if _, err = New(Options{Dir: t.TempDir(), Host: &fakeHost{}, Mode: "gateway", PublicListenAddr: ":8080", PublicInference: http.NotFoundHandler()}); err == nil {
		t.Fatal("unencrypted public listener accepted")
	}
	if _, err = New(Options{Dir: t.TempDir(), Host: &fakeHost{}, Mode: "worker", GatewayURL: "http://example.com"}); err == nil {
		t.Fatal("unencrypted gateway accepted")
	}
	_, err = store.UpdateSettings(func(s *Settings) { s.SharedModels = []string{"model-a"} })
	if err != nil {
		t.Fatal(err)
	}
	settings := store.Settings()
	settings.SharedModels[0] = "changed"
	reloaded, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Settings().SharedModels[0] != "model-a" || store.Settings().SharedModels[0] != "model-a" {
		t.Fatal("sharing state was not isolated and persisted")
	}
}

func TestOldPublicConnectionCannotOverwriteReplacement(t *testing.T) {
	m := &Manager{}
	old := m.observePublicGateway("gateway")
	old(true)
	next := m.observePublicGateway("gateway")
	if m.publicConnection.online {
		t.Fatal("retry reported connected before handshake")
	}
	next(true)
	old(false)
	if !m.publicConnection.online {
		t.Fatal("old disconnect hid new connection")
	}
	next(false)
	if m.publicConnection.online || m.publicConnection.lastSeen.IsZero() {
		t.Fatal("disconnect was not recorded")
	}
	m.observePublicGateway("different-gateway")
	if !m.publicConnection.lastSeen.IsZero() {
		t.Fatal("last-seen leaked across gateway identities")
	}
}

func TestWorkerServiceViewsRespectSharingWithoutHidingLocalInventory(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"worker", ""} {
		m, err := New(Options{Dir: t.TempDir(), Mode: mode, GatewayURL: "https://gateway.example", Host: &fakeHost{models: []ModelStatus{{ID: "shared"}, {ID: "private"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.UpdateSettings(func(s *Settings) { s.SharedModels = []string{"shared"} }); err != nil {
			t.Fatal(err)
		}
		models := m.Models(ctx)
		want := 2
		if mode == "worker" {
			want = 1
		}
		if len(models) != want {
			t.Fatalf("mode %s models=%+v", mode, models)
		}
		ex := m.Explain(ctx, "private", 10, 10, "")
		if mode == "worker" && (ex.Candidates[0].Eligible || ex.Candidates[0].Excluded != "model is not shared by this node") {
			t.Fatalf("private model eligible: %+v", ex)
		}
		if len(m.View(ctx).Members[0].Status.Models) != 2 || len(m.LocalStatus(ctx).Models) != 2 {
			t.Fatal("service filtering changed local inventory")
		}
		if _, err := m.UpdateSettings(func(s *Settings) { s.SharedModels = []string{"shared", "private"} }); err != nil {
			t.Fatal(err)
		}
		if len(m.Models(ctx)) != 2 || !m.Explain(ctx, "private", 10, 10, "").Candidates[0].Eligible {
			t.Fatal("sharing did not immediately update local service views")
		}
	}
}

func TestPublicWorkerExplainsGatewayTelemetryScope(t *testing.T) {
	m, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{}, Mode: "worker", GatewayURL: "https://gateway.example"})
	if err != nil {
		t.Fatal(err)
	}
	info := ClusterInfo{UUID: "cluster", Topology: "public", GatewayUUID: "gateway"}
	if err := m.store.Adopt(info, "", []Member{{UUID: "gateway", Name: "gateway", CertFingerprint: "sha256:test"}}, m.identity.UUID); err != nil {
		t.Fatal(err)
	}
	m.dir.Track("gateway", nil)
	m.observePublicGateway("gateway")(true)
	view := m.View(context.Background())
	node := view.Members[1]
	if !node.Online || node.Status != nil || node.StatusScope != "connectivity_only" {
		t.Fatalf("gateway view=%+v", node)
	}
	ex := m.Explain(context.Background(), "m", 0, 0, "")
	for _, c := range ex.Candidates {
		if c.UUID == "gateway" {
			if c.Eligible || c.Excluded != "gateway telemetry is managed on the Master" {
				t.Fatalf("gateway candidate=%+v", c)
			}
			return
		}
	}
	t.Fatal("gateway missing from explain")
}

func TestPublicWorkerQueuesSharingChangesAcrossConnectWindow(t *testing.T) {
	m, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{models: []ModelStatus{{ID: "m"}}}, Mode: "worker", GatewayURL: "https://gateway.example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{"m"}, {}, {"m"}} {
		if _, err := m.UpdateSettings(func(s *Settings) { s.SharedModels = ids }); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.publicStatusUpdates) != 1 {
		t.Fatal("sharing updates were not coalesced for the connecting session")
	}
	raw, err := m.publicWorkerStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var status Status
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Models) != 1 || status.Models[0].ID != "m" {
		t.Fatalf("queued update did not use latest settings: %s", raw)
	}
}

func TestPublicWorkerRecoversAfterRejectedHandshake(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gateway, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{limit: 2}, Mode: "gateway", PublicListenAddr: "127.0.0.1:0", PublicInference: http.NotFoundHandler(), Logf: t.Logf})
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Stop()
			if err = gateway.Start(ctx); err != nil {
				t.Fatal(err)
			}
			_, token, err := gateway.CreateCluster("retry")
			if err != nil {
				t.Fatal(err)
			}
			routes := gateway.publicRoutes(gateway.publicHub())
			var attempts, joins atomic.Int32
			var mu sync.Mutex
			var firstProof string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == peerPathJoin {
					joins.Add(1)
				}
				if r.URL.Path == "/cluster/v1/connect" {
					attempt := attempts.Add(1)
					mu.Lock()
					proof := r.Header.Get("X-CSGLite-Node-Token")
					if attempt == 1 {
						firstProof = proof
					}
					reused := attempt > 1 && proof == firstProof
					mu.Unlock()
					if reused {
						t.Error("reconnection reused an old signed proof")
					}
					if attempt == 1 {
						http.Error(w, "temporary rejection", status)
						return
					}
				}
				routes.ServeHTTP(w, r)
			}))
			defer server.Close()
			caFile := filepath.Join(t.TempDir(), "ca.pem")
			if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			worker, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{limit: 2}, Mode: "worker", GatewayURL: server.URL, TLSCAFile: caFile, Logf: t.Logf})
			if err != nil {
				t.Fatal(err)
			}
			defer worker.Stop()
			if err = worker.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = worker.Join(ctx, token, server.URL); err != nil {
				t.Fatal(err)
			}
			uuid := worker.identity.UUID
			waitPublic(t, func() bool { _, _, err := gateway.publicStatus(uuid); return err == nil })
			if attempts.Load() < 2 || joins.Load() != 1 || len(gateway.store.Members()) != 1 {
				t.Fatal("recovery did not reuse the existing membership")
			}
			if err = gateway.RemoveMember(ctx, uuid); err != nil {
				t.Fatal(err)
			}
			before := attempts.Load()
			waitPublic(t, func() bool { return attempts.Load() > before })
			if joins.Load() != 1 || len(gateway.store.Members()) != 0 {
				t.Fatal("revoked node was re-enrolled")
			}
			if _, ok := gateway.publicHub().Snapshot(uuid); ok {
				t.Fatal("revoked node became connected")
			}
		})
	}
}

func TestPublicWorkerReconnectsAfterGatewayRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := Options{Dir: t.TempDir(), Host: &fakeHost{limit: 2}, Mode: "gateway", PublicListenAddr: "127.0.0.1:0", PublicInference: http.NotFoundHandler(), Logf: t.Logf}
	gateway, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var current atomic.Pointer[Manager]
	current.Store(gateway)
	defer func() { current.Load().Stop() }()
	_, token, err := gateway.CreateCluster("restart")
	if err != nil {
		t.Fatal(err)
	}
	var joins atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == peerPathJoin {
			joins.Add(1)
		}
		g := current.Load()
		hub := g.publicHub()
		if hub == nil {
			http.Error(w, "restarting", 503)
			return
		}
		g.publicRoutes(hub).ServeHTTP(w, r)
	}))
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	worker, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{limit: 2}, Mode: "worker", GatewayURL: server.URL, TLSCAFile: caFile, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Stop()
	if err = worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Join(ctx, token, server.URL); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { _, _, err := gateway.publicStatus(worker.identity.UUID); return err == nil })
	identity := gateway.identity.UUID
	gateway.Stop()
	restarted, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	current.Store(restarted)
	if err = restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitPublic(t, func() bool { _, _, err := restarted.publicStatus(worker.identity.UUID); return err == nil })
	assertWorkerGatewayOnline(t, worker, identity, true)
	if restarted.identity.UUID != identity || len(restarted.store.Members()) != 1 || joins.Load() != 1 {
		t.Fatal("gateway restart changed membership or re-enrolled the worker")
	}
}

func TestPublicClusterHTTPJoinFromStandalone(t *testing.T) {
	ctx := t.Context()
	gateway, err := New(Options{
		Dir: t.TempDir(), Host: &fakeHost{limit: 2}, Mode: "gateway", AllowHTTP: true,
		PublicListenAddr: "127.0.0.1:0", PublicInference: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Stop)
	if err = gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	created, token, err := gateway.CreateCluster("HTTP join test")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.publicRoutes(gateway.publicHub()))
	defer server.Close()

	var savedGateway string
	worker, err := New(Options{
		Dir: t.TempDir(), Host: &fakeHost{limit: 2}, ListenAddr: "127.0.0.1:0",
		ConfigureWorkerRole: func(address string, allowHTTP bool, caFile string) (func() error, error) {
			if !allowHTTP || caFile != "" {
				t.Fatal("HTTP join selected the wrong transport")
			}
			savedGateway = address
			return func() error { savedGateway = ""; return nil }, nil
		},
		ResetWorkerRole: func() error { savedGateway = ""; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Stop)
	if err = worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	request := func(token, address string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]any{"token": token, "address": address, "enable_ssl": false})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		worker.Admin().HandleJoin(w, httptest.NewRequest(http.MethodPost, "/api/cluster/join", bytes.NewReader(body)))
		return w
	}

	if response := request("invalid", server.URL); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid join returned %d", response.Code)
	}
	if savedGateway != "" || worker.mode() != "" || worker.Active() {
		t.Fatal("failed join changed standalone mode")
	}
	response := request(token, "https://"+strings.TrimPrefix(server.URL, "http://"))
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP join returned %d: %s", response.Code, response.Body)
	}
	if savedGateway != server.URL || worker.mode() != "worker" || worker.ListenPort() != 0 {
		t.Fatal("HTTP join did not save and apply the worker role")
	}
	if worker.Store().Cluster().UUID != created.UUID {
		t.Fatal("HTTP join saved the wrong cluster")
	}
	waitPublic(t, func() bool { _, ok := gateway.publicHub().Snapshot(worker.identity.UUID); return ok })
	if err = worker.Leave(ctx); err != nil {
		t.Fatal(err)
	}
	if worker.mode() != "" || savedGateway != "" {
		t.Fatal("leaving a UI-configured worker did not restore standalone mode")
	}
}
