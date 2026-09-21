// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Spanning a model means running one copy of it across several machines,
// because it does not fit on any of them alone. It is the opposite trade to
// the rest of this package: routing a request to whichever member can finish
// it soonest buys throughput and keeps every copy independent, while spanning
// buys capacity and gives up both speed and redundancy.
//
// Measured on three machines over a wireless link, splitting a model that does
// already fit costs about half the generation speed on two nodes and about
// two thirds on three. So a span is never the faster choice: it is the only
// choice, for a model that would otherwise page from disk at seconds per token.

// ErrSpanNotSupported is returned when the worker binary is missing, which is
// the usual reason a span cannot start.
var ErrSpanNotSupported = errors.New("cluster: this build cannot split a model across machines")

// SpanMember is one machine taking part, and what it contributes.
type SpanMember struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	Local    bool   `json:"local"`
	VRAMFree uint64 `json:"vram_free"`
	// Endpoint is the loopback address llama-server was given for this member.
	// For the local machine it is empty: its own devices are used directly.
	Endpoint string `json:"-"`
}

// Span describes one model running across several machines.
type Span struct {
	Model   string       `json:"model"`
	Members []SpanMember `json:"members"`
	Started time.Time    `json:"started_at"`
	// HostDevices says whether the node serving requests holds a share of the
	// weights as well as driving the others.
	HostDevices bool `json:"host_devices"`

	tunnels []*rpcTunnel
	release []func()
}

// SpanView is what the API reports.
type SpanView struct {
	Model   string       `json:"model"`
	Members []SpanMember `json:"members"`
	Started time.Time    `json:"started_at"`
	// HostDevices says whether the node serving requests holds a share of the
	// weights too, or only drives the machines that do.
	HostDevices bool `json:"host_devices"`
	// Redundant is always false and is reported so a client cannot forget:
	// the weights exist once, spread across these machines, so losing any one
	// of them loses the model until it is loaded again.
	Redundant bool `json:"redundant"`
}

func (s *Span) view() SpanView {
	return SpanView{Model: s.Model, Members: append([]SpanMember(nil), s.Members...), Started: s.Started, HostDevices: s.HostDevices}
}

// spanState holds the spans this node is serving, and the ones being built.
type spanState struct {
	mu    sync.Mutex
	spans map[string]*Span
	// starting are the models a split is being built for right now. Building
	// one takes minutes, which is long enough for a second request to arrive
	// for the same model and start a second set of workers on the same
	// machines.
	starting map[string]bool
}

func newSpanState() *spanState {
	return &spanState{spans: map[string]*Span{}, starting: map[string]bool{}}
}

// begin claims a model for one caller. It reports false when the model is
// already split or another caller is splitting it.
func (s *spanState) begin(model string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spans[model] != nil || s.starting[model] {
		return false
	}
	s.starting[model] = true
	return true
}

// done releases the claim begin took, whether the split was built or not.
func (s *spanState) done(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.starting, model)
}

func (s *spanState) get(model string) (*Span, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spans[model]
	return sp, ok
}

func (s *spanState) list() []SpanView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SpanView, 0, len(s.spans))
	for _, sp := range s.spans {
		out = append(out, sp.view())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

func (s *spanState) put(sp *Span) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans[sp.Model] = sp
}

func (s *spanState) take(model string) (*Span, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spans[model]
	if ok {
		delete(s.spans, model)
	}
	return sp, ok
}

// Spans lists the models this node is running across several machines.
func (m *Manager) Spans() []SpanView { return m.spans.list() }

