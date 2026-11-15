// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
					node.Status == nil && node.LastSeen != nil &&
					worker.Summary(context.Background()).OnlineCount == wantCount
			}
		}
		return false
	})
}

func TestPublicClusterReusesMembershipAndScheduler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{name: "gateway", limit: 2}, Mode: "gateway", PublicListenAddr: "127.0.0.1:0", PublicInference: http.NotFoundHandler(), Logf: t.Logf})
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
	server := httptest.NewTLSServer(gateway.publicRoutes(gateway.publicHub()))
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "gateway-ca.pem")
	if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	host := &fakeHost{name: "outbound-worker", limit: 2, models: []ModelStatus{{ID: "shared-model", Size: 1 << 20, Loaded: true, Slots: 2}, {ID: "private-model", Size: 1 << 20, Loaded: true, Slots: 1}}}
	opts := Options{Dir: dir, Host: host, Mode: "worker", GatewayURL: server.URL, TLSCAFile: caFile, ListenAddr: "192.0.2.1:11438", Logf: t.Logf}
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
	waitPublic(t, func() bool {
		st, addr, err := gateway.publicStatus(worker.identity.UUID)
		if err != nil || len(st.Models) != 1 {
			return false
		}
		gateway.dir.MarkSuccess(worker.identity.UUID, addr, st)
		return true
	})
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
	extra, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{limit: 2}, Mode: "worker", GatewayURL: server.URL, TLSCAFile: caFile, Logf: t.Logf})
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
	if err = gateway.RemoveMember(ctx, restarted.identity.UUID); err != nil {
		t.Fatal(err)
	}
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
