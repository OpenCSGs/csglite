// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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
	// scheduler can pick this node like any other.
	LocalEngine(ctx context.Context, modelID string, opts EngineOptions) (inference.Engine, error)
	// InferenceHandler serves a forwarded request on this node only.
	InferenceHandler() http.Handler
	// PullHandler creates a pull job on this node (POST /api/pull/jobs body).
	PullHandler() http.Handler
	// NodeLimit is the licensed member cap including this node; 0 = unlimited.
	NodeLimit() int
	// Licensed reports whether an enterprise license is in effect.
	Licensed() bool
	Version() string
	APIPort() int
}

// EngineOptions are the runtime overrides a request may carry.
type EngineOptions struct {
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

	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
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
	if id.Name == genericNodeName {
		// Several appliances with IP-style host names would otherwise all be
		// called the same; the UUID prefix tells them apart in every list.
		_ = id.SaveName(genericNodeName + "-" + shortUUID(id.UUID))
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

// ListenPort is the bound cluster port.
func (m *Manager) ListenPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listenPort
}

// Start opens the peer listener, starts discovery and the background loops.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.startedAt = time.Now()
	ln, srv, err := listenPeer(m.opts.ListenAddr, m.identity, m.peerMux())
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("cluster listener on %s: %w", m.opts.ListenAddr, err)
	}
	m.listenPort = ln.Addr().(*net.TCPAddr).Port
	m.peerServer = srv
	m.started = true
	m.mu.Unlock()

	go func() {
		if err := serveTLS(m.ctx, srv, ln); err != nil {
			m.logf("cluster: peer listener stopped: %v", err)
		}
	}()
	if err := m.discoverer.Start(m.ctx, m.announcement(), m.onObservation); err != nil {
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
	m.logf("cluster: node %s (%s) listening on :%d", shortUUID(m.identity.UUID), m.identity.Name, m.listenPort)
	return nil
}

// Stop shuts the manager down. It announces "not accepting work" first so
// peers stop routing here while in-flight requests finish.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	m.started = false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = m.discoverer.Close()
	m.perf.flush()
}

func (m *Manager) context() context.Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

func (m *Manager) announcement() Announcement {
	a := Announcement{
		UUID:        m.identity.UUID,
		Name:        m.identity.Name,
		Version:     m.opts.Host.Version(),
		Protocol:    ProtocolVersion,
		ClusterPort: m.ListenPort(),
		APIPort:     m.opts.Host.APIPort(),
		Licensed:    m.opts.Host.Licensed(),
	}
	if c := m.store.Cluster(); c != nil {
		a.ClusterUUID = c.UUID
	}
	return a
}

func (m *Manager) reannounce() {
	if err := m.discoverer.Update(m.announcement()); err != nil {
		m.logf("cluster: re-announce: %v", err)
	}
}

func (m *Manager) onObservation(obs Observation) {
	if obs.Protocol != 0 && obs.Protocol != ProtocolVersion {
		return
	}
	if m.store.IsTombstoned(obs.UUID) {
		return
	}
	m.dir.Observe(obs, func(id string) bool { _, ok := m.store.Member(id); return ok })
}

// probeSeed asks a static endpoint who it is; a member's address is learned
// and an unpaired node is listed as discovered.
func (m *Manager) probeSeed(seed string) {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return
	}
	if _, _, err := net.SplitHostPort(seed); err != nil {
		seed = net.JoinHostPort(seed, strconv.Itoa(portOf(DefaultListenAddr)))
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.context(), 5*time.Second)
		defer cancel()
		st, err := m.fetchStatusUnpinned(ctx, seed)
		if err != nil || st.UUID == m.identity.UUID {
			return
		}
		addr, _ := netip.ParseAddr(endpointHost(seed))
		m.onObservation(Observation{
			Announcement: Announcement{UUID: st.UUID, ClusterUUID: st.ClusterUUID, Name: st.Name, Version: st.Version,
				Protocol: st.Protocol, ClusterPort: portOf(seed), APIPort: st.APIPort, Licensed: st.Licensed},
			Addr: addr, Seen: time.Now(), Source: "seed",
		})
	}()
}

