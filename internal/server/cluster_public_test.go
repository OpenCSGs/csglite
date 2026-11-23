package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
)

func TestPublicClusterAlwaysAuthenticatesAndHasNoManagement(t *testing.T) {
	s := &Server{apiKeys: config.NewAPIKeyStore(t.TempDir())}
	_, key, err := s.apiKeys.Create("public-test")
	if err != nil {
		t.Fatal(err)
	}
	h := s.publicClusterInferenceRoutes()
	for _, tc := range []struct {
		path, key string
		status    int
	}{
		{"/v1/models", "", 401}, {"/v1/models", "invalid", 401}, {"/v1/models", key, 200},
		{"/api/settings", key, 404}, {"/api/cluster/token", key, 404}, {"/api/license", key, 404},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+tc.key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s => %d, want %d", tc.path, w.Code, tc.status)
		}
	}
	for _, source := range []string{"cloud", "provider:test", "pool:test"} {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","source":"`+source+`"}`))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Errorf("source %s => %d", source, w.Code)
		}
	}
}

func TestPublicManagementListenerCannotBindHostInterfaces(t *testing.T) {
	for _, mode := range []string{"gateway", "worker"} {
		for _, addr := range []string{":11435", "0.0.0.0:11435", "192.0.2.1:11435", "[::]:11435"} {
			if validatePublicManagementListener(mode, addr) == nil {
				t.Errorf("%s accepted %s", mode, addr)
			}
		}
		for _, addr := range []string{"127.0.0.1:11435", "localhost:11435", "[::1]:11435"} {
			if err := validatePublicManagementListener(mode, addr); err != nil {
				t.Errorf("%s rejected %s: %v", mode, addr, err)
			}
		}
	}
	if err := validatePublicManagementListener("", ":11435"); err != nil {
		t.Fatal("changed existing LAN listener behavior")
	}
}

func TestLeavePublicWorkerClearsConfigAndRestoresStandaloneAfterRestart(t *testing.T) {
	useIsolatedStorageHome(t)
	t.Setenv(EnvClusterDisabled, "")
	t.Setenv(EnvClusterJoinToken, "")
	t.Setenv(EnvClusterDiscovery, "none")
	t.Setenv(EnvClusterAddr, "127.0.0.1:0")
	t.Setenv(config.EnvClusterSecret, "")
	t.Setenv(config.EnvClusterName, "")
	config.Reset()
	t.Cleanup(config.Reset)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ListenAddr = "127.0.0.1:11435"
	cfg.Cluster = config.ClusterConfig{Mode: "worker", GatewayURL: "https://master.invalid", Name: "local node name"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	worker := newTestServerWithConfig(t, cfg)
	if worker.cluster == nil {
		t.Fatal("worker manager was not initialized")
	}
	t.Cleanup(worker.cluster.Stop)
	if err := worker.startCluster(t.Context()); err != nil {
		t.Fatal(err)
	}
	nodeID := worker.cluster.Identity().UUID
	request := func(s *Server, method, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/cluster", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		s.routes().ServeHTTP(w, r)
		return w
	}
	if w := request(worker, http.MethodPost, `{"name":"local"}`); w.Code != http.StatusForbidden {
		t.Fatalf("unpaired worker could create a LAN cluster: %d %s", w.Code, w.Body)
	}
	// The same leave endpoint recovers a node previously left on an older build.
	w := request(worker, http.MethodDelete, "")
	if w.Code != http.StatusOK {
		t.Fatalf("leave request failed: %d %s", w.Code, w.Body)
	}
	var left cluster.ClusterView
	if err := json.Unmarshal(w.Body.Bytes(), &left); err != nil {
		t.Fatal(err)
	}
	if left.Mode != "" || left.GatewayURL != "" || left.Cluster != nil || left.Node.UUID != nodeID {
		t.Fatal("leave did not restore standalone mode with the same identity")
	}
	// Subsequent settings writes must not put the stale Worker role back.
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	worker.cluster.Stop()
	config.Reset()
	t.Setenv(EnvClusterJoinToken, "leftover-worker-bootstrap-token")

	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Cluster != (config.ClusterConfig{Name: "local node name"}) || reloaded.ListenAddr != "127.0.0.1:11435" {
		t.Fatal("worker role was persisted again or unrelated settings were changed")
	}
	standalone := newTestServerWithConfig(t, reloaded)
	if standalone.cluster == nil {
		t.Fatal("standalone manager was not initialized")
	}
	t.Cleanup(standalone.cluster.Stop)
	if err := standalone.startCluster(t.Context()); err != nil {
		t.Fatal(err)
	}
	if standalone.cluster.Active() || standalone.cluster.Identity().UUID != nodeID {
		t.Fatal("restart changed the identity or automatically activated LAN networking")
	}
	if w := request(standalone, http.MethodPost, `{"name":"local"}`); w.Code != http.StatusCreated {
		t.Fatalf("LAN create after restart failed: %d %s", w.Code, w.Body)
	}
}
