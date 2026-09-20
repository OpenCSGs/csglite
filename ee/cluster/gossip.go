// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
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

func (m *Manager) mergeGossip(msg gossipMessage, from string) {
	c := m.store.Cluster()
	if c == nil || msg.ClusterUUID != c.UUID {
		return
	}
	if removed, err := m.store.ApplyTombstones(msg.Tombstones); err == nil {
		for _, id := range removed {
			m.forgetNode(id)
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
		// Joining is capped by the licensed node count, and gossip must not
		// be a way around it. A member that is compromised, or simply out of
		// step, can otherwise name any number of nodes: the count then
		// exceeds the cap, every node reports itself unlicensed, and the
		// scheduler excludes all of them, which takes the cluster out of
		// service rather than granting extra capacity.
		if _, known := m.store.Member(card.UUID); !known && !m.withinNodeLimit(len(m.store.Members())+2) {
			m.logf("cluster: ignoring node %s from gossip: the cluster is at its licensed node limit of %d", shortUUID(card.UUID), m.NodeLimit())
			continue
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

// rejectionGracePeriod is how long every member must keep refusing this node
// before it concludes it was removed.
const rejectionGracePeriod = 2 * time.Minute
