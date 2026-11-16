package cli

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
)

const (
	joinTestGateway = "https://master.example.com:8080"
	joinTestToken   = "csgl1-731fd95a-c041-43a7-8f69-e20eec2c7cc8-example-secret"
	joinTestID      = "731fd95a-c041-43a7-8f69-e20eec2c7cc8"
)

// Exercise the CLI against a local HTTP server while replacing only process
// lifecycle operations. No test may stop or reconfigure the developer's Lite.
type publicJoinFixture struct {
	mu                           sync.Mutex
	t                            *testing.T
	server                       *httptest.Server
	store                        *cluster.Store
	cfg                          *config.Config
	view                         cluster.ClusterView
	running                      bool
	starts, stops, joins, leaves int
	joinError                    bool
	lostReceipt                  bool
	startError                   bool
	expectedAddress              string
}

func newPublicJoinFixture(t *testing.T, running bool) *publicJoinFixture {
	t.Helper()
	setupCLIConfigHome(t)
	t.Setenv("CSGHUB_LITE_CLUSTER_SECRET", "")
	t.Setenv("CSGHUB_LITE_CLUSTER_JOIN_TOKEN", "")
	f := &publicJoinFixture{t: t, running: running, expectedAddress: joinTestGateway}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ListenAddr = f.server.Listener.Addr().String()
	cfg.ServerURL = "https://hub.example.com"
	cfg.Token = "existing-hub-session"
	cfg.Cluster.Name = "my-worker"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	f.cfg = cfg
	f.store, err = cluster.OpenStore(filepath.Join(cfg.StorageDir(), "cluster"))
	if err != nil {
		t.Fatal(err)
	}
	oldStart, oldRestart := startBackgroundServerForStart, startBackgroundServerForRestart
	oldStop := stopBackgroundServiceForRestart
	oldStartWait, oldRestartWait := waitForServerForStart, waitForServerForRestart
	t.Cleanup(func() {
		startBackgroundServerForStart, startBackgroundServerForRestart = oldStart, oldRestart
		stopBackgroundServiceForRestart = oldStop
		waitForServerForStart, waitForServerForRestart = oldStartWait, oldRestartWait
	})
	start := func(next *config.Config) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.starts++
		if f.startError && next.Cluster.Mode == "worker" {
			return errors.New("fixture start failed")
		}
		f.running = true
		f.view.Mode, f.view.GatewayURL = next.Cluster.Mode, next.Cluster.GatewayURL
		return nil
	}
	startBackgroundServerForStart, startBackgroundServerForRestart = start, start
	stopBackgroundServiceForRestart = func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stops++
		f.running = false
		return nil
	}
	wait := func(base string, _ time.Duration) error {
		if !serverHealthy(base) {
			return errors.New("fixture did not start")
		}
		return nil
	}
	waitForServerForStart, waitForServerForRestart = wait, wait
	return f
}

