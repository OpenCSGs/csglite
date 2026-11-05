// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
)

// LocalStatus is this node's full status as peers see it.
func (m *Manager) LocalStatus(ctx context.Context) Status {
	st := m.opts.Host.LocalStatus(ctx)
	settings := m.store.Settings()
	st.UUID = m.identity.UUID
	st.Name = m.identity.DisplayName()
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
	st.SpanWorker = m.worker != nil && m.worker.inUse()
	if spans := m.spans.list(); len(spans) > 0 {
		st.Spans = spans
	}
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

func (m *Manager) card() nodeCard {
	return nodeCard{
		UUID:        m.identity.UUID,
		Name:        m.identity.DisplayName(),
		CertPEM:     m.identity.CertPEM(),
		Fingerprint: m.identity.Fingerprint(),
		APIPort:     m.opts.Host.APIPort(),
		ClusterPort: m.ListenPort(),
		Addresses:   m.advertisedEndpoints(),
		Version:     m.opts.Host.Version(),
		Protocol:    ProtocolVersion,
	}
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

// cachedLocalStatus reuses the local status for two seconds: building it
// shells out to nvidia-smi and the like, which must not run per request.
func (m *Manager) cachedLocalStatus(ctx context.Context) *Status {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.statusCache != nil && time.Since(m.statusAt) < 2*time.Second {
		return m.statusCache
	}
	st := m.LocalStatus(ctx)
	m.statusCache = &st
	m.statusAt = time.Now()
	return m.statusCache
}

func (m *Manager) overlayPerf(nodeUUID string, st *Status) *Status {
	changed := false
	models := make([]ModelStatus, len(st.Models))
	copy(models, st.Models)
	for i := range models {
		if p := m.perf.get(nodeUUID, models[i].ID); p != nil && p.Samples > 0 {
			merged := *p
			if models[i].Perf != nil && models[i].Perf.LoadSeconds > 0 && merged.LoadSeconds == 0 {
				merged.LoadSeconds = models[i].Perf.LoadSeconds
			}
			models[i].Perf = &merged
			changed = true
		}
	}
	if !changed {
		return st
	}
	c := *st
	c.Models = models
	return &c
}

func (m *Manager) candidateStatus(nodeUUID string) *Status {
	if nodeUUID == m.identity.UUID {
		return m.cachedLocalStatus(context.Background())
	}
	rt, ok := m.dir.Get(nodeUUID)
	if !ok {
		return nil
	}
	return rt.Status
}

// candidates builds the scheduler input from the directory and local status.
func (m *Manager) candidates(ctx context.Context, model string) []Candidate {
	var out []Candidate
	local := m.cachedLocalStatus(ctx)
	// Reserved is how many requests are in flight to a node right now, not how
	// many it has ever been sent: a running total never comes down, so it
	// would stop telling the scheduler anything the moment the cluster had
	// been busy for a while, and tells it nothing at all on a cluster that has
	// just started, where every node reads zero and a burst of identical
	// requests lands on whichever node the tie-break happens to name.
	out = append(out, Candidate{UUID: m.identity.UUID, Name: m.identity.DisplayName(), Local: true, Status: local, Health: HealthHealthy, Reserved: m.dir.ReservedCount(m.identity.UUID), Breaker: m.dir.ModelBroken(m.identity.UUID, model)})
	for _, rt := range m.dir.Snapshot() {
		if _, ok := m.store.Member(rt.UUID); !ok {
			continue
		}
		name := rt.UUID
		if rt.Status != nil && rt.Status.Name != "" {
			name = rt.Status.Name
		} else if mem, ok := m.store.Member(rt.UUID); ok && mem.Name != "" {
			name = mem.Name
		}
		st := rt.Status
		if st != nil {
			// Entry-side perf samples override what the node reports when
			// we have measured this node ourselves.
			st = m.overlayPerf(rt.UUID, st)
		}
		out = append(out, Candidate{UUID: rt.UUID, Name: name, Status: st, Health: rt.Health, Reserved: m.dir.ReservedCount(rt.UUID), Breaker: m.dir.ModelBroken(rt.UUID, model), Cooling: m.dir.Cooling(rt.UUID)})
	}
	return out
}

func (m *Manager) invalidateLocalStatus() {
	m.statusMu.Lock()
	m.statusCache = nil
	m.statusMu.Unlock()
}