func portOf(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

// seedAddressesFor lists the endpoints worth trying for a member before
// discovery answers: the operator's static address, then recent successes.
func (m *Manager) seedAddressesFor(mem Member) []string {
	var seeds []string
	if static := m.store.Settings().StaticAddresses[mem.UUID]; static != "" {
		if _, _, err := net.SplitHostPort(static); err != nil {
			port := mem.ClusterPort
			if port == 0 {
				port = portOf(DefaultListenAddr)
			}
			static = net.JoinHostPort(static, strconv.Itoa(port))
		}
		seeds = append(seeds, static)
	}
	seeds = append(seeds, mem.LastAddresses...)
	return seeds
}

// ---- local status ----

// LocalStatus is this node's full status as peers see it.
func (m *Manager) LocalStatus(ctx context.Context) Status {
	st := m.opts.Host.LocalStatus(ctx)
	settings := m.store.Settings()
	st.UUID = m.identity.UUID
	st.Name = m.identity.Name
	st.Version = m.opts.Host.Version()
	st.Protocol = ProtocolVersion
	if c := m.store.Cluster(); c != nil {
		st.ClusterUUID = c.UUID
	}
	st.NodeLimit = m.opts.Host.NodeLimit()
	st.Licensed = m.withinNodeLimit(len(m.store.Members()) + 1)
	st.AcceptWork = settings.AcceptWork && settings.State == NodeStateActive
	st.State = settings.State
	st.Weight = settings.Weight
	st.APIPort = m.opts.Host.APIPort()
	st.ClusterPort = m.ListenPort()
	if st.Hostname == "" {
		st.Hostname, _ = hostnameShort()
	}
	st.OS = runtime.GOOS
	st.Arch = runtime.GOARCH
	if !m.startedAt.IsZero() {
		st.UptimeSec = int64(time.Since(m.startedAt).Seconds())
	}
	st.Time = time.Now().UTC()
	st.Net.Addrs = m.localAddrs()
	// Merge this node's own perf samples so peers see them in status.
	for i := range st.Models {
		if p := m.perf.get(m.identity.UUID, st.Models[i].ID); p != nil {
			merged := p
			if st.Models[i].Perf != nil && st.Models[i].Perf.LoadSeconds > 0 {
				merged.LoadSeconds = st.Models[i].Perf.LoadSeconds
			}
			st.Models[i].Perf = merged
		}
	}
	if st.Models == nil {
		st.Models = []ModelStatus{}
	}
	if st.GPUs == nil {
		st.GPUs = []GPUStatus{}
	}
	return st
}

// withinNodeLimit reports whether a cluster of n nodes fits this node's cap.
func (m *Manager) withinNodeLimit(n int) bool {
	limit := m.opts.Host.NodeLimit()
	return limit <= 0 || n <= limit
}

// NodeLimit returns the licensed cap (0 = unlimited).
func (m *Manager) NodeLimit() int { return m.opts.Host.NodeLimit() }

func (m *Manager) localAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	sort.Strings(out)
	return out
}

func (m *Manager) card() nodeCard {
	return nodeCard{
		UUID:        m.identity.UUID,
		Name:        m.identity.Name,
		CertPEM:     m.identity.CertPEM(),
		Fingerprint: m.identity.Fingerprint(),
		APIPort:     m.opts.Host.APIPort(),
		ClusterPort: m.ListenPort(),
		Addresses:   m.advertisedEndpoints(),
		Version:     m.opts.Host.Version(),
		Protocol:    ProtocolVersion,
	}
}

