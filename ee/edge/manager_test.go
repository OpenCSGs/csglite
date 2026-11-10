// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package edge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

type fakeHost struct {
	gateway    string
	token      string
	unlicensed atomic.Bool
	mu         sync.Mutex
	seen       []*http.Request
}

func (h *fakeHost) InferenceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.seen = append(h.seen, r.Clone(context.Background()))
		h.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
}

func (h *fakeHost) Models(context.Context) []Model {
	return []Model{{ID: "Qwen3-8B-GGUF", Ready: true, Format: "gguf"}, {ID: "too-big", Ready: false}}
}
func (h *fakeHost) ActiveRequests() int { return 0 }
func (h *fakeHost) Cloud() (string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gateway, h.token
}

// configure changes the gateway and sign-in, as editing settings does.
func (h *fakeHost) configure(gateway, token string) {
	h.mu.Lock()
	h.gateway, h.token = gateway, token
	h.mu.Unlock()
}
func (h *fakeHost) Version() string { return "test" }
func (h *fakeHost) Licensed() bool  { return !h.unlicensed.Load() }
func (h *fakeHost) requests() []*http.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*http.Request(nil), h.seen...)
}

// fakeGateway plays the CSGHub AI gateway side of the tunnel.
type fakeGateway struct {
	t           *testing.T
	server      *httptest.Server
	registers   atomic.Int32
	rejectToken atomic.Value // string: node token to refuse once
	foreignUUID atomic.Value // string: node UUID registered to another account
	lastUUID    atomic.Value // string: node UUID of the latest registration
	disabled    atomic.Bool
	sessions    chan *gatewaySession
}

type gatewaySession struct {
	mux     *yamux.Session
	control *controlConn
	status  chan controlMessage
}

