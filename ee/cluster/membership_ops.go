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
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// CreateCluster founds a cluster of one and returns the join token.
func (m *Manager) CreateCluster(name string) (ClusterInfo, string, error) {
	if err := m.Activate(); err != nil {
		return ClusterInfo{}, "", err
	}
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
	if err := m.Activate(); err != nil {
		return ClusterInfo{}, err
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
		if errors.As(err, &pe) && pe.Body.Code != "wrong_cluster" && pe.Body.Code != "not_clustered" &&
			(pe.Status == http.StatusForbidden || pe.Status == http.StatusUnauthorized || pe.Status == http.StatusConflict) {
			// A definitive refusal (bad token, node cap); other members will
			// say the same. A node that is simply not in that cluster is
			// skipped and the next target tried.
			return ClusterInfo{}, err
		}
	}
	return ClusterInfo{}, lastErr
}

func (m *Manager) joinTargets(clusterUUID, address string) []string {
	var targets []string
	if address = withDefaultPort(address); address != "" {
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
		if obs.ClusterUUID != clusterUUID && !slices.Contains(targets, obs.Endpoint()) {
			targets = append(targets, obs.Endpoint())
		}
	}
	for _, seed := range m.opts.Seeds {
		seed = withDefaultPort(seed)
		if !slices.Contains(targets, seed) {
			targets = append(targets, seed)
		}
	}
	return targets
}

// joinHandshake performs the authenticated join exchange with a member at
// addr and returns its reply and verified card. It does not change state;
// joinVia adopts the reply and mergeVia unions it.
func (m *Manager) joinHandshake(ctx context.Context, addr, clusterUUID, key string) (joinResponse, Member, error) {
	nonce, err := randomNonce()
	if err != nil {
		return joinResponse{}, Member{}, err
	}
	now := time.Now().Unix()
	req := joinRequest{Node: m.card(), Nonce: nonce, TS: now}
	req.MAC = HandshakeMAC(key, m.identity.UUID, m.identity.Fingerprint(), nonce, now)
	client := peerClient(m.identity, "", "")
	defer client.CloseIdleConnections()

	raw, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+peerPathJoin+"?cluster="+clusterUUID, bytes.NewReader(raw))
	if err != nil {
		return joinResponse{}, Member{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	if err != nil {
		return joinResponse{}, Member{}, fmt.Errorf("contacting %s: %w", addr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		return joinResponse{}, Member{}, peerErrorFrom(resp.StatusCode, body)
	}
	var reply joinResponse
	if err := json.Unmarshal(body, &reply); err != nil {
		return joinResponse{}, Member{}, fmt.Errorf("parsing join response: %w", err)
	}
	if reply.Cluster.UUID != clusterUUID {
		return joinResponse{}, Member{}, errors.New("peer answered for a different cluster")
	}
	// The TLS peer must be the responder it claims to be.
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return joinResponse{}, Member{}, errors.New("peer presented no certificate")
	}
	if got := CertFingerprint(resp.TLS.PeerCertificates[0]); got != reply.Responder.Fingerprint || uuidFromCert(resp.TLS.PeerCertificates[0]) != reply.Responder.UUID {
		return joinResponse{}, Member{}, errors.New("peer certificate does not match the responder's identity")
	}
	responder, err := m.memberFromCard(reply.Responder)
	if err != nil {
		return joinResponse{}, Member{}, err
	}
	responder.LastAddresses = append([]string{addr}, responder.LastAddresses...)
	return reply, responder, nil
}

func (m *Manager) joinVia(ctx context.Context, addr, clusterUUID, key string) (ClusterInfo, error) {
	reply, responder, err := m.joinHandshake(ctx, addr, clusterUUID, key)
	if err != nil {
		return ClusterInfo{}, err
	}
	members := make([]Member, 0, len(reply.Members)+1)
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
	if err := m.Activate(); err != nil {
		return Member{}, err
	}
	if !m.withinNodeLimit(len(m.store.Members()) + 2) {
		return Member{}, &LimitError{Limit: m.NodeLimit(), Current: len(m.store.Members()) + 1}
	}
	var target string
	if target = withDefaultPort(address); target == "" {
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
		return Member{}, peerErrorFrom(resp.StatusCode, body)
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

// Leave announces departure to every member and forgets the cluster. On a
// secret-provisioned node it also pauses automatic formation, otherwise the
// node would rejoin within seconds; creating or joining a cluster resumes it.
func (m *Manager) Leave(ctx context.Context) error {
	if !m.store.InCluster() {
		return ErrNotClustered
	}
	if m.AutoFormEnabled() {
		_, _ = m.store.UpdateSettings(func(s *Settings) { s.AutoFormPaused = true })
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
	if err := m.leaveLocally(); err != nil {
		return err
	}
	if !m.AutoFormEnabled() && strings.TrimSpace(m.opts.JoinToken) == "" {
		// A machine that left on purpose is a single machine again: stop
		// listening and broadcasting until the operator clusters it again.
		m.deactivate()
		_, _ = m.store.UpdateSettings(func(s *Settings) { s.Enabled = false })
	}
	return nil
}

func (m *Manager) leaveLocally() error {
	// Former members stay visible as discovered nodes of the cluster we
	// left, so a re-join needs no fresh multicast announcement from them.
	var former []Observation
	if c := m.store.Cluster(); c != nil {
		for _, mem := range m.store.Members() {
			addrs := m.dir.Candidates(mem.UUID)
			if len(addrs) == 0 {
				continue
			}
			ip, err := netip.ParseAddr(endpointHost(addrs[0]))
			if err != nil {
				continue
			}
			former = append(former, Observation{
				Announcement: Announcement{UUID: mem.UUID, ClusterUUID: c.UUID, Name: mem.Name, Protocol: ProtocolVersion, ClusterPort: portOf(addrs[0]), APIPort: mem.APIPort},
				Addr:         ip, Seen: time.Now(), Source: "former-member",
			})
		}
	}
	if err := m.store.Leave(); err != nil && !errors.Is(err, ErrNotClustered) {
		return err
	}
	m.dir.Reset()
	for _, obs := range former {
		m.dir.Observe(obs, func(string) bool { return false })
	}
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
	m.forgetNode(nodeUUID)
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
