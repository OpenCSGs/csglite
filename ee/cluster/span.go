// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"errors"
	"fmt"
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

	tunnels []*rpcTunnel
	release []func()
}

// SpanView is what the API reports.
type SpanView struct {
	Model   string       `json:"model"`
	Members []SpanMember `json:"members"`
	Started time.Time    `json:"started_at"`
	// Redundant is always false and is reported so a client cannot forget:
	// the weights exist once, spread across these machines, so losing any one
	// of them loses the model until it is loaded again.
	Redundant bool `json:"redundant"`
}

func (s *Span) view() SpanView {
	return SpanView{Model: s.Model, Members: append([]SpanMember(nil), s.Members...), Started: s.Started}
}

// spanState holds the spans this node is serving.
type spanState struct {
	mu    sync.Mutex
	spans map[string]*Span
}

func newSpanState() *spanState { return &spanState{spans: map[string]*Span{}} }

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
func (m *Manager) SpanModel(ctx context.Context, modelID string, nodeUUIDs []string) (SpanView, error) {
	if !m.store.InCluster() {
		return SpanView{}, ErrNotClustered
	}
	if _, running := m.spans.get(modelID); running {
		return SpanView{}, fmt.Errorf("%s is already split across machines; tear it down first", modelID)
	}

	participants, err := m.spanParticipants(ctx, nodeUUIDs)
	if err != nil {
		return SpanView{}, err
	}
	if len(participants) == 0 {
		return SpanView{}, errors.New("no other member has memory to spare, so there is nothing to split onto")
	}

	span := &Span{Model: modelID, Started: time.Now()}
	// The local machine always takes part: it reads the weights and drives the
	// others, so it holds a share as well.
	local := m.localSpanMember(ctx)
	span.Members = append(span.Members, local)

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
		tunnel, err := m.openRPCTunnel(mem, addr)
		if err != nil {
			cleanup()
			return SpanView{}, err
		}
		span.tunnels = append(span.tunnels, tunnel)
		p.Endpoint = tunnel.Endpoint()
		endpoints = append(endpoints, p.Endpoint)
		span.Members = append(span.Members, p)
	}

	if err := m.opts.Host.SpanModel(ctx, modelID, endpoints); err != nil {
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
	if err := m.opts.Host.SpanModel(ctx, modelID, nil); err != nil {
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

// startRemoteWorker asks a member to bring its RPC worker up and returns the
// cluster address to tunnel to. The worker itself stays on that machine's
// loopback; only this address, already protected by mutual TLS, is used.
func (m *Manager) startRemoteWorker(ctx context.Context, mem Member) (string, error) {
	addrs := m.dir.Candidates(mem.UUID)
	if len(addrs) == 0 {
		return "", errors.New("no known address")
	}
	var lastErr error
	for _, addr := range addrs[:min(2, len(addrs))] {
		var reply struct {
			Port int `json:"port"`
		}
		attempt, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := m.peerJSON(attempt, mem, addr, "POST", peerPathRPCWorker, struct{}{}, &reply)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		if reply.Port <= 0 {
			lastErr = errors.New("the member reported no worker port")
			continue
		}
		return addr, nil
	}
	return "", lastErr
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
func (m *Manager) startLocalRPCWorker(ctx context.Context) (int, error) {
	if m.worker == nil {
		return 0, ErrSpanNotSupported
	}
	binary, err := m.opts.Host.RPCWorkerPath()
	if err != nil {
		return 0, err
	}
	return m.worker.start(m.context(), binary)
}
