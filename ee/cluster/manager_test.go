// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/inference"
)

// fakeHost is a CSGLite server stand-in: a fixed model inventory and an
// inference handler that answers with the node's name.
type fakeHost struct {
	name      string
	models    []ModelStatus
	limit     int
	licensed  bool
	served    atomic.Int32
	failNext  atomic.Int32
	localEng  inference.Engine
	mu        sync.Mutex
	statusMod func(*Status)
}

func (h *fakeHost) LocalStatus(context.Context) Status {
	st := Status{
		Hostname: h.name,
		GPUs:     []GPUStatus{{Name: "Test GPU", VRAMTotal: 24 * gb, VRAMUsed: 2 * gb, UsageKnown: true}},
		CPU:      CPUStatus{Cores: 8},
		RAM:      RAMStatus{Total: 32 * gb, Used: 4 * gb},
		Disk:     DiskStatus{Total: 500 * gb, Free: 400 * gb},
		Models:   append([]ModelStatus(nil), h.models...),
	}
	h.mu.Lock()
	if h.statusMod != nil {
		h.statusMod(&st)
	}
	h.mu.Unlock()
	return st
}

func (h *fakeHost) LocalEngine(context.Context, string, EngineOptions) (inference.Engine, error) {
	if h.localEng == nil {
		return nil, errors.New("no local engine in test")
	}
	return h.localEng, nil
}

func (h *fakeHost) InferenceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.failNext.Load() > 0 {
			h.failNext.Add(-1)
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		h.served.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "model": req["model"],
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "hello from " + h.name}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 50, "completion_tokens": 20},
		})
	})
}

