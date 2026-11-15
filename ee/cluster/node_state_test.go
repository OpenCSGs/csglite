// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRemoteNodeStateUsesPinnedLANController(t *testing.T) {
	bus := NewMemoryBus()
	master := startNode(t, bus, "master", &fakeHost{})
	worker := startNode(t, bus, "worker", &fakeHost{})
	_, token, err := master.m.CreateCluster("test")
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(master.m.ListenPort()))
	if _, err := worker.m.Join(context.Background(), token, addr); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.m.UpdateSettings(func(s *Settings) { s.ManagedBy = master.m.identity.UUID }); err != nil {
		t.Fatal(err)
	}
	st := worker.m.LocalStatus(context.Background())
	master.m.dir.MarkSuccess(worker.m.identity.UUID, net.JoinHostPort("127.0.0.1", strconv.Itoa(worker.m.ListenPort())), &st)
	for _, state := range []NodeState{NodeStateMaintenance, NodeStateDrain, NodeStateActive} {
		if _, err := master.m.setRemoteNodeState(context.Background(), worker.m.identity.UUID, state); err != nil {
			t.Fatal(err)
		}
		if got := worker.m.store.Settings().State; got != state {
			t.Fatalf("state=%s, want %s", got, state)
		}
	}
	if _, err := worker.m.UpdateSettings(func(s *Settings) { s.ManagedBy = "" }); err != nil {
		t.Fatal(err)
	}
	// Even a Master with cached authority must be refused by the worker.
	if _, err := master.m.setRemoteNodeState(context.Background(), worker.m.identity.UUID, NodeStateDrain); err == nil {
		t.Fatal("revoked controller changed state")
	}
}

func TestNodeStateControlRefusesUnassignedControllerAndBadState(t *testing.T) {
	m, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{}, Mode: "worker", GatewayURL: "https://gateway.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.Adopt(ClusterInfo{UUID: "cluster", Topology: "public", GatewayUUID: "master"}, "", []Member{{UUID: "master", CertFingerprint: "sha256:test"}}, m.identity.UUID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		controller, body string
		want             int
	}{
		{"stranger", `{"state":"active"}`, 403},
		{m.identity.UUID, `{"state":"active"}`, 403},
		{"master", `{"state":"invalid"}`, 400},
		{"master", `[]`, 400},
	} {
		w := httptest.NewRecorder()
		m.nodeStateHandler(tc.controller).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
		if w.Code != tc.want {
			t.Fatalf("controller=%s body=%s: %d %s", tc.controller, tc.body, w.Code, w.Body.String())
		}
	}
}