func (f *publicJoinFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /api/health":
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	case "GET /api/cluster":
	case "POST /api/cluster/join":
		f.joins++
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["token"] != joinTestToken || body["address"] != f.expectedAddress {
			f.t.Error("join did not send the requested token and Master address")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if f.joinError {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"node limit reached","code":"feature_not_licensed","limit":2,"current":2}`))
			return
		}
		f.view.Cluster = &cluster.ClusterInfo{UUID: joinTestID, Name: "fixture", Topology: "public", GatewayUUID: "master"}
		if err := f.store.Adopt(*f.view.Cluster, "", nil, "worker"); err != nil {
			f.t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if f.lostReceipt {
			_, _ = w.Write([]byte(`{"incomplete":`))
			return
		}
	case "DELETE /api/cluster":
		f.leaves++
		if err := f.store.Leave(); err != nil {
			f.t.Error(err)
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.view.Cluster = nil
	default:
		http.NotFound(w, r)
		return
	}
	if err := json.NewEncoder(w).Encode(f.view); err != nil {
		f.t.Error(err)
	}
}

func runPublicJoin(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newClusterJoinCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return output.String(), err
}

func TestClusterPublicJoinAliasesAndLeave(t *testing.T) {
	for _, flag := range []string{"--gateway", "--server", "--address"} {
		t.Run(flag, func(t *testing.T) {
			f := newPublicJoinFixture(t, false)
			output, err := runPublicJoin(t, joinTestToken+"\n", flag, joinTestGateway+"/", "--token-stdin")
			if err != nil || !strings.Contains(output, "Joined cluster") {
				t.Fatalf("join failed: %v, %s", err, output)
			}
			config.Reset()
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Cluster.Mode != "worker" || cfg.Cluster.GatewayURL != joinTestGateway || cfg.Cluster.Name != "my-worker" || cfg.ServerURL != f.cfg.ServerURL || cfg.Token != f.cfg.Token {
				t.Fatal("worker setup changed unrelated settings or lost the Master address")
			}
			path, err := config.ConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := os.ReadFile(path)
			if err != nil || bytes.Contains(persisted, []byte(joinTestToken)) {
				t.Fatal("join token must never be persisted in config.json", err)
			}
			output, err = runPublicJoin(t, "", joinTestToken, flag, joinTestGateway)
			if err != nil || !strings.Contains(output, "Already joined") || f.starts != 1 || f.stops != 0 || f.joins != 1 {
				t.Fatalf("repeated join was not idempotent: %v, %s", err, output)
			}
			model := filepath.Join(cfg.ModelDir, "keep.gguf")
			if err := os.MkdirAll(filepath.Dir(model), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(model, []byte("model data"), 0o600); err != nil {
				t.Fatal(err)
			}
			leave := newClusterLeaveCmd()
			leave.SetArgs([]string{})
			if err := leave.Execute(); err != nil {
				t.Fatal(err)
			}
			if f.leaves != 1 || f.store.InCluster() {
				t.Fatal("leave did not clear membership")
			}
			if _, err := os.Stat(model); err != nil {
				t.Fatal("leave removed a local model", err)
			}
		})
	}
}

func TestClusterPublicJoinRestartsRunningLANService(t *testing.T) {
	f := newPublicJoinFixture(t, true)
	if _, err := runPublicJoin(t, "", joinTestToken, "--server", joinTestGateway); err != nil {
		t.Fatal(err)
	}
	if f.starts != 1 || f.stops != 1 || f.joins != 1 {
		t.Fatalf("lifecycle: starts=%d stops=%d joins=%d", f.starts, f.stops, f.joins)
	}
}

func TestClusterPublicJoinReusesConfiguredRunningWorker(t *testing.T) {
	f := newPublicJoinFixture(t, true)
	f.cfg.Cluster.Mode, f.cfg.Cluster.GatewayURL = "worker", joinTestGateway
	f.view.Mode, f.view.GatewayURL = "worker", joinTestGateway
	if err := config.Save(f.cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := runPublicJoin(t, "", joinTestToken, "--gateway", joinTestGateway, "--server", joinTestGateway+"/"); err != nil {
		t.Fatal(err)
	}
	if f.starts != 0 || f.stops != 0 || f.joins != 1 {
		t.Fatal("already configured worker was unnecessarily restarted")
	}
}

func TestClusterPublicJoinFailuresRestoreConfiguration(t *testing.T) {
	for _, running := range []bool{false, true} {
		for _, startError := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "stopped", true: "running"}[running], map[bool]string{false: "quota", true: "start-error"}[startError]}, "/"), func(t *testing.T) {
				f := newPublicJoinFixture(t, running)
				f.startError, f.joinError = startError, true
				_, err := runPublicJoin(t, "", joinTestToken, "--gateway", joinTestGateway)
				if err == nil {
					t.Fatal("failed join reported success")
				}
				if !startError && !strings.Contains(err.Error(), "up to 2 nodes") {
					t.Fatal("quota guidance lost", err)
				}
				config.Reset()
				cfg, loadErr := config.Load()
				if loadErr != nil || !reflect.DeepEqual(cfg, f.cfg) {
					t.Fatal("previous configuration was not restored", loadErr)
				}
				if f.running != running || f.store.InCluster() {
					t.Fatal("failed join changed previous running state or membership")
				}
			})
		}
	}
}

func TestClusterPublicJoinLostReceiptRetainsPairing(t *testing.T) {
	f := newPublicJoinFixture(t, false)
	f.lostReceipt = true
	_, err := runPublicJoin(t, "", joinTestToken, "--gateway", joinTestGateway)
	if err == nil || !strings.Contains(err.Error(), "configuration retained") {
		t.Fatal("missing uncertain receipt guidance", err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.Cluster.Mode != "worker" || !f.running || f.stops != 0 || !f.store.InCluster() {
		t.Fatal("a persisted pairing was undone after losing the HTTP response", err)
	}
}

func TestClusterPublicJoinRejectsExistingCluster(t *testing.T) {
	for _, source := range []string{"stored", "running"} {
		for _, topology := range []string{"lan", "public"} {
			t.Run(source+"/"+topology, func(t *testing.T) {
				f := newPublicJoinFixture(t, true)
				f.view.Cluster = &cluster.ClusterInfo{UUID: "another-cluster", Topology: topology}
				if source == "stored" {
					if err := f.store.Adopt(*f.view.Cluster, "", nil, "worker"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := runPublicJoin(t, "", joinTestToken, "--gateway", joinTestGateway); err == nil || !strings.Contains(err.Error(), "leave") {
					t.Fatal("existing cluster was not protected", err)
				}
				if f.starts != 0 || f.stops != 0 || f.joins != 0 {
					t.Fatal("rejecting a different cluster changed the running service")
				}
			})
		}
	}
}

func TestClusterPublicJoinRejectsConflictingStartupToken(t *testing.T) {
	f := newPublicJoinFixture(t, true)
	t.Setenv("CSGHUB_LITE_CLUSTER_JOIN_TOKEN", "another-token")
	if _, err := runPublicJoin(t, "", joinTestToken, "--gateway", joinTestGateway); err == nil || !strings.Contains(err.Error(), "CSGHUB_LITE_CLUSTER_JOIN_TOKEN") {
		t.Fatal("conflicting auto-join token was not rejected", err)
	}
	if f.starts != 0 || f.stops != 0 || f.joins != 0 {
		t.Fatal("conflicting token changed the running service")
	}
}

func TestClusterPublicJoinRejectsInvalidArgumentsBeforeChanges(t *testing.T) {
	tests := [][]string{
		{"--gateway", joinTestGateway},
		{joinTestToken, "--gateway", ""},
		{joinTestToken, "--gateway", "http://master.example.com"},
		{joinTestToken, "--gateway", joinTestGateway, "--server", "https://other.example.com"},
		{joinTestToken, "--server", joinTestGateway, "--address", "another-node:11438"},
		{joinTestToken, "--gateway", "https://user:secret@master.example.com"},
		{joinTestToken, "--gateway", joinTestGateway + "/api"},
		{joinTestToken, "--gateway", joinTestGateway + "?token=secret"},
		{joinTestToken, "--gateway", joinTestGateway + "#fragment"},
		{joinTestToken, "--gateway", joinTestGateway, "--ca-file", "does-not-exist.pem"},
		{joinTestToken, "--ca-file", ""},
		{"not-a-join-token", "--gateway", joinTestGateway},
		{joinTestToken, "--gateway", joinTestGateway, "--token-stdin"},
	}
	for i, args := range tests {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			f := newPublicJoinFixture(t, true)
			if _, err := runPublicJoin(t, "", args...); err == nil {
				t.Fatal("invalid arguments accepted")
			}
			cfg, err := config.Load()
			if err != nil || cfg != f.cfg || f.starts != 0 || f.stops != 0 || f.joins != 0 {
				t.Fatal("invalid input changed configuration or service", err)
			}
		})
	}
}

func TestPublicWorkerJoinConfigCAAndLoopback(t *testing.T) {
	setupCLIConfigHome(t)
	server := httptest.NewTLSServer(nil)
	t.Cleanup(server.Close)
	ca := filepath.Join(t.TempDir(), "gateway-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	original := &config.Config{ListenAddr: config.DefaultListenAddr}
	next, err := publicWorkerJoinConfig(original, joinTestGateway, ca, true)
	if err != nil || next.ListenAddr != "127.0.0.1:11435" || next.Cluster.TLSCAFile != ca || original.Cluster.Mode != "" {
		t.Fatal("private CA or loopback defaults not applied to a copy", err)
	}
	retained, err := publicWorkerJoinConfig(next, joinTestGateway, "", false)
	if err != nil || retained.Cluster.TLSCAFile != ca {
		t.Fatal("omitting --ca-file lost trust configuration", err)
	}
	cleared, err := publicWorkerJoinConfig(next, joinTestGateway, "", true)
	if err != nil || cleared.Cluster.TLSCAFile != "" {
		t.Fatal("explicit empty --ca-file did not restore system trust", err)
	}
	for _, listen := range []string{"0.0.0.0:11435", "[::]:11435", "192.168.1.2:11435", "invalid"} {
		if _, err := publicWorkerJoinConfig(&config.Config{ListenAddr: listen}, joinTestGateway, "", false); err == nil {
			t.Fatalf("non-loopback management accepted: %s", listen)
		}
	}
	if err := os.WriteFile(ca, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := publicWorkerJoinConfig(original, joinTestGateway, ca, true); err == nil {
		t.Fatal("invalid CA accepted")
	}
	original.Cluster.Secret = "auto-form-secret"
	if _, err := publicWorkerJoinConfig(original, joinTestGateway, "", false); err == nil {
		t.Fatal("LAN auto-form secret accepted for public worker")
	}
}

func TestClusterLANJoinKeepsLegacyAddress(t *testing.T) {
	f := newPublicJoinFixture(t, true)
	f.expectedAddress = "192.168.1.20:11438"
	if _, err := runPublicJoin(t, "", joinTestToken, "--address", f.expectedAddress); err != nil {
		t.Fatal(err)
	}
	if f.starts != 0 || f.stops != 0 || f.joins != 1 || f.cfg.Cluster.Mode != "" {
		t.Fatal("LAN join unexpectedly provisioned a public worker")
	}
}