func (h *fakeHost) PullHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"job-1","status":"queued"}`))
	})
}

func (h *fakeHost) NodeLimit() int  { return h.limit }
func (h *fakeHost) Licensed() bool  { return h.licensed }
func (h *fakeHost) Version() string { return "test" }
func (h *fakeHost) APIPort() int    { return 11435 }

type testNode struct {
	m    *Manager
	host *fakeHost
}

func startNode(t *testing.T, bus *MemoryBus, name string, host *fakeHost) *testNode {
	return startNodeWith(t, bus, name, host, func(*Options) {})
}

func startNodeWith(t *testing.T, bus *MemoryBus, name string, host *fakeHost, tweak func(*Options)) *testNode {
	t.Helper()
	host.name = name
	opts := Options{
		Dir:        t.TempDir(),
		Host:       host,
		Discoverer: bus.NewDiscoverer(mustAddr("127.0.0.1")),
		ListenAddr: "127.0.0.1:0",
		Logf:       func(format string, args ...any) { t.Logf(format, args...) },
	}
	tweak(&opts)
	m, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.AutoFormEnabled() {
		if err := m.Activate(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { cancel(); m.Stop() })
	_ = m.identity.SaveName(name)
	return &testNode{m: m, host: host}
}

func TestDormantByDefaultUntilActivated(t *testing.T) {
	bus := NewMemoryBus()
	host := &fakeHost{licensed: true, name: "solo"}
	m, err := New(Options{Dir: t.TempDir(), Host: host, Discoverer: bus.NewDiscoverer(mustAddr("127.0.0.1")), ListenAddr: "127.0.0.1:0", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	if m.Active() || m.ListenPort() != 0 {
		t.Fatalf("a plain node must stay dormant: active=%v port=%d", m.Active(), m.ListenPort())
	}
	// Another active node on the bus must not see the dormant one.
	other := startNode(t, bus, "other", &fakeHost{licensed: true})
	time.Sleep(200 * time.Millisecond)
	if len(other.m.dir.Discovered(time.Minute)) != 0 {
		t.Fatal("dormant node was advertised")
	}
	view := m.View(context.Background())
	if view.Active {
		t.Fatal("view claims active")
	}
	// An operator action switches it on and the choice persists.
	if _, _, err := m.CreateCluster("Lab"); err != nil {
		t.Fatal(err)
	}
	if !m.Active() || m.ListenPort() == 0 || !m.store.Settings().Enabled {
		t.Fatal("create did not activate")
	}
	waitFor(t, "other to see it", func() bool { return len(other.m.dir.Discovered(time.Minute)) == 1 })
	// Leaving on purpose makes it dormant again.
	if err := m.Leave(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Active() || m.store.Settings().Enabled {
		t.Fatal("leave did not return the node to dormant")
	}
	// Reopening the store on a node that was enabled and still a member
	// activates at start.
	dir := t.TempDir()
	m2, err := New(Options{Dir: dir, Host: host, Discoverer: bus.NewDiscoverer(mustAddr("127.0.0.1")), ListenAddr: "127.0.0.1:0", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.store.UpdateSettings(func(s *Settings) { s.Enabled = true }); err != nil {
		t.Fatal(err)
	}
	if err := m2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m2.Stop)
	if !m2.Active() {
		t.Fatal("previously enabled node did not activate at start")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestJoinWithTokenAndRouteToPeer(t *testing.T) {
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{limit: 0, licensed: true, models: []ModelStatus{{ID: "m-a", Size: gb, Loaded: true, Slots: 4}}})
	b := startNode(t, bus, "beta", &fakeHost{limit: 0, licensed: true, models: []ModelStatus{{ID: "m-b", Size: gb, Loaded: true, Slots: 4}}})

	_, token, err := a.m.CreateCluster("Lab")
	if err != nil {
		t.Fatal(err)
	}
	// b learns about a over discovery and joins with the token only.
	waitFor(t, "b to discover a", func() bool { return len(b.m.dir.Discovered(time.Minute)) == 1 })
	if _, err := b.m.Join(context.Background(), token, ""); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, ok := a.m.store.Member(b.m.identity.UUID); !ok {
		t.Fatal("a does not list b")
	}
	if _, ok := b.m.store.Member(a.m.identity.UUID); !ok {
		t.Fatal("b does not list a")
	}
	if b.m.store.JoinTokenHash() != a.m.store.JoinTokenHash() {
		t.Fatal("token hash not shared with the new member")
	}
	waitFor(t, "a to see b's status", func() bool {
		rt, ok := a.m.dir.Get(b.m.identity.UUID)
		return ok && rt.Health == HealthHealthy && rt.Status != nil
	})

	// A request for b's model from a lands on b.
	holders := a.m.RemoteHolders("m-b")
	if len(holders) != 1 || holders[0] != b.m.identity.UUID {
		t.Fatalf("remote holders %v", holders)
	}
	eng, err := a.m.ChatEngine(context.Background(), "m-b", SourceCluster, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := eng.(inference.ChatCompletionProxier).ChatCompletion(context.Background(), map[string]any{"messages": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "hello from beta") {
		t.Fatalf("unexpected answer %s", body)
	}
	if resp.Header.Get(NodeHeader) != b.m.identity.UUID || resp.Header.Get(NodeNameHeader) != "beta" {
		t.Fatalf("node headers %v", resp.Header)
	}
	// The measured body fed the perf store (the fake answers instantly, so
	// the combined rate is attributed to decoding).
	waitFor(t, "perf sample", func() bool { return a.m.perf.get(b.m.identity.UUID, "m-b") != nil })

	// The Ollama-style Chat path streams through the same route.
	var tokens []string
	text, err := eng.Chat(context.Background(), []inference.Message{{Role: "user", Content: "hi"}}, inference.DefaultOptions(), func(tok string) { tokens = append(tokens, tok) })
	if err != nil {
		t.Fatal(err)
	}
	_ = text
	if b.host.served.Load() < 2 {
		t.Fatalf("peer served %d requests", b.host.served.Load())
	}

	// Cluster-wide inventory sees both models.
	models := a.m.Models(context.Background())
	if len(models) != 2 {
		t.Fatalf("models %+v", models)
	}
	ex := a.m.Explain(context.Background(), "m-b", 0, 0, "")
	if len(ex.Order) != 1 || ex.Order[0] != b.m.identity.UUID {
		t.Fatalf("explain %+v", ex)
	}
}

func TestInviteWithAdmissionCodeAndGossip(t *testing.T) {
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{licensed: true})
	b := startNode(t, bus, "beta", &fakeHost{licensed: true})
	c := startNode(t, bus, "gamma", &fakeHost{licensed: true})
	if _, _, err := a.m.CreateCluster("Lab"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "discovery", func() bool { return len(a.m.dir.Discovered(time.Minute)) == 2 })

	// Wrong code is refused and nothing changes.
	if _, err := a.m.Invite(context.Background(), b.m.identity.UUID, "00000000", ""); err == nil {
		t.Fatal("wrong code accepted")
	}
	code, _, err := b.m.store.NodeCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.m.Invite(context.Background(), b.m.identity.UUID, code, ""); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if !b.m.store.InCluster() {
		t.Fatal("b did not adopt the cluster")
	}
	// Code was consumed: a second invite with it fails.
	if _, err := a.m.Invite(context.Background(), b.m.identity.UUID, code, ""); err == nil {
		t.Fatal("consumed code accepted")
	}

	// c joins through b (any member admits), and a learns about c by gossip.
	code, _, _ = c.m.store.NodeCode()
	if _, err := b.m.Invite(context.Background(), c.m.identity.UUID, code, ""); err != nil {
		t.Fatalf("invite via b: %v", err)
	}
	waitFor(t, "a to learn c by gossip", func() bool {
		_, ok := a.m.store.Member(c.m.identity.UUID)
		return ok
	})
	waitFor(t, "c to learn a by gossip", func() bool {
		_, ok := c.m.store.Member(a.m.identity.UUID)
		return ok
	})
	waitFor(t, "full mesh online", func() bool {
		for _, n := range []*testNode{a, b, c} {
			for _, rt := range n.m.dir.Snapshot() {
				if rt.Health != HealthHealthy {
					return false
				}
			}
			if len(n.m.dir.Snapshot()) != 2 {
				return false
			}
		}
		return true
	})

	// Removing c from a propagates: c leaves, b forgets c.
	if err := a.m.RemoveMember(context.Background(), c.m.identity.UUID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "c to leave", func() bool { return !c.m.store.InCluster() })
	waitFor(t, "b to forget c", func() bool {
		_, ok := b.m.store.Member(c.m.identity.UUID)
		return !ok
	})
	if !a.m.store.IsTombstoned(c.m.identity.UUID) {
		t.Fatal("no tombstone for the removed node")
	}
	// c, now unpaired, can be invited again explicitly.
	code, _, _ = c.m.store.NodeCode()
	if _, err := a.m.Invite(context.Background(), c.m.identity.UUID, code, ""); err != nil {
		t.Fatalf("re-invite: %v", err)
	}
}

func TestNodeLimitIsEnforcedOnJoinAndInvite(t *testing.T) {
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{limit: 2})
	b := startNode(t, bus, "beta", &fakeHost{limit: 2})
	c := startNode(t, bus, "gamma", &fakeHost{limit: 2})
	_, token, err := a.m.CreateCluster("CE")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "discovery", func() bool {
		return len(b.m.dir.Discovered(time.Minute)) >= 1 && len(c.m.dir.Discovered(time.Minute)) >= 1
	})
	if _, err := b.m.Join(context.Background(), token, ""); err != nil {
		t.Fatalf("second node must join under the community cap: %v", err)
	}
	_, err = c.m.Join(context.Background(), token, "")
	var pe *peerError
	if !errors.As(err, &pe) || pe.Status != http.StatusForbidden || pe.Body.Code != "feature_not_licensed" || pe.Body.Limit != 2 || pe.Body.Current != 2 {
		t.Fatalf("third node should hit the cap, got %v", err)
	}
	code, _, _ := c.m.store.NodeCode()
	_, err = a.m.Invite(context.Background(), c.m.identity.UUID, code, "")
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("invite past the cap should be a LimitError, got %v", err)
	}
	// Status of a member reports whether its own license covers the cluster.
	st := a.m.LocalStatus(context.Background())
	if !st.Licensed || st.NodeLimit != 2 {
		t.Fatalf("status %+v", st)
	}
}

func TestFailoverToNextNodeAndBreaker(t *testing.T) {
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{licensed: true})
	b := startNode(t, bus, "beta", &fakeHost{licensed: true, models: []ModelStatus{{ID: "m", Size: gb, Loaded: true, Slots: 4}}})
	c := startNode(t, bus, "gamma", &fakeHost{licensed: true, models: []ModelStatus{{ID: "m", Size: gb, Loaded: true, Slots: 4}}})
	_, token, _ := a.m.CreateCluster("Lab")
	waitFor(t, "discovery", func() bool {
		return len(b.m.dir.Discovered(time.Minute)) >= 1 && len(c.m.dir.Discovered(time.Minute)) >= 1
	})
	for _, n := range []*testNode{b, c} {
		if _, err := n.m.Join(context.Background(), token, ""); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "statuses", func() bool {
		ok := 0
		for _, rt := range a.m.dir.Snapshot() {
			if rt.Health == HealthHealthy && rt.Status != nil {
				ok++
			}
		}
		return ok == 2
	})
	// Force the first-ranked node to fail once: the request must still succeed
	// on the other node, and the failing node gets a model breaker.
	ranked, _ := (&clusterEngine{m: a.m, model: "m"}).rank(0, 0)
	if len(ranked) != 2 {
		t.Fatalf("ranked %+v", ranked)
	}
	first := ranked[0].UUID
	var firstHost *fakeHost
	for _, n := range []*testNode{b, c} {
		if n.m.identity.UUID == first {
			firstHost = n.host
		}
	}
	firstHost.failNext.Store(1)
	eng, _ := a.m.ChatEngine(context.Background(), "m", SourceCluster, EngineOptions{})
	resp, err := eng.(inference.ChatCompletionProxier).ChatCompletion(context.Background(), map[string]any{"messages": []any{}})
	if err != nil {
		t.Fatalf("failover did not happen: %v", err)
	}
	resp.Body.Close()
	if resp.Header.Get(NodeHeader) == first {
		t.Fatal("request served by the node that failed")
	}
	if !a.m.dir.ModelBroken(first, "m") {
		t.Fatal("model breaker not opened for the failing node")
	}
	// Pinning a node that lacks the model is a clear error.
	pinned, err := a.m.ChatEngine(context.Background(), "nope", SourceNodePrefix+b.m.identity.UUID, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pinned.(inference.ChatCompletionProxier).ChatCompletion(context.Background(), map[string]any{}); err == nil || inference.HTTPStatusCode(err) != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for a pinned node without the model, got %v", err)
	}
	// Explain lists the broken node as excluded.
	ex := a.m.Explain(context.Background(), "m", 0, 0, "")
	for _, cand := range ex.Candidates {
		if cand.UUID == first && cand.Eligible {
			t.Fatal("broken node still eligible")
		}
	}
}

func TestSessionAffinitySticksToOneNode(t *testing.T) {
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{licensed: true})
	b := startNode(t, bus, "beta", &fakeHost{licensed: true, models: []ModelStatus{{ID: "m", Size: gb, Loaded: true, Slots: 4}}})
	c := startNode(t, bus, "gamma", &fakeHost{licensed: true, models: []ModelStatus{{ID: "m", Size: gb, Loaded: true, Slots: 4}}})
	_, token, _ := a.m.CreateCluster("Lab")
	waitFor(t, "discovery", func() bool {
		return len(b.m.dir.Discovered(time.Minute)) >= 1 && len(c.m.dir.Discovered(time.Minute)) >= 1
	})
	for _, n := range []*testNode{b, c} {
		if _, err := n.m.Join(context.Background(), token, ""); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "statuses", func() bool {
		ok := 0
		for _, rt := range a.m.dir.Snapshot() {
			if rt.Health == HealthHealthy && rt.Status != nil {
				ok++
			}
		}
		return ok == 2
	})
	ctx := WithAffinityKey(context.Background(), "thread:t1")
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		eng, _ := a.m.ChatEngine(ctx, "m", SourceCluster, EngineOptions{})
		resp, err := eng.(inference.ChatCompletionProxier).ChatCompletion(ctx, map[string]any{"messages": []any{}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		seen[resp.Header.Get(NodeHeader)]++
	}
	if len(seen) != 1 {
		t.Fatalf("affinity broken across %v", seen)
	}
	// A different thread may land elsewhere but stays consistent too.
	other := WithAffinityKey(context.Background(), "thread:t2")
	eng, _ := a.m.ChatEngine(other, "m", SourceCluster, EngineOptions{})
	resp, err := eng.(inference.ChatCompletionProxier).ChatCompletion(other, map[string]any{"messages": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestPeerListenerRefusesUnpairedCallers(t *testing.T) {
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{licensed: true, models: []ModelStatus{{ID: "m", Size: gb, Loaded: true}}})
	stranger := startNode(t, bus, "stranger", &fakeHost{licensed: true})
	if _, _, err := a.m.CreateCluster("Lab"); err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", a.m.ListenPort())
	client := peerClient(stranger.m.identity, "", "")
	// Full status and the forwarded inference path are cluster data.
	for _, path := range []string{peerPathStatus, peerPathInference + "v1/chat/completions"} {
		req, _ := http.NewRequest(http.MethodPost, "https://"+addr+path, strings.NewReader("{}"))
		if path == peerPathStatus {
			req.Method = http.MethodGet
		}
		req.Header.Set(RoutedHeader, "x")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s answered %d to an unpaired node", path, resp.StatusCode)
		}
	}
	// The public status carries identity only.
	st, err := stranger.m.fetchStatusUnpinned(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	if st.UUID != a.m.identity.UUID || len(st.Models) != 0 || len(st.GPUs) != 0 {
		t.Fatalf("public status leaked cluster data: %+v", st)
	}
	// A bad token is refused with 401 and the stranger is not added.
	_, err = stranger.m.Join(context.Background(), "csgl1-"+a.m.store.Cluster().UUID+"-wrongsecret", addr)
	var pe *peerError
	if !errors.As(err, &pe) || pe.Status != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %v", err)
	}
	if len(a.m.store.Members()) != 0 {
		t.Fatal("stranger was added")
	}
}

func TestLeaveAndStaticAddressRecovery(t *testing.T) {
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{licensed: true})
	b := startNode(t, bus, "beta", &fakeHost{licensed: true})
	_, token, _ := a.m.CreateCluster("Lab")
	waitFor(t, "discovery", func() bool { return len(b.m.dir.Discovered(time.Minute)) >= 1 })
	if _, err := b.m.Join(context.Background(), token, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a sees b", func() bool { rt, ok := a.m.dir.Get(b.m.identity.UUID); return ok && rt.Health == HealthHealthy })
	// Operator pins a static address; it is tracked as a candidate.
	if _, err := a.m.UpdateSettings(func(s *Settings) { s.StaticAddresses[b.m.identity.UUID] = "127.0.0.1" }); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range a.m.dir.Candidates(b.m.identity.UUID) {
		if c == fmt.Sprintf("127.0.0.1:%d", b.m.ListenPort()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("static address not among candidates: %v", a.m.dir.Candidates(b.m.identity.UUID))
	}
	if err := b.m.Leave(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a to drop b", func() bool { _, ok := a.m.store.Member(b.m.identity.UUID); return !ok })
	if b.m.store.InCluster() {
		t.Fatal("b still clustered")
	}
	// b's identity survives leaving.
	if b.m.identity.UUID == "" {
		t.Fatal("identity lost")
	}
	view := a.m.View(context.Background())
	if len(view.Members) != 1 || !view.Members[0].Local {
		t.Fatalf("view %+v", view.Members)
	}
	sum := a.m.Summary(context.Background())
	if !sum.InCluster || sum.NodeCount != 1 || sum.Nodes[0].GPUName != "Test GPU" {
		t.Fatalf("summary %+v", sum)
	}
}

func TestHTTPHandlersViaRecorder(t *testing.T) {
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{licensed: true, models: []ModelStatus{{ID: "m", Size: gb, Loaded: true}}})
	rec := httptest.NewRecorder()
	a.m.HandleCode(rec, httptest.NewRequest(http.MethodGet, "/api/cluster/code", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	a.m.HandleCreate(rec, httptest.NewRequest(http.MethodPost, "/api/cluster", strings.NewReader(`{"name":"Lab"}`)))
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "csgl1-") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	a.m.HandleCode(rec, httptest.NewRequest(http.MethodGet, "/api/cluster/code", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("code while clustered: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	a.m.HandleSettingsUpdate(rec, httptest.NewRequest(http.MethodPut, "/api/cluster/settings", strings.NewReader(`{"routing_mode":"balanced","weight":50}`)))
	if rec.Code != http.StatusOK || a.m.store.Settings().RoutingMode != RoutingBalanced || a.m.store.Settings().Weight != 50 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	a.m.HandleSettingsUpdate(rec, httptest.NewRequest(http.MethodPut, "/api/cluster/settings", strings.NewReader(`{"routing_mode":"nope"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad routing mode accepted: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	a.m.HandleModelSync(rec, httptest.NewRequest(http.MethodPost, "/api/cluster/models/sync", strings.NewReader(`{"model":"new-model","nodes":"all"}`)))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"job-1"`) {
		t.Fatalf("sync: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	a.m.HandleExplain(rec, httptest.NewRequest(http.MethodGet, "/api/cluster/explain?model=m", nil))
	var ex Explain
	if err := json.Unmarshal(rec.Body.Bytes(), &ex); err != nil || len(ex.Order) != 1 {
		t.Fatalf("explain: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	a.m.HandleLeave(rec, httptest.NewRequest(http.MethodDelete, "/api/cluster", nil))
	if rec.Code != http.StatusOK || a.m.store.InCluster() {
		t.Fatalf("leave: %d %s", rec.Code, rec.Body)
	}
}

func TestPerfRates(t *testing.T) {
	start := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	// Streamed: 1s to first byte over 400 prompt tokens, 4s generating 200 tokens.
	d, p := perfRates(start, perfSample{ok: true, firstByte: start.Add(time.Second), end: start.Add(5 * time.Second), promptTokens: 400, completionTokens: 200})
	if d < 49 || d > 51 || p < 399 || p > 401 {
		t.Fatalf("streamed rates %.1f/%.1f", d, p)
	}
	// Whole answer after 2s: only a combined decode rate.
	d, p = perfRates(start, perfSample{ok: true, firstByte: start.Add(2 * time.Second), end: start.Add(2*time.Second + time.Millisecond), promptTokens: 400, completionTokens: 100})
	if d < 49 || d > 51 || p != 0 {
		t.Fatalf("non-streamed rates %.1f/%.1f", d, p)
	}
	if d, p := perfRates(start, perfSample{ok: false}); d != 0 || p != 0 {
		t.Fatal("failed request produced a sample")
	}
}

func shortAutoForm(t *testing.T, grace, jitter, interval time.Duration) {
	t.Helper()
	g, j, i := autoFormGrace, autoFormMaxJitter, autoFormInterval
	autoFormGrace, autoFormMaxJitter, autoFormInterval = grace, jitter, interval
	t.Cleanup(func() { autoFormGrace, autoFormMaxJitter, autoFormInterval = g, j, i })
}

func TestDeriveAutoFormIsStableAndSecretNeverAppears(t *testing.T) {
	uuid1, token1, err := DeriveAutoForm("lab-shared-secret")
	if err != nil {
		t.Fatal(err)
	}
	uuid2, token2, _ := DeriveAutoForm("lab-shared-secret")
	if uuid1 != uuid2 || token1 != token2 {
		t.Fatal("derivation is not deterministic")
	}
	other, _, _ := DeriveAutoForm("another-secret!")
	if other == uuid1 {
		t.Fatal("different secrets derive the same cluster")
	}
	if strings.Contains(token1, "lab-shared-secret") {
		t.Fatal("the shared secret must not appear in the token")
	}
	if cu, _, err := ParseJoinToken(token1); err != nil || cu != uuid1 {
		t.Fatalf("derived token does not parse to the derived cluster: %v", err)
	}
	if _, _, err := DeriveAutoForm("abc"); err == nil {
		t.Fatal("near-empty secret accepted")
	}
	if _, _, err := DeriveAutoForm("james"); err != nil {
		t.Fatalf("a short but deliberate secret must be accepted: %v", err)
	}
}

func TestAutoFormJoinsWithoutAnyCommand(t *testing.T) {
	shortAutoForm(t, 400*time.Millisecond, 200*time.Millisecond, 200*time.Millisecond)
	bus := NewMemoryBus()
	withSecret := func(secret string) func(*Options) {
		return func(o *Options) { o.AutoFormSecret = secret; o.AutoFormName = "Lab" }
	}
	a := startNodeWith(t, bus, "alpha", &fakeHost{licensed: true}, withSecret("lab-shared-secret"))
	// b starts after a has had time to found the cluster.
	waitFor(t, "a to found", func() bool { return a.m.store.InCluster() })
	b := startNodeWith(t, bus, "beta", &fakeHost{licensed: true}, withSecret("lab-shared-secret"))
	stranger := startNodeWith(t, bus, "gamma", &fakeHost{licensed: true}, withSecret("a-different-secret"))
	waitFor(t, "b to join a", func() bool {
		_, ok := a.m.store.Member(b.m.identity.UUID)
		return ok && b.m.store.InCluster()
	})
	if c := a.m.store.Cluster(); c.Name != "Lab" {
		t.Fatalf("cluster name %q", c.Name)
	}
	derived, _, _ := DeriveAutoForm("lab-shared-secret")
	if a.m.store.Cluster().UUID != derived || b.m.store.Cluster().UUID != derived {
		t.Fatal("cluster uuid is not the derived one")
	}
	time.Sleep(600 * time.Millisecond)
	if !stranger.m.store.InCluster() || stranger.m.store.Cluster().UUID == derived {
		t.Fatal("a node with another secret must form its own cluster, not join this one")
	}
	if _, ok := a.m.store.Member(stranger.m.identity.UUID); ok {
		t.Fatal("stranger was admitted")
	}
	// An explicit leave pauses automatic formation; it does not snap back.
	if err := b.m.Leave(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if b.m.store.InCluster() {
		t.Fatal("automatic formation re-joined a node that was told to leave")
	}
	view := b.m.View(context.Background())
	if !view.AutoForm || !view.AutoFormPaused {
		t.Fatalf("view %+v", view)
	}
	// Joining again (any way) resumes it.
	_, token, _ := DeriveAutoForm("lab-shared-secret")
	if _, err := b.m.Join(context.Background(), token, ""); err != nil {
		t.Fatal(err)
	}
	if b.m.store.Settings().AutoFormPaused {
		t.Fatal("pause not cleared by an explicit join")
	}
}

func TestAutoFormMergesClustersFoundedInParallel(t *testing.T) {
	// No grace and no jitter: both nodes found a one-node cluster at once.
	shortAutoForm(t, 0, 0, 200*time.Millisecond)
	bus := NewMemoryBus()
	secret := func(o *Options) { o.AutoFormSecret = "lab-shared-secret" }
	a := startNodeWith(t, bus, "alpha", &fakeHost{licensed: true}, secret)
	b := startNodeWith(t, bus, "beta", &fakeHost{licensed: true}, secret)
	waitFor(t, "both to found", func() bool { return a.m.store.InCluster() && b.m.store.InCluster() })
	waitFor(t, "the two clusters to merge", func() bool {
		_, ab := a.m.store.Member(b.m.identity.UUID)
		_, ba := b.m.store.Member(a.m.identity.UUID)
		return ab && ba
	})
	if a.m.store.Cluster().UUID != b.m.store.Cluster().UUID {
		t.Fatal("merged nodes disagree on the cluster uuid")
	}
	waitFor(t, "healthy mesh", func() bool {
		ra, _ := a.m.dir.Get(b.m.identity.UUID)
		rb, _ := b.m.dir.Get(a.m.identity.UUID)
		return ra.Health == HealthHealthy && rb.Health == HealthHealthy
	})
}
