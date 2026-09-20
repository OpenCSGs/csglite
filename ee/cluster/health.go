// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"sync"
	"time"
)

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
				m.forgetNode(id)
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