// SpanModel loads modelID across this node and the named members. Passing no
// members picks every healthy member that has memory to spare.
//
// The model file is read by this node only: the RPC backend sends tensors to
// the workers rather than expecting them to hold a copy, which is why a span
// does not require the model to be synchronised first.
func (m *Manager) SpanModel(ctx context.Context, modelID string, nodeUUIDs []string, hostDevices bool, numCtx int) (SpanView, error) {
	if !m.store.InCluster() {
		return SpanView{}, ErrNotClustered
	}
	if !m.spans.begin(modelID) {
		return SpanView{}, fmt.Errorf("%s is already split across machines, or is being split right now; tear it down first", modelID)
	}
	defer m.spans.done(modelID)

	participants, err := m.spanParticipants(ctx, nodeUUIDs)
	if err != nil {
		return SpanView{}, err
	}
	if len(participants) == 0 {
		return SpanView{}, errors.New("no other member has memory to spare, so there is nothing to split onto")
	}

	span := &Span{Model: modelID, Started: time.Now(), HostDevices: hostDevices}
	// This machine always reads the weights and drives the others. Whether it
	// also holds a share of them is a separate question: a model split because
	// it does not fit here must not be given one, since llama.cpp would hand
	// this machine a share by free memory and then put the whole KV cache on
	// top of it. A 27B loaded that way reported success and then failed every
	// request with a compute error.
	if hostDevices {
		span.Members = append(span.Members, m.localSpanMember(ctx))
	}

	cleanup := func() {
		for _, t := range span.tunnels {
			t.Close()
		}
		for _, r := range span.release {
			r()
		}
	}

	var endpoints []string
	for _, p := range participants {
		mem, ok := m.store.Member(p.UUID)
		if !ok {
			continue
		}
		addr, err := m.startRemoteWorker(ctx, mem)
		if err != nil {
			cleanup()
			return SpanView{}, fmt.Errorf("%s could not start its RPC worker: %w", mem.Name, err)
		}
		// Record the release before anything else can fail, so a span that
		// breaks half way through still hands back the workers it started.
		span.release = append(span.release, m.releaseRemoteWorkerFunc(mem, addr))
		tunnel, err := m.openRPCTunnel(mem, addr)
		if err != nil {
			cleanup()
			return SpanView{}, err
		}
		span.tunnels = append(span.tunnels, tunnel)
		// Prove the path end to end before llama-server depends on it. A
		// tunnel that cannot reach the worker would otherwise surface as a
		// line in a log and a model quietly loaded on one machine.
		if err := probeTunnel(tunnel.Endpoint()); err != nil {
			cleanup()
			return SpanView{}, fmt.Errorf("the connection to %s does not reach its RPC worker: %w", mem.Name, err)
		}
		p.Endpoint = tunnel.Endpoint()
		endpoints = append(endpoints, p.Endpoint)
		span.Members = append(span.Members, p)
	}

	if err := m.opts.Host.SpanModel(ctx, modelID, endpoints, hostDevices, numCtx); err != nil {
		cleanup()
		return SpanView{}, fmt.Errorf("loading %s across %d machines: %w", modelID, len(span.Members), err)
	}
	m.spans.put(span)
	m.invalidateLocalStatus()
	m.logf("cluster: %s is now split across %d machines (%s); it has no redundancy, losing any one of them unloads it",
		modelID, len(span.Members), spanNames(span.Members))
	return span.view(), nil
}

// UnspanModel tears a split model down and releases the workers.
func (m *Manager) UnspanModel(ctx context.Context, modelID string) error {
	span, ok := m.spans.take(modelID)
	if !ok {
		return fmt.Errorf("%s is not split across machines", modelID)
	}
	if err := m.opts.Host.SpanModel(ctx, modelID, nil, false, 0); err != nil {
		m.logf("cluster: unloading the split copy of %s: %v", modelID, err)
	}
	for _, t := range span.tunnels {
		t.Close()
	}
	for _, r := range span.release {
		r()
	}
	m.invalidateLocalStatus()
	m.logf("cluster: %s is no longer split across machines", modelID)
	return nil
}

func spanNames(members []SpanMember) string {
	names := make([]string, 0, len(members))
	for _, mem := range members {
		names = append(names, mem.Name)
	}
	return joinComma(names)
}