// advertisedEndpoints are cluster endpoints peers may try for this node:
// the configured advertise host first, then addresses peers have actually
// reached us on, then every local IPv4.
func (m *Manager) advertisedEndpoints() []string {
	port := m.ListenPort()
	seen := map[string]bool{}
	var out []string
	add := func(host string) {
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		out = append(out, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	add(strings.TrimSpace(m.opts.AdvertiseHost))
	if host := endpointHost(m.opts.ListenAddr); host != "" {
		if ip, err := netip.ParseAddr(host); err == nil && !ip.IsUnspecified() {
			// Bound to one address: that is the only one peers can use.
			add(host)
			return out
		}
	}
	m.observedMu.Lock()
	type obs struct {
		ip string
		at time.Time
	}
	var observed []obs
	for ip, at := range m.observedIPs {
		if time.Since(at) < 10*time.Minute {
			observed = append(observed, obs{ip, at})
		} else {
			delete(m.observedIPs, ip)
		}
	}
	m.observedMu.Unlock()
	sort.Slice(observed, func(i, j int) bool { return observed[i].at.After(observed[j].at) })
	for _, o := range observed {
		add(o.ip)
	}
	for _, ip := range m.localAddrs() {
		add(ip)
	}
	return out
}

func (m *Manager) noteObservedIP(ip string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	if parsed, err := netip.ParseAddr(ip); err != nil || parsed.IsLoopback() || parsed.IsUnspecified() {
		return
	}
	m.observedMu.Lock()
	m.observedIPs[ip] = time.Now()
	m.observedMu.Unlock()
}

func (m *Manager) memberFromCard(c nodeCard) (Member, error) {
	if c.UUID == "" {
		return Member{}, errors.New("node card has no uuid")
	}
	fp := c.Fingerprint
	if c.CertPEM != "" {
		cert, err := ParseCertPEM(c.CertPEM, c.UUID)
		if err != nil {
			return Member{}, err
		}
		fp = CertFingerprint(cert)
		if c.Fingerprint != "" && c.Fingerprint != fp {
			return Member{}, fmt.Errorf("node %s: certificate does not match its fingerprint", shortUUID(c.UUID))
		}
	}
	if fp == "" {
		return Member{}, fmt.Errorf("node %s: no certificate", shortUUID(c.UUID))
	}
	return Member{
		UUID:            c.UUID,
		Name:            c.Name,
		CertFingerprint: fp,
		CertPEM:         c.CertPEM,
		APIPort:         c.APIPort,
		ClusterPort:     c.ClusterPort,
		LastAddresses:   append([]string(nil), c.Addresses...),
	}, nil
}

func (m *Manager) cardFromMember(mem Member) nodeCard {
	addrs := m.dir.Candidates(mem.UUID)
	if len(addrs) == 0 {
		addrs = append([]string(nil), mem.LastAddresses...)
	}
	return nodeCard{
		UUID:        mem.UUID,
		Name:        mem.Name,
		CertPEM:     mem.CertPEM,
		Fingerprint: mem.CertFingerprint,
		APIPort:     mem.APIPort,
		ClusterPort: mem.ClusterPort,
		Addresses:   addrs,
		Protocol:    ProtocolVersion,
	}
}

func (m *Manager) view() clusterView {
	v := clusterView{JoinTokenHash: m.store.JoinTokenHash(), Tombstones: m.store.Tombstones()}
	if c := m.store.Cluster(); c != nil {
		v.Cluster = *c
	}
	v.Members = append(v.Members, m.card())
	for _, mem := range m.store.Members() {
		v.Members = append(v.Members, m.cardFromMember(mem))
	}
	return v
}

// ---- peer HTTP client ----

type peerError struct {
	Status int
	Body   errorResponse
}

func (e *peerError) Error() string {
	if e.Body.Error != "" {
		return fmt.Sprintf("peer answered %d: %s", e.Status, e.Body.Error)
	}
	return fmt.Sprintf("peer answered %d", e.Status)
}

// peerJSON posts (or gets) JSON to a pinned member at addr.
func (m *Manager) peerJSON(ctx context.Context, mem Member, addr, method, path string, in, out any) error {
	client := peerClient(m.identity, mem.UUID, mem.CertFingerprint)
	defer client.CloseIdleConnections()
	return doJSON(ctx, client, addr, method, path, in, out)
}

func doJSON(ctx context.Context, client *http.Client, addr, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+addr+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		pe := &peerError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, &pe.Body)
		if pe.Body.Error == "" {
			pe.Body.Error = strings.TrimSpace(string(raw))
		}
		return pe
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// fetchStatusUnpinned reads a node's status without a pin (seed probing,
// unpaired nodes). Only non-sensitive fields are trusted from it.
func (m *Manager) fetchStatusUnpinned(ctx context.Context, addr string) (*Status, error) {
	client := peerClient(m.identity, "", "")
	defer client.CloseIdleConnections()
	var st Status
	if err := doJSON(ctx, client, addr, http.MethodGet, peerPathStatus+"?public=1", nil, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// fetchStatus polls a member, trying each known endpoint in turn.
func (m *Manager) fetchStatus(ctx context.Context, mem Member) (*Status, string, error) {
	addrs := m.dir.Candidates(mem.UUID)
	for _, a := range m.seedAddressesFor(mem) {
		if !containsString(addrs, a) {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		return nil, "", errors.New("no known address")
	}
	var lastErr error
	for _, addr := range addrs {
		attempt, cancel := context.WithTimeout(ctx, 6*time.Second)
		var st Status
		err := m.peerJSON(attempt, mem, addr, http.MethodGet, peerPathStatus, nil, &st)
		cancel()
		if err == nil {
			if st.UUID != mem.UUID {
				lastErr = fmt.Errorf("%s answered as %s", addr, shortUUID(st.UUID))
				continue
			}
			return &st, addr, nil
		}
		lastErr = err
	}
	return nil, "", lastErr
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---- background loops ----

func (m *Manager) pollLoop() {
	ctx := m.context()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, id := range m.dir.Due() {
			mem, ok := m.store.Member(id)
			if !ok {
				m.dir.Forget(id)
				continue
			}
			if !m.dir.BeginPoll(id) {
				continue
			}
			go m.pollMember(ctx, mem)
		}
	}
}

func (m *Manager) pollMember(ctx context.Context, mem Member) {
	before, _ := m.dir.Get(mem.UUID)
	st, addr, err := m.fetchStatus(ctx, mem)
	if err != nil {
		m.dir.MarkFailure(mem.UUID, err)
		after, _ := m.dir.Get(mem.UUID)
		if before.Health != after.Health {
			m.logf("cluster: node %s (%s) is %s: %v", shortUUID(mem.UUID), mem.Name, after.Health, err)
		}
		return
	}
	m.dir.MarkSuccess(mem.UUID, addr, st)
	m.store.RecordAddress(mem.UUID, addr)
	if st.Name != "" && st.Name != mem.Name {
		_, _ = m.store.Upsert(Member{UUID: mem.UUID, Name: st.Name, CertFingerprint: mem.CertFingerprint}, false)
	}
	if before.Health != HealthHealthy {
		m.logf("cluster: node %s (%s) is online at %s", shortUUID(mem.UUID), st.Name, addr)
	}
}

// discoveredRefreshLoop re-reads every unpaired node it knows about. mDNS
// only reports a node once (and again when its record changes), so a node
// that restarts, joins another cluster or simply stays around would otherwise
// age out of the discovered list or show stale cluster membership. A direct
// public-status probe keeps the list current and works without multicast.
func (m *Manager) discoveredRefreshLoop() {
	ctx := m.context()
	ticker := time.NewTicker(discoveredRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var wg sync.WaitGroup
		for _, obs := range m.dir.DiscoveredAll() {
			wg.Add(1)
			go func(obs Observation) {
				defer wg.Done()
				attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				st, err := m.fetchStatusUnpinned(attempt, obs.Endpoint())
				if err != nil || st.UUID != obs.UUID {
					return
				}
				refreshed := obs
				refreshed.Seen = time.Now()
				refreshed.ClusterUUID = st.ClusterUUID
				refreshed.Name = st.Name
				refreshed.Version = st.Version
				refreshed.Licensed = st.Licensed
				refreshed.APIPort = st.APIPort
				m.onObservation(refreshed)
			}(obs)
		}
		wg.Wait()
		m.dir.ExpireDiscovered(discoveredMaxAge)
	}
}

const (
	discoveredRefreshInterval = 20 * time.Second
	// discoveredMaxAge is how long an unpaired node stays listed after its
	// last successful observation or probe.
	discoveredMaxAge = 3 * time.Minute
)

// gossipLoop exchanges member tables every 15 seconds, or sooner when woken
// by a membership change.
func (m *Manager) gossipLoop() {
	ctx := m.context()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.gossipWake:
		}
		if !m.store.InCluster() {
			continue
		}
		m.gossipOnce(ctx)
	}
}

func (m *Manager) wakeGossip() {
	select {
	case m.gossipWake <- struct{}{}:
	default:
	}
}

func (m *Manager) gossipOnce(ctx context.Context) {
	members := m.store.Members()
	var wg sync.WaitGroup
	for _, mem := range members {
		rt, _ := m.dir.Get(mem.UUID)
		if rt.Health == HealthDown && time.Since(rt.LastSeen) > 10*time.Minute && rt.Failures%4 != 0 {
			// Long-dead nodes are gossiped less often; polls keep probing.
			continue
		}
		wg.Add(1)
		go func(mem Member) {
			defer wg.Done()
			m.gossipWith(ctx, mem)
		}(mem)
	}
	wg.Wait()
	m.mu.Lock()
	m.lastGossip = time.Now()
	m.mu.Unlock()
}

func (m *Manager) gossipWith(ctx context.Context, mem Member) {
	addrs := m.dir.Candidates(mem.UUID)
	if len(addrs) == 0 {
		addrs = m.seedAddressesFor(mem)
	}
	if len(addrs) == 0 {
		return
	}
	c := m.store.Cluster()
	if c == nil {
		return
	}
	msg := gossipMessage{ClusterUUID: c.UUID, Sender: m.card(), Tombstones: m.store.Tombstones()}
	for _, other := range m.store.Members() {
		msg.Members = append(msg.Members, m.cardFromMember(other))
	}
	// One address per round is enough: the poll loop is what repairs a
	// stale address, gossip only needs to reach the member when it can.
	addr := addrs[0]
	msg.Observed = addr
	attempt, cancel := context.WithTimeout(ctx, 8*time.Second)
	var reply gossipMessage
	err := m.peerJSON(attempt, mem, addr, http.MethodPost, peerPathGossip, msg, &reply)
	cancel()
	if err != nil {
		var pe *peerError
		if errors.As(err, &pe) && pe.Status == http.StatusForbidden && pe.Body.Code == "not_a_member" {
			m.noteRejection(mem.UUID)
		}
		return
	}
	m.clearRejection(mem.UUID)
	m.mergeGossip(reply, addr)
}

// mergeGossip folds a peer's member table into ours.
// noteRejection records that a member does not recognise this node. A single
// rejection is normal right after pairing (the peer has not heard yet), so
// this node only concludes it was removed while offline when every member
// with a known address has rejected it for a sustained period.
func (m *Manager) noteRejection(nodeUUID string) {
	m.rejectMu.Lock()
	if m.rejectedBy == nil {
		m.rejectedBy = map[string]time.Time{}
	}
	if _, ok := m.rejectedBy[nodeUUID]; !ok {
		m.rejectedBy[nodeUUID] = time.Now()
	}
	first := time.Now()
	for _, at := range m.rejectedBy {
		if at.Before(first) {
			first = at
		}
	}
	rejected := len(m.rejectedBy)
	m.rejectMu.Unlock()

	members := m.store.Members()
	if rejected < len(members) || time.Since(first) < rejectionGracePeriod {
		return
	}
	for _, mem := range members {
		m.rejectMu.Lock()
		_, ok := m.rejectedBy[mem.UUID]
		m.rejectMu.Unlock()
		if !ok {
			return
		}
	}
	if c := m.store.Cluster(); c != nil {
		m.logf("cluster: every member has refused this node for %s; it was removed while offline, leaving cluster %s", rejectionGracePeriod, shortUUID(c.UUID))
	}
	_ = m.leaveLocally()
	m.rejectMu.Lock()
	m.rejectedBy = nil
	m.rejectMu.Unlock()
}

func (m *Manager) clearRejection(nodeUUID string) {
	m.rejectMu.Lock()
	delete(m.rejectedBy, nodeUUID)
	m.rejectMu.Unlock()
}

// rejectionGracePeriod is how long every member must keep refusing this node
// before it concludes it was removed.
const rejectionGracePeriod = 2 * time.Minute

func (m *Manager) mergeGossip(msg gossipMessage, from string) {
	c := m.store.Cluster()
	if c == nil || msg.ClusterUUID != c.UUID {
		return
	}
	if removed, err := m.store.ApplyTombstones(msg.Tombstones); err == nil {
		for _, id := range removed {
			m.dir.Forget(id)
			m.logf("cluster: node %s removed by a peer", shortUUID(id))
		}
	}
	now := time.Now()
	cards := append([]nodeCard{msg.Sender}, msg.Members...)
	for _, card := range cards {
		if card.UUID == "" || card.UUID == m.identity.UUID {
			continue
		}
		if m.store.IsTombstoned(card.UUID) {
			continue
		}
		mem, err := m.memberFromCard(card)
		if err != nil {
			continue
		}
		if card.UUID == msg.Sender.UUID && from != "" {
			mem.LastAddresses = append([]string{from}, mem.LastAddresses...)
		}
		added, err := m.store.Upsert(mem, false)
		if err != nil {
			m.logf("cluster: gossip: %v", err)
			continue
		}
		if added {
			m.dir.Track(card.UUID, m.seedAddressesFor(mem))
		}
		for i, addr := range mem.LastAddresses {
			m.dir.LearnAddress(card.UUID, addr, now.Add(-time.Duration(i)*time.Second))
		}
	}
	if msg.Observed != "" {
		m.noteObservedIP(endpointHost(msg.Observed))
	}
}

// ---- membership operations ----

// CreateCluster founds a cluster of one and returns the join token.
func (m *Manager) CreateCluster(name string) (ClusterInfo, string, error) {
	info, token, err := m.store.Create(name)
	if err != nil {
		return ClusterInfo{}, "", err
	}
	m.dir.Reset()
	m.reannounce()
	m.logf("cluster: created %q (%s)", info.Name, shortUUID(info.UUID))
	return info, token, nil
}

// JoinToken returns the current token (minted this process) or rotates one.
func (m *Manager) JoinToken(rotate bool) (string, error) {
	if !m.store.InCluster() {
		return "", ErrNotClustered
	}
	if rotate || m.store.JoinToken() == "" {
		token, err := m.store.RotateJoinToken()
		if err != nil {
			return "", err
		}
		m.wakeGossip()
		return token, nil
	}
	return m.store.JoinToken(), nil
}

// Join contacts a member of the cluster named by token and joins.
func (m *Manager) Join(ctx context.Context, token, address string) (ClusterInfo, error) {
	if m.store.InCluster() {
		return ClusterInfo{}, ErrAlreadyClustered
	}
	clusterUUID, secret, err := ParseJoinToken(token)
	if err != nil {
		return ClusterInfo{}, err
	}
	targets := m.joinTargets(clusterUUID, address)
	if len(targets) == 0 {
		return ClusterInfo{}, errors.New("no member of that cluster was found on the network; pass its address explicitly")
	}
	key := hashSecret(secret)
	var lastErr error
	for _, addr := range targets {
		info, err := m.joinVia(ctx, addr, clusterUUID, key)
		if err == nil {
			return info, nil
		}
		lastErr = err
		var pe *peerError
		if errors.As(err, &pe) && (pe.Status == http.StatusForbidden || pe.Status == http.StatusUnauthorized || pe.Status == http.StatusConflict) {
			// A definitive refusal; other members will say the same.
			return ClusterInfo{}, err
		}
	}
	return ClusterInfo{}, lastErr
}

func (m *Manager) joinTargets(clusterUUID, address string) []string {
	var targets []string
	if address = strings.TrimSpace(address); address != "" {
		if _, _, err := net.SplitHostPort(address); err != nil {
			address = net.JoinHostPort(address, strconv.Itoa(portOf(DefaultListenAddr)))
		}
		targets = append(targets, address)
	}
	discovered := m.dir.Discovered(discoveredMaxAge)
	for _, obs := range discovered {
		if obs.ClusterUUID == clusterUUID {
			targets = append(targets, obs.Endpoint())
		}
	}
	// A node whose membership we have not refreshed yet may still be a
	// member; the join handler refuses a wrong cluster, so trying costs little.
	for _, obs := range discovered {
		if obs.ClusterUUID != clusterUUID && !containsString(targets, obs.Endpoint()) {
			targets = append(targets, obs.Endpoint())
		}
	}
	for _, seed := range m.opts.Seeds {
		if _, _, err := net.SplitHostPort(seed); err != nil {
			seed = net.JoinHostPort(seed, strconv.Itoa(portOf(DefaultListenAddr)))
		}
		if !containsString(targets, seed) {
			targets = append(targets, seed)
		}
	}
	return targets
}

func (m *Manager) joinVia(ctx context.Context, addr, clusterUUID, key string) (ClusterInfo, error) {
	nonce, err := randomNonce()
	if err != nil {
		return ClusterInfo{}, err
	}
	now := time.Now().Unix()
	req := joinRequest{Node: m.card(), Nonce: nonce, TS: now}
	req.MAC = HandshakeMAC(key, m.identity.UUID, m.identity.Fingerprint(), nonce, now)
	client := peerClient(m.identity, "", "")
	defer client.CloseIdleConnections()

	raw, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+peerPathJoin+"?cluster="+clusterUUID, bytes.NewReader(raw))
	if err != nil {
		return ClusterInfo{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	if err != nil {
		return ClusterInfo{}, fmt.Errorf("contacting %s: %w", addr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		pe := &peerError{Status: resp.StatusCode}
		_ = json.Unmarshal(body, &pe.Body)
		if pe.Body.Error == "" {
			pe.Body.Error = strings.TrimSpace(string(body))
		}
		return ClusterInfo{}, pe
	}
	var reply joinResponse
	if err := json.Unmarshal(body, &reply); err != nil {
		return ClusterInfo{}, fmt.Errorf("parsing join response: %w", err)
	}
	if reply.Cluster.UUID != clusterUUID {
		return ClusterInfo{}, errors.New("peer answered for a different cluster")
	}
	// The TLS peer must be the responder it claims to be.
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return ClusterInfo{}, errors.New("peer presented no certificate")
	}
	if got := CertFingerprint(resp.TLS.PeerCertificates[0]); got != reply.Responder.Fingerprint || uuidFromCert(resp.TLS.PeerCertificates[0]) != reply.Responder.UUID {
		return ClusterInfo{}, errors.New("peer certificate does not match the responder's identity")
	}
	members := make([]Member, 0, len(reply.Members)+1)
	responder, err := m.memberFromCard(reply.Responder)
	if err != nil {
		return ClusterInfo{}, err
	}
	responder.LastAddresses = append([]string{addr}, responder.LastAddresses...)
	members = append(members, responder)
	for _, card := range reply.Members {
		if card.UUID == m.identity.UUID || card.UUID == responder.UUID {
			continue
		}
		mem, err := m.memberFromCard(card)
		if err != nil {
			m.logf("cluster: join: skipping member %s: %v", shortUUID(card.UUID), err)
			continue
		}
		members = append(members, mem)
	}
	if err := m.store.Adopt(reply.Cluster, reply.JoinTokenHash, members, m.identity.UUID); err != nil {
		return ClusterInfo{}, err
	}
	m.dir.Reset()
	for _, mem := range members {
		m.dir.Track(mem.UUID, m.seedAddressesFor(mem))
		m.dir.DropDiscovered(mem.UUID)
	}
	m.dir.LearnAddress(responder.UUID, addr, time.Now())
	m.reannounce()
	m.wakeGossip()
	m.logf("cluster: joined %q (%s) via %s", reply.Cluster.Name, shortUUID(reply.Cluster.UUID), addr)
	return reply.Cluster, nil
}

func (m *Manager) autoJoin(token string) {
	ctx := m.context()
	delay := 3 * time.Second
	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if m.store.InCluster() {
			return
		}
		joinCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, err := m.Join(joinCtx, token, "")
		cancel()
		if err == nil {
			return
		}
		var pe *peerError
		if errors.As(err, &pe) && pe.Status/100 == 4 {
			m.logf("cluster: automatic join refused: %v", err)
			return
		}
		if attempt == 1 || attempt%10 == 0 {
			m.logf("cluster: automatic join not yet possible (%v); retrying", err)
		}
		if delay < time.Minute {
			delay *= 2
		}
	}
}

// Invite brings an unpaired node seen on the LAN into this cluster using the
// admission code shown on that node.
func (m *Manager) Invite(ctx context.Context, nodeUUID, code, address string) (Member, error) {
	if !m.store.InCluster() {
		return Member{}, ErrNotClustered
	}
	if !m.withinNodeLimit(len(m.store.Members()) + 2) {
		return Member{}, &LimitError{Limit: m.NodeLimit(), Current: len(m.store.Members()) + 1}
	}
	var target string
	if address = strings.TrimSpace(address); address != "" {
		target = address
		if _, _, err := net.SplitHostPort(target); err != nil {
			target = net.JoinHostPort(target, strconv.Itoa(portOf(DefaultListenAddr)))
		}
	} else {
		for _, obs := range m.dir.Discovered(discoveredMaxAge) {
			if obs.UUID == nodeUUID {
				target = obs.Endpoint()
				break
			}
		}
	}
	if target == "" {
		return Member{}, errors.New("node not seen on the network; pass its address")
	}
	nonce, err := randomNonce()
	if err != nil {
		return Member{}, err
	}
	now := time.Now().Unix()
	req := inviteRequest{clusterView: m.view(), Inviter: m.card(), Nonce: nonce, TS: now}
	req.MAC = HandshakeMAC(strings.TrimSpace(code), m.identity.UUID, m.identity.Fingerprint(), nonce, now)
	client := peerClient(m.identity, "", "")
	defer client.CloseIdleConnections()
	raw, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+target+peerPathInvite, bytes.NewReader(raw))
	if err != nil {
		return Member{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	if err != nil {
		return Member{}, fmt.Errorf("contacting %s: %w", target, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		pe := &peerError{Status: resp.StatusCode}
		_ = json.Unmarshal(body, &pe.Body)
		if pe.Body.Error == "" {
			pe.Body.Error = strings.TrimSpace(string(body))
		}
		return Member{}, pe
	}
	var reply inviteResponse
	if err := json.Unmarshal(body, &reply); err != nil {
		return Member{}, err
	}
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 || CertFingerprint(resp.TLS.PeerCertificates[0]) != reply.Node.Fingerprint || uuidFromCert(resp.TLS.PeerCertificates[0]) != reply.Node.UUID {
		return Member{}, errors.New("invited node's certificate does not match its identity")
	}
	if nodeUUID != "" && reply.Node.UUID != nodeUUID {
		return Member{}, fmt.Errorf("address %s belongs to node %s, not %s", target, shortUUID(reply.Node.UUID), shortUUID(nodeUUID))
	}
	mem, err := m.memberFromCard(reply.Node)
	if err != nil {
		return Member{}, err
	}
	mem.LastAddresses = append([]string{target}, mem.LastAddresses...)
	if _, err := m.store.Upsert(mem, true); err != nil {
		return Member{}, err
	}
	m.dir.Track(mem.UUID, m.seedAddressesFor(mem))
	m.dir.LearnAddress(mem.UUID, target, time.Now())
	m.dir.DropDiscovered(mem.UUID)
	m.wakeGossip()
	m.logf("cluster: invited node %s (%s) at %s", shortUUID(mem.UUID), mem.Name, target)
	return mem, nil
}

// LimitError reports a refused pairing because of the node cap.
type LimitError struct {
	Limit   int
	Current int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("cluster is limited to %d nodes by the license (currently %d)", e.Limit, e.Current)
}

// Leave announces departure to every member and forgets the cluster.
func (m *Manager) Leave(ctx context.Context) error {
	if !m.store.InCluster() {
		return ErrNotClustered
	}
	msg := leaveMessage{UUID: m.identity.UUID}
	var wg sync.WaitGroup
	for _, mem := range m.store.Members() {
		addrs := m.dir.Candidates(mem.UUID)
		if len(addrs) == 0 {
			continue
		}
		wg.Add(1)
		go func(mem Member, addr string) {
			defer wg.Done()
			attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = m.peerJSON(attempt, mem, addr, http.MethodPost, peerPathLeave, msg, nil)
		}(mem, addrs[0])
	}
	wg.Wait()
	return m.leaveLocally()
}

func (m *Manager) leaveLocally() error {
	if err := m.store.Leave(); err != nil && !errors.Is(err, ErrNotClustered) {
		return err
	}
	m.dir.Reset()
	m.reannounce()
	m.logf("cluster: left the cluster")
	return nil
}

// RemoveMember drops a member, tells it, and lets gossip carry the tombstone.
func (m *Manager) RemoveMember(ctx context.Context, nodeUUID string) error {
	mem, ok := m.store.Member(nodeUUID)
	if !ok {
		return fmt.Errorf("node %s is not a member", shortUUID(nodeUUID))
	}
	addrs := m.dir.Candidates(nodeUUID)
	if _, err := m.store.Remove(nodeUUID); err != nil {
		return err
	}
	m.dir.Forget(nodeUUID)
	if len(addrs) > 0 {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = m.peerJSON(attempt, mem, addrs[0], http.MethodPost, peerPathLeave, leaveMessage{UUID: nodeUUID}, nil)
	}
	m.wakeGossip()
	m.logf("cluster: removed node %s (%s)", shortUUID(nodeUUID), mem.Name)
	return nil
}

// Rename changes this node's display name.
func (m *Manager) Rename(name string) error {
	if err := m.identity.SaveName(name); err != nil {
		return err
	}
	m.reannounce()
	m.wakeGossip()
	return nil
}

// UpdateSettings changes operator settings and re-tracks static addresses.
func (m *Manager) UpdateSettings(fn func(*Settings)) (Settings, error) {
	s, err := m.store.UpdateSettings(fn)
	if err != nil {
		return Settings{}, err
	}
	for _, mem := range m.store.Members() {
		m.dir.Track(mem.UUID, m.seedAddressesFor(mem))
	}
	return s, nil
}

// PullOnNode asks a member to download a model from its own model source.
func (m *Manager) PullOnNode(ctx context.Context, nodeUUID string, body json.RawMessage) (json.RawMessage, error) {
	mem, ok := m.store.Member(nodeUUID)
	if !ok {
		return nil, fmt.Errorf("node %s is not a member", shortUUID(nodeUUID))
	}
	addrs := m.dir.Candidates(nodeUUID)
	if len(addrs) == 0 {
		return nil, fmt.Errorf("node %s has no known address", shortUUID(nodeUUID))
	}
	var out json.RawMessage
	if err := m.peerJSON(ctx, mem, addrs[0], http.MethodPost, peerPathPull, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func randomNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// ---- perf store (entry-side EMA of per node/model throughput) ----

type perfStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]*ModelPerf
	dirty   bool
	lastFl  time.Time
}

func newPerfStore(path string) *perfStore {
	ps := &perfStore{path: path, entries: map[string]*ModelPerf{}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &ps.entries)
	}
	return ps
}

func perfKey(nodeUUID, model string) string { return nodeUUID + "\x00" + model }

func (ps *perfStore) get(nodeUUID, model string) *ModelPerf {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p, ok := ps.entries[perfKey(nodeUUID, model)]
	if !ok {
		return nil
	}
	c := *p
	return &c
}

// observe folds one measurement into the EMA (alpha 0.3).
func (ps *perfStore) observe(nodeUUID, model string, decodeTPS, promptTPS, loadSeconds float64) {
	if nodeUUID == "" || model == "" {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	key := perfKey(nodeUUID, model)
	p, ok := ps.entries[key]
	if !ok {
		p = &ModelPerf{}
		ps.entries[key] = p
	}
	const alpha = 0.3
	ema := func(cur, sample float64) float64 {
		if sample <= 0 {
			return cur
		}
		if cur <= 0 {
			return sample
		}
		return cur*(1-alpha) + sample*alpha
	}
	p.DecodeTPS = ema(p.DecodeTPS, decodeTPS)
	p.PromptTPS = ema(p.PromptTPS, promptTPS)
	p.LoadSeconds = ema(p.LoadSeconds, loadSeconds)
	p.Samples++
	ps.dirty = true
	if time.Since(ps.lastFl) > 30*time.Second {
		ps.flushLocked()
	}
}

func (ps *perfStore) flush() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.flushLocked()
}

func (ps *perfStore) flushLocked() {
	if !ps.dirty || ps.path == "" {
		return
	}
	raw, err := json.MarshalIndent(ps.entries, "", "  ")
	if err != nil {
		return
	}
	if err := writeFileAtomic(ps.path, raw, 0o600); err == nil {
		ps.dirty = false
		ps.lastFl = time.Now()
	}
}