func newFakeGateway(t *testing.T) *fakeGateway {
	g := &fakeGateway{t: t, sessions: make(chan *gatewaySession, 4)}
	g.rejectToken.Store("")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/edge/nodes", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer user-token") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			NodeUUID string `json:"node_uuid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if forbidden, _ := g.foreignUUID.Load().(string); forbidden != "" && body.NodeUUID == forbidden {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"msg": "edge node belongs to another user"})
			return
		}
		g.lastUUID.Store(body.NodeUUID)
		n := g.registers.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"msg": "OK", "data": map[string]any{"token": "node-token-" + string(rune('0'+n))}})
	})
	mux.HandleFunc("POST /api/v1/edge/applications", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["quantization"] != "gguf" || body["node_uuid"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"msg": "bad application"})
			return
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"msg": "this model of this node already has a live application"})
	})
	mux.HandleFunc("GET /v1/edge/connect", func(w http.ResponseWriter, r *http.Request) {
		if g.disabled.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get(headerTunnelProtocol) != tunnelProtocolVersion || r.Header.Get(headerNodeToken) == g.rejectToken.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sess, err := yamux.Client(newWSStream(ws), yamuxConfig(time.Second))
		if err != nil {
			return
		}
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		gs := &gatewaySession{mux: sess, control: newControlConn(stream), status: make(chan controlMessage, 16)}
		_ = gs.control.write(controlMessage{Type: msgHello, HeartbeatSeconds: 1})
		go func() {
			for {
				msg, err := gs.control.read()
				if err != nil {
					close(gs.status)
					return
				}
				gs.status <- msg
			}
		}()
		g.sessions <- gs
		<-sess.CloseChan()
	})
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGateway) next(t *testing.T) *gatewaySession {
	t.Helper()
	select {
	case s := <-g.sessions:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("node did not connect")
		return nil
	}
}

// do sends one HTTP request to the node over a new tunnel stream, as the
// gateway relay does.
func (s *gatewaySession) do(t *testing.T, method, path string, header http.Header) *http.Response {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return s.mux.OpenStream() },
	}}
	req, err := http.NewRequest(method, "http://edge-node"+path, strings.NewReader(`{"model":"Qwen3-8B-GGUF"}`))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestManagerServesThroughTunnel(t *testing.T) {
	g := newFakeGateway(t)
	host := &fakeHost{gateway: g.server.URL, token: "user-token"}
	m, err := New(host, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()

	sess := g.next(t)
	first := <-sess.status
	if first.Type != msgStatus || len(first.Models) != 2 || !first.Models[0].Ready || first.Models[1].Ready {
		t.Fatalf("first status = %+v", first)
	}
	// Heartbeats follow at the interval the gateway asked for (1s).
	select {
	case msg := <-sess.status:
		if msg.Type != msgStatus {
			t.Fatalf("heartbeat = %+v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no heartbeat")
	}
	if st := m.Status(); st.State != StateConnected || st.NodeUUID == "" {
		t.Fatalf("status = %+v", st)
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer leaked")
	header.Set("Cookie", "a=b")
	header.Set(HeaderRequestID, "gw-1")
	resp := sess.do(t, http.MethodPost, "/v1/chat/completions", header)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != `{"ok":true}` {
		t.Fatalf("chat = %d %s", resp.StatusCode, body)
	}
	seen := host.requests()
	if len(seen) != 1 || seen[0].Header.Get("Authorization") != "" || seen[0].Header.Get("Cookie") != "" || seen[0].Header.Get(HeaderRequestID) != "gw-1" {
		t.Fatalf("node saw %+v", seen)
	}

	for _, path := range []string{"/api/ps", "/api/apps/shell", "/v1/models"} {
		resp = sess.do(t, http.MethodPost, path, nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
	if len(host.requests()) != 1 {
		t.Fatal("a refused path reached the server")
	}

	// Disabling announces a drain before closing.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = m.SetEnabled(false)
	}()
	for msg := range sess.status {
		if msg.Type == msgDrain {
			break
		}
	}
	<-done
	if st := m.Status(); st.State != StateDisabled || st.Enabled {
		t.Fatalf("status after disable = %+v", st)
	}
}

func TestManagerReconnectsAndReregistersOnRejectedToken(t *testing.T) {
	g := newFakeGateway(t)
	host := &fakeHost{gateway: g.server.URL, token: "user-token"}
	dir := t.TempDir()
	m, err := New(host, dir, "studio")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	sess := g.next(t)
	<-sess.status

	// The gateway drops the tunnel and now refuses the old token (as after
	// a token rotation): the node registers again and reconnects.
	g.rejectToken.Store("node-token-1")
	_ = sess.mux.Close()
	sess = g.next(t)
	<-sess.status
	if g.registers.Load() != 2 {
		t.Fatalf("registrations = %d, want 2", g.registers.Load())
	}
	m.Stop()

	// The identity survives a restart and serving resumes on its own.
	m2, err := New(host, dir, "studio")
	if err != nil {
		t.Fatal(err)
	}
	if m2.Status().NodeUUID != m.Status().NodeUUID {
		t.Fatal("node identity changed across restarts")
	}
}

func TestManagerKeepsTokenWhenNodeDisabled(t *testing.T) {
	g := newFakeGateway(t)
	g.disabled.Store(true)
	host := &fakeHost{gateway: g.server.URL, token: "user-token"}
	m, err := New(host, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(m.Status().LastError, "disabled by the platform") {
		if time.Now().After(deadline) {
			t.Fatalf("status = %+v", m.Status())
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(3500 * time.Millisecond) // two more attempts at 1s and 2s backoff
	if g.registers.Load() != 1 {
		t.Fatalf("registrations = %d, want 1: a disabled node must not re-register", g.registers.Load())
	}
}

func TestManagerRequiresLicense(t *testing.T) {
	g := newFakeGateway(t)
	host := &fakeHost{gateway: g.server.URL, token: "user-token"}
	host.unlicensed.Store(true)
	m, err := New(host, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	waitState(t, m, StateUnlicensed)
	if g.registers.Load() != 0 {
		t.Fatal("an unlicensed node must not register with the gateway")
	}

	// Once licensed it connects; when the license lapses it drains and
	// closes the tunnel at the next heartbeat (1s here).
	host.unlicensed.Store(false)
	sess := g.next(t)
	<-sess.status
	host.unlicensed.Store(true)
	drained := false
	for msg := range sess.status {
		if msg.Type == msgDrain {
			drained = true
			break
		}
	}
	if !drained {
		t.Fatal("tunnel closed without a drain after the license lapsed")
	}
	waitState(t, m, StateUnlicensed)
}

func waitState(t *testing.T, m *Manager, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for m.Status().State != want {
		if time.Now().After(deadline) {
			t.Fatalf("state = %s, want %s", m.Status().State, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestManagerSignedOut(t *testing.T) {
	g := newFakeGateway(t)
	m, err := New(&fakeHost{gateway: g.server.URL}, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for m.Status().State != StateSignedOut {
		if time.Now().After(deadline) {
			t.Fatalf("state = %s, want signed_out", m.Status().State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestApplicationsRewritesGatewayNotFoundPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	m, err := New(&fakeHost{gateway: srv.URL, token: "user-token"}, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Applications(context.Background(), 1, 100)
	if !errors.Is(err, ErrGatewayUnreachable) {
		t.Fatalf("err = %v, want unreachable", err)
	}
}

func TestApplicationsPassesGatewayJSONNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"msg": "application not found"})
	}))
	defer srv.Close()
	m, err := New(&fakeHost{gateway: srv.URL, token: "user-token"}, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Applications(context.Background(), 1, 100)
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != http.StatusNotFound || apiErr.Message != "application not found" {
		t.Fatalf("err = %v", err)
	}
}

func TestManagerSubmitPassesGatewayErrors(t *testing.T) {
	g := newFakeGateway(t)
	m, err := New(&fakeHost{gateway: g.server.URL, token: "user-token"}, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Submit(context.Background(), SubmitRequest{ModelName: "qwen3-8b", LocalModelID: "Qwen3-8B-GGUF", PromptPrice: 1})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != http.StatusConflict || !strings.Contains(apiErr.Message, "live application") {
		t.Fatalf("err = %v", err)
	}
}

func TestManagerFollowsConfiguredGateway(t *testing.T) {
	g1, g2 := newFakeGateway(t), newFakeGateway(t)
	host := &fakeHost{gateway: g1.server.URL, token: "user-token"}
	m, err := New(host, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	sess := g1.next(t)
	<-sess.status

	// The user points ai_gateway_url at another gateway: the old tunnel
	// drains and the node registers with and connects to the new one.
	host.configure(g2.server.URL, "user-token")
	drained := false
	for msg := range sess.status {
		if msg.Type == msgDrain {
			drained = true
			break
		}
	}
	if !drained {
		t.Fatal("old tunnel closed without a drain")
	}
	<-g2.next(t).status
	if g2.registers.Load() != 1 {
		t.Fatalf("registrations on the new gateway = %d, want 1", g2.registers.Load())
	}
	if st := m.Status(); st.GatewayURL != g2.server.URL {
		t.Fatalf("status gateway = %s", st.GatewayURL)
	}

	// Switching back reuses the first gateway's credential.
	host.configure(g1.server.URL, "user-token")
	<-g1.next(t).status
	if g1.registers.Load() != 1 {
		t.Fatalf("switching back re-registered: %d registrations", g1.registers.Load())
	}
}

func TestManagerReregistersForAnotherAccount(t *testing.T) {
	g := newFakeGateway(t)
	host := &fakeHost{gateway: g.server.URL, token: "user-token-alice"}
	m, err := New(host, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	<-g.next(t).status
	base := m.Status().NodeUUID

	// Bob signs in; on this gateway the machine's identity belongs to Alice,
	// so the node takes a fresh identity there and registers as Bob's.
	g.foreignUUID.Store(base)
	host.configure(g.server.URL, "user-token-bob")
	<-g.next(t).status
	if g.registers.Load() != 2 {
		t.Fatalf("registrations = %d, want 2", g.registers.Load())
	}
	if got := m.Status().NodeUUID; got == base || got != g.lastUUID.Load().(string) {
		t.Fatalf("node uuid after account switch = %s (base %s)", got, base)
	}
}

func TestManagerMigratesSingleGatewayState(t *testing.T) {
	g := newFakeGateway(t)
	dir := t.TempDir()
	legacy := `{"enabled":true,"node_uuid":"legacy-node","node_token":"node-token-legacy","gateway_url":"` + g.server.URL + `/"}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := New(&fakeHost{gateway: g.server.URL, token: "user-token"}, dir, "studio")
	if err != nil {
		t.Fatal(err)
	}
	cred := m.state.Gateways[gatewayKey(g.server.URL)]
	if cred == nil || cred.NodeToken != "node-token-legacy" || cred.NodeUUID != "legacy-node" || m.state.NodeToken != "" {
		t.Fatalf("migrated state = %+v / %+v", m.state, cred)
	}
	m.Start()
	defer m.Stop()
	<-g.next(t).status
	// The legacy token was not bound to an account: one registration records it.
	if g.registers.Load() != 1 || g.lastUUID.Load().(string) != "legacy-node" {
		t.Fatalf("registrations = %d as %v", g.registers.Load(), g.lastUUID.Load())
	}
}