func joinComma(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func (m *Manager) localSpanMember(ctx context.Context) SpanMember {
	st := m.cachedLocalStatus(ctx)
	var free uint64
	if st != nil {
		for _, g := range st.GPUs {
			free += g.VRAMTotal - g.VRAMUsed
		}
	}
	return SpanMember{UUID: m.identity.UUID, Name: m.identity.DisplayName(), Local: true, VRAMFree: free}
}

// spanParticipants picks the members to split onto: the named ones, or every
// healthy member when none are named, ordered by free memory so the largest
// share goes where there is most room.
func (m *Manager) spanParticipants(ctx context.Context, nodeUUIDs []string) ([]SpanMember, error) {
	wanted := map[string]bool{}
	for _, u := range nodeUUIDs {
		wanted[u] = true
	}
	var out []SpanMember
	for _, rt := range m.dir.Snapshot() {
		if rt.UUID == m.identity.UUID {
			continue
		}
		if len(wanted) > 0 && !wanted[rt.UUID] {
			continue
		}
		mem, ok := m.store.Member(rt.UUID)
		if !ok {
			continue
		}
		if !rt.Online() || rt.Status == nil {
			if len(wanted) > 0 {
				return nil, fmt.Errorf("%s is not reachable", mem.Name)
			}
			continue
		}
		st := rt.Status
		if !st.AcceptWork || st.State != NodeStateActive {
			if len(wanted) > 0 {
				return nil, fmt.Errorf("%s is %s and is not taking work", mem.Name, st.State)
			}
			continue
		}
		var free uint64
		for _, g := range st.GPUs {
			free += g.VRAMTotal - g.VRAMUsed
		}
		out = append(out, SpanMember{UUID: rt.UUID, Name: mem.Name, VRAMFree: free})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VRAMFree > out[j].VRAMFree })
	return out, nil
}

// rpcWorkerDialBudget bounds one attempt at one address, so a stale address
// costs seconds rather than a minute before the next is tried.
// The worker initialises its GPU backend before it listens, which on a busy
// machine takes several seconds, so the budget is generous. A member that is
// reachable answers on the first address tried, and the dead addresses a
// moved node leaves behind are only reached when that one has already failed.
const rpcWorkerDialBudget = 45 * time.Second

// rpcWorkerReply is what a member answers when asked to bring its worker up.
type rpcWorkerReply struct {
	Port int `json:"port"`
	// Build is the member's llama.cpp build. It must equal this node's, or the
	// two cannot exchange tensors at all.
	Build string `json:"build"`
}

