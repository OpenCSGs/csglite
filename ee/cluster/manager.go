// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/inference"
)

// DefaultListenAddr is the node-to-node listener; every node may use a
// different port, the discovery record carries the real one.
const DefaultListenAddr = ":11438"

// Host is what the manager needs from the CSGLite server it runs inside.
type Host interface {
	// LocalStatus reports this node's models, hardware and load. The
	// manager fills in identity and cluster fields.
	LocalStatus(ctx context.Context) Status
	// LocalEngine returns the local inference engine for a model so the
	// scheduler can pick this node like any other. opts.Kind says which
	// engine the request needs: a node answers chat and embedding requests
	// from different processes started with different flags, so a locally
	// placed embedding request served by a chat engine would reach a
	// llama-server that was never given --embeddings.
	LocalEngine(ctx context.Context, modelID string, opts EngineOptions) (inference.Engine, error)
	// InferenceHandler serves a forwarded request on this node only.
	InferenceHandler() http.Handler
	// PullHandler creates a pull job on this node (POST /api/pull/jobs body).
	PullHandler() http.Handler
	// ModelBundle describes a complete local model for a peer to copy, or
	// returns an error when the model is absent or still downloading.
	ModelBundle(modelID string) (*ModelBundle, error)
	// PullSpec turns a cluster model id into the repository and artifact
	// source a pull job needs: registry-prefixed ids such as
	// "modelscope/Qwen/Qwen3.5-2B" split into ("Qwen/Qwen3.5-2B", "modelscope").
	PullSpec(modelID string) (repo, artifactSource string)
	// NodeLimit is the licensed member cap including this node; 0 = unlimited.
	NodeLimit() int
	// Licensed reports whether an enterprise license is in effect.
	Licensed() bool
	Version() string
	APIPort() int
}

// EngineKind names the kind of inference engine a routed request needs. It
// travels with the request because the node that ends up serving it has to
// load the matching engine, and only the caller knows which one was asked for.
type EngineKind string

const (
	// EngineChat is the default: chat, completion and messages requests.
	EngineChat EngineKind = ""
	// EngineEmbedding is an embeddings request, served by a process started
	// in embedding mode.
	EngineEmbedding EngineKind = "embedding"
)

// EngineOptions are the runtime overrides a request may carry.
type EngineOptions struct {
	// Kind selects the engine a request needs. The zero value is EngineChat
	// so callers that only ever wanted chat keep working unchanged.
	Kind        EngineKind
	NumCtx      int
	NumParallel int
	NGPULayers  int
	CacheTypeK  string
	CacheTypeV  string
	DType       string
}

// Options configure a Manager.
type Options struct {
	Dir        string
	Host       Host
	Discoverer Discoverer
	ListenAddr string
	// Seeds are static cluster endpoints (host:port) to probe when
	// multicast is unavailable.
	Seeds []string
	// JoinToken, when set and the node is unpaired, joins automatically at
	// startup (CSGHUB_LITE_CLUSTER_JOIN_TOKEN).
	JoinToken string
	// AutoFormSecret, when set, forms the cluster automatically: every node
	// provisioned with the same secret founds or joins the same derived
	// cluster with no create or join step (see autoform.go). AutoFormName is
	// the display name the founding node gives it.
	AutoFormSecret string
	AutoFormName   string
	// AdvertiseHost, when set, is the host name or IP published to peers
	// instead of the address the peer observed (for NAT-free but multi-homed
	// boxes that should be reached on one interface).
	AdvertiseHost string
	Logf          func(format string, args ...any)
}

// Manager runs the cluster on one node.
type Manager struct {
	opts     Options
	logf     func(string, ...any)
	identity *Identity
	store    *Store
	dir      *Directory
	affinity *Affinity
	perf     *perfStore
	peers    *peerClientCache

	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	runCtx      context.Context
	runCancel   context.CancelFunc
	active      bool
	listenPort  int
	peerServer  *http.Server
	startedAt   time.Time
	started     bool
	lastGossip  time.Time
	gossipWake  chan struct{}
	discoverer  Discoverer
	observedMu  sync.Mutex
	observedIPs map[string]time.Time
	statusMu    sync.Mutex
	statusCache *Status
	statusAt    time.Time
	rejectMu    sync.Mutex
	rejectedBy  map[string]time.Time
}

// New prepares a manager; Start opens the listener and begins discovery.
func New(opts Options) (*Manager, error) {
	if opts.Host == nil {
		return nil, errors.New("cluster: Host is required")
	}
	if opts.Dir == "" {
		return nil, errors.New("cluster: Dir is required")
	}
	if opts.ListenAddr == "" {
		opts.ListenAddr = DefaultListenAddr
	}
	if opts.Discoverer == nil {
		opts.Discoverer = NewMDNSDiscoverer()
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	id, err := LoadOrCreateIdentity(opts.Dir, defaultNodeName())
	if err != nil {
		return nil, err
	}
	if name := id.DisplayName(); name == genericNodeName || strings.HasPrefix(name, "csglite-node-") {
		// Several appliances with IP-style host names would otherwise all be
		// called the same; a short UUID prefix tells them apart without
		// wrapping in tables. Names from the earlier long scheme are shortened.
		_ = id.SaveName(defaultShortName(id.UUID))
	}
	store, err := OpenStore(opts.Dir)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		opts:        opts,
		logf:        logf,
		identity:    id,
		store:       store,
		dir:         NewDirectory(),
		affinity:    NewAffinity(),
		perf:        newPerfStore(filepath.Join(opts.Dir, "perf.json")),
		peers:       newPeerClientCache(id),
		gossipWake:  make(chan struct{}, 1),
		discoverer:  opts.Discoverer,
		observedIPs: map[string]time.Time{},
	}
	for _, mem := range store.Members() {
		m.dir.Track(mem.UUID, m.seedAddressesFor(mem))
	}
	return m, nil
}