func TestBalanceAndStatementsReadCSGHubAsTheSignedInUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/edge/balance":
			_ = json.NewEncoder(w).Encode(map[string]any{"msg": "OK", "data": map[string]any{"total": 28000, "settled": 8000, "unsettled": 0, "current_month": 20000}})
		case "/api/v1/edge/statements":
			_ = json.NewEncoder(w).Encode(map[string]any{"msg": "OK", "data": []any{map[string]any{"month": "2026-09", "owner_amount": 8000, "status": "settled"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	m, err := New(&fakeHost{gateway: srv.URL, token: "user-token"}, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	data, err := m.Balance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var balance map[string]int64
	if err := json.Unmarshal(data, &balance); err != nil {
		t.Fatal(err)
	}
	if balance["total"] != 28000 || balance["settled"] != 8000 || balance["current_month"] != 20000 {
		t.Fatalf("balance = %+v", balance)
	}

	data, err = m.Statements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var statements []struct {
		Month  string `json:"month"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &statements); err != nil {
		t.Fatal(err)
	}
	if len(statements) != 1 || statements[0].Month != "2026-09" || statements[0].Status != "settled" {
		t.Fatalf("statements = %+v", statements)
	}

	m, err = New(&fakeHost{gateway: srv.URL}, t.TempDir(), "studio")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Balance(context.Background()); err == nil {
		t.Fatal("balance without a CSGHub sign-in succeeded")
	}
	if _, err := m.Statements(context.Background()); err == nil {
		t.Fatal("statements without a CSGHub sign-in succeeded")
	}
}