// startRemoteWorker asks a member to bring its RPC worker up and returns the
// cluster address to tunnel to. The worker itself stays on that machine's
// loopback; only this address, already protected by mutual TLS, is used.
//
// The build is checked here rather than left to fail later, because
// llama-server does not fail on a worker it cannot talk to: it logs a line and
// loads the whole model locally instead. For a model that does not fit, that
// is the difference between a clear refusal and a machine thrashing on disk
// while the API reports success.
func (m *Manager) startRemoteWorker(ctx context.Context, mem Member) (string, error) {
	addrs := m.dir.Candidates(mem.UUID)
	if len(addrs) == 0 {
		return "", errors.New("no known address")
	}
	mine := m.opts.Host.LlamaBuildID()
	// Every address is reported, not only the last one tried. A member that
	// has changed network keeps its old addresses, so the final error is
	// usually a timeout on a dead one while the real reason, which the member
	// itself answered with, came from an address that did work.
	var problems []string
	// Every known address is tried, not the first one or two: a member keeps
	// the addresses it has ever been reached on, and after it moves networks
	// the stale ones are still in the list. Polling copes by trying them all,
	// and so must this, or a node that has changed address since it joined
	// cannot take part in a split.
	for _, addr := range addrs {
		var reply rpcWorkerReply
		attempt, cancel := context.WithTimeout(ctx, rpcWorkerDialBudget)
		err := m.peerJSON(attempt, mem, addr, "POST", peerPathRPCWorker, struct{}{}, &reply)
		cancel()
		if err != nil {
			// An answer, as opposed to silence, means this address reached the
			// member and the member said no. Trying its other addresses would
			// only ask the same machine the same question and bury the reason
			// it gave under a list of repetitions.
			var pe *peerError
			if errors.As(err, &pe) && pe.Status == http.StatusConflict {
				return "", fmt.Errorf("%s", pe.Body.Error)
			}
			problems = append(problems, addr+": "+err.Error())
			continue
		}
		if reply.Port <= 0 {
			problems = append(problems, addr+": the member reported no worker port")
			continue
		}
		if mine != "" && reply.Build != "" && mine != reply.Build {
			// The member started its worker before it answered, and this span
			// will not use it; hand it back rather than leave it held until
			// the grace period expires.
			m.releaseRemoteWorkerFunc(mem, addr)()
			return "", fmt.Errorf("%s runs llama.cpp build %s and this node runs %s; a model can only be split between machines on the same build",
				mem.Name, reply.Build, mine)
		}
		return addr, nil
	}
	if len(problems) == 0 {
		return "", errors.New("no known address")
	}
	return "", errors.New(joinComma(problems))
}

// rpcWorkerPort reports the local worker's port, starting nothing.
func (m *Manager) rpcWorkerPort() int {
	if m.worker == nil {
		return 0
	}
	return m.worker.running()
}

// startLocalRPCWorker brings this node's worker up for a member that is
// splitting a model onto it.
func (m *Manager) startLocalRPCWorker(ctx context.Context, holder string) (int, error) {
	if m.worker == nil {
		return 0, ErrSpanNotSupported
	}
	binary, err := m.opts.Host.RPCWorkerPath()
	if err != nil {
		return 0, err
	}
	return m.worker.start(m.context(), binary, holder)
}

// releaseRemoteWorkerFunc tells a member its worker is no longer needed. It is
// best effort: a member that has gone away has already stopped its worker with
// itself, and one that is merely slow to answer will time its worker out.
func (m *Manager) releaseRemoteWorkerFunc(mem Member, addr string) func() {
	return func() {
		ctx, cancel := context.WithTimeout(m.context(), 10*time.Second)
		defer cancel()
		if err := m.peerJSON(ctx, mem, addr, "POST", peerPathRPCWorkerRelease, struct{}{}, nil); err != nil {
			m.logf("cluster: telling %s its RPC worker is free: %v", mem.Name, err)
		}
	}
}

// rpcWorkerHeldByOther reports whether another member already has a model
// split onto this node.
func (m *Manager) rpcWorkerHeldByOther(holder string) (string, bool) {
	if m.worker == nil {
		return "", false
	}
	return m.worker.heldByOther(holder)
}

// releaseLocalRPCWorker marks one of a member's spans as finished with this
// node's worker.
func (m *Manager) releaseLocalRPCWorker(holder string) {
	if m.worker != nil {
		m.worker.release(holder)
	}
}

// spanWatchInterval is how often spans and idle workers are looked at. A span
// has no redundancy, so noticing a lost member in under a minute is the
// difference between one clear log line and a queue of requests timing out.
const spanWatchInterval = 20 * time.Second