// Identity returns this node's identity.
func (m *Manager) Identity() *Identity { return m.identity }

// Store exposes the membership store (read-mostly; mutations go through the
// manager so peers are told).
func (m *Manager) Store() *Store { return m.store }

// Directory exposes the live member view.
func (m *Manager) Directory() *Directory { return m.dir }

// forgetNode drops everything this node holds about a former member: its live
// health and addresses, and the pooled connections to it. Forgetting one
// without the other would leave a client trusting a certificate for a node
// that is no longer a member.
func (m *Manager) forgetNode(nodeUUID string) {
	m.dir.Forget(nodeUUID)
	m.peers.forget(nodeUUID)
}

// ListenPort is the bound cluster port.
func (m *Manager) ListenPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listenPort
}

// Start prepares the manager. Networking stays off ("dormant") on a plain
// single machine: no listener, no multicast, no polling goroutines. It is
// switched on (Activate) when the node was provisioned with a shared secret
// or a join token, already belongs to a cluster, was enabled before, or when
// an operator performs a cluster action.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.startedAt = time.Now()
	m.started = true
	m.mu.Unlock()
	if m.shouldActivateAtStart() {
		return m.Activate()
	}
	m.logf("cluster: node %s (%s) is dormant; no cluster secret, token or membership configured", shortUUID(m.identity.UUID), m.identity.DisplayName())
	return nil
}

func (m *Manager) shouldActivateAtStart() bool {
	return m.AutoFormEnabled() || strings.TrimSpace(m.opts.JoinToken) != "" || m.store.InCluster() || m.store.Settings().Enabled
}

// Active reports whether the listener and discovery are running.
func (m *Manager) Active() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// Activate opens the peer listener, starts discovery and the background
// loops, and remembers the choice so the node comes up active next time.
// It is idempotent.
func (m *Manager) Activate() error {
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return nil
	}
	if m.ctx == nil {
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.started = true
	}
	runCtx, runCancel := context.WithCancel(m.ctx)
	ln, srv, err := listenPeer(m.opts.ListenAddr, m.identity, m.Peers().peerMux())
	if err != nil {
		runCancel()
		m.mu.Unlock()
		return fmt.Errorf("cluster listener on %s: %w", m.opts.ListenAddr, err)
	}
	m.runCtx, m.runCancel = runCtx, runCancel
	m.listenPort = ln.Addr().(*net.TCPAddr).Port
	m.peerServer = srv
	m.active = true
	m.mu.Unlock()

	if !m.store.Settings().Enabled {
		_, _ = m.store.UpdateSettings(func(s *Settings) { s.Enabled = true })
	}
	go func() {
		if err := serveTLS(runCtx, srv, ln); err != nil {
			m.logf("cluster: peer listener stopped: %v", err)
		}
	}()
	if err := m.discoverer.Start(runCtx, m.announcement(), m.onObservation); err != nil {
		m.logf("cluster: discovery unavailable (%v); relying on static addresses and gossip", err)
	}
	for _, seed := range m.opts.Seeds {
		m.probeSeed(seed)
	}
	go m.pollLoop()
	go m.gossipLoop()
	go m.discoveredRefreshLoop()
	if token := strings.TrimSpace(m.opts.JoinToken); token != "" && !m.store.InCluster() {
		go m.autoJoin(token)
	}
	if m.AutoFormEnabled() {
		go m.autoFormLoop()
	}
	m.logf("cluster: node %s (%s) listening on :%d", shortUUID(m.identity.UUID), m.identity.DisplayName(), m.listenPort)
	return nil
}

// Deactivate closes the listener and discovery and stops every loop, putting
// the node back to dormant. It refuses while the node is a cluster member.
func (m *Manager) Deactivate() error {
	if m.store.InCluster() {
		return errors.New("leave the cluster before disabling the cluster feature")
	}
	m.deactivate()
	_, _ = m.store.UpdateSettings(func(s *Settings) { s.Enabled = false })
	m.logf("cluster: node %s is dormant", shortUUID(m.identity.UUID))
	return nil
}

func (m *Manager) deactivate() {
	m.mu.Lock()
	if !m.active {
		m.mu.Unlock()
		return
	}
	cancel := m.runCancel
	m.active = false
	m.listenPort = 0
	m.peerServer = nil
	m.runCancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = m.discoverer.Close()
	m.dir.Reset()
	m.peers.closeAll()
	m.invalidateLocalStatus()
}

// Stop shuts the manager down for process exit.
func (m *Manager) Stop() {
	m.deactivate()
	m.mu.Lock()
	cancel := m.cancel
	m.started = false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.perf.flush()
}

// context is the lifetime of the current activation (or the manager when
// dormant), which every background loop watches.
func (m *Manager) context() context.Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.runCtx != nil && m.active {
		return m.runCtx
	}
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

// ---- local status ----

// ---- peer HTTP client ----

// ---- background loops ----

const (
	discoveredRefreshInterval = 20 * time.Second
	// discoveredMaxAge is how long an unpaired node stays listed after its
	// last successful observation or probe.
	discoveredMaxAge = 3 * time.Minute
)

// ---- membership operations ----

// LimitError reports a refused pairing because of the node cap.
type LimitError struct {
	Limit   int
	Current int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("cluster is limited to %d nodes by the license (currently %d)", e.Limit, e.Current)
}