// spanWatchLoop does two jobs that both belong to spanning and both only
// matter between requests: it stops a worker nobody is using any more, and it
// tears down a span whose machine has gone.
//
// A span cannot survive losing a member: the weights exist once, spread across
// the machines, so a member that disappears takes its layers with it and every
// request would block on tensors that will never arrive. Unloading is the
// honest answer, and it frees this node to load whatever it can hold alone.
func (m *Manager) spanWatchLoop() {
	ctx := m.context()
	ticker := time.NewTicker(spanWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if m.worker != nil {
			for _, holder := range m.worker.releaseGone(m.memberStillPaired) {
				m.logf("cluster: %s was using this node's RPC worker and has left the cluster; the memory it held is free again", m.memberName(holder))
			}
			for _, holder := range m.worker.releaseStale(time.Now()) {
				m.logf("cluster: nothing has been served for %s on behalf of %s, so this node is no longer holding memory for it",
					rpcWorkerHoldGrace, m.memberName(holder))
			}
		}
		for _, lost := range m.lostSpans() {
			m.logf("cluster: %s was split across machines and %s %s; unloading it, as a split model has no redundancy",
				lost.model, lost.member, lost.why)
			if err := m.UnspanModel(ctx, lost.model); err != nil {
				m.logf("cluster: unloading the split copy of %s: %v", lost.model, err)
			}
		}
	}
}

// lostSpan names a span, the member whose loss ended it, and what happened.
type lostSpan struct {
	model  string
	member string
	why    string
}

// lostSpans lists the spans that have lost a machine, and why.
//
// Two things end a span. The machine can go: it took its share of the weights
// with it and no request can be finished again. Or the machine can stay while
// the worker inside it goes, which llama.cpp's worker does by aborting when a
// load asks for more memory than the machine has. The second is reported by
// the member itself, and is only believed once the member has said something
// newer than the span is old, since a status from before the split naturally
// says no model is split onto it.
func (m *Manager) lostSpans() []lostSpan {
	var out []lostSpan
	for _, sp := range m.spans.list() {
		for _, mem := range sp.Members {
			if mem.Local {
				continue
			}
			rt, ok := m.dir.Get(mem.UUID)
			if !ok || !rt.Online() {
				out = append(out, lostSpan{model: sp.Model, member: mem.Name, why: "is no longer reachable"})
				break
			}
			if st := rt.Status; st != nil && !st.SpanWorker && rt.LastSeen.After(sp.Started) {
				out = append(out, lostSpan{model: sp.Model, member: mem.Name, why: "is no longer holding its share of the weights"})
				break
			}
		}
	}
	return out
}

// memberStillPaired says whether a member is still in this node's member
// table, which is what decides whether its hold on the RPC worker stands.
//
// Health deliberately plays no part. A member is not online to this node until
// it has been polled, so a node that has just restarted sees every peer as not
// yet reachable for a few seconds, and holding memory for a machine is not
// something to give up on a signal that says "not asked yet". A member that is
// really gone is caught either here, when it leaves or is removed, or by the
// traffic backstop in releaseStale, which is this node's own evidence rather
// than an opinion about someone else's health.
func (m *Manager) memberStillPaired(nodeUUID string) bool {
	_, ok := m.store.Member(nodeUUID)
	return ok
}

// memberName is a member's display name, falling back to its UUID.
func (m *Manager) memberName(nodeUUID string) string {
	if mem, ok := m.store.Member(nodeUUID); ok && mem.Name != "" {
		return mem.Name
	}
	return nodeUUID
}

// trackWorkerConn and untrackWorkerConn bracket a tunnel connection into the
// local worker, which is how this node knows a split model is still live.
func (m *Manager) trackWorkerConn() {
	if m.worker != nil {
		m.worker.connOpened()
	}
}

func (m *Manager) untrackWorkerConn() {
	if m.worker != nil {
		m.worker.connClosed()
	}
}

// releaseAllSpans tears down every span this node is serving. It runs at
// shutdown: the members lending their memory have no other way to learn that
// the model they were holding is gone, short of the silence eventually timing
// the hold out.
func (m *Manager) releaseAllSpans() {
	for _, view := range m.spans.list() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := m.UnspanModel(ctx, view.Model); err != nil {
			m.logf("cluster: releasing the split copy of %s at shutdown: %v", view.Model, err)
		}
		cancel()
	}
}
