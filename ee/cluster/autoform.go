// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Automatic formation: every node provisioned with the same shared secret
// derives the same cluster identity and join token from it, so the first node
// up founds the cluster and every later node finds it over discovery and joins
// with the derived token. No create or join step is ever typed. Two nodes that
// boot at the same moment each found a one-node cluster with the same UUID;
// when either sees the other advertising that UUID it runs the join handshake
// against it and the two member tables merge.

const (
	// DefaultAutoFormName names a cluster formed from a secret.
	DefaultAutoFormName = "CSGLite Cluster"
	// MinAutoFormSecretLen rejects empty or near-empty secrets; anything
	// shorter than RecommendedAutoFormSecretLen is accepted with a warning,
	// because the operator may deliberately pick something short on a
	// physically private network.
	MinAutoFormSecretLen         = 4
	RecommendedAutoFormSecretLen = 12
)

// Timing of automatic formation; variables so tests can shorten them.
var (
	// autoFormGrace is how long a fresh node listens for an existing cluster
	// before founding its own, plus a per-node jitter so simultaneous boots
	// rarely both found.
	autoFormGrace     = 8 * time.Second
	autoFormMaxJitter = 6 * time.Second
	autoFormInterval  = 5 * time.Second
)

// autoFormNamespace is the UUIDv5 namespace for derived cluster identities.
var autoFormNamespace = uuid.MustParse("6f2c7b4e-9d3a-4c1f-8e5b-2a1d0c9b7e61")

// DeriveAutoForm turns a shared secret into the cluster UUID and join token
// every node with that secret agrees on. The token secret is an HMAC of the
// secret, so the shared secret itself never appears on disk or on the wire:
// only its derivative (and, in cluster.json, that derivative's hash).
func DeriveAutoForm(secret string) (clusterUUID, joinToken string, err error) {
	secret = strings.TrimSpace(secret)
	if len(secret) < MinAutoFormSecretLen {
		return "", "", errors.New("cluster secret must be at least 4 characters")
	}
	clusterUUID = uuid.NewSHA1(autoFormNamespace, []byte("csglite-cluster:"+secret)).String()
	mac := hmac.New(sha256.New, []byte("csglite-join-token"))
	mac.Write([]byte(secret))
	derived := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(mac.Sum(nil)[:20]))
	return clusterUUID, joinTokenPrefix + "-" + clusterUUID + "-" + derived, nil
}

// AutoFormEnabled reports whether this node was provisioned with a secret.
func (m *Manager) AutoFormEnabled() bool {
	return strings.TrimSpace(m.opts.AutoFormSecret) != ""
}

// autoFormLoop keeps a secret-provisioned node in its derived cluster.
func (m *Manager) autoFormLoop() {
	ctx := m.context()
	clusterUUID, token, err := DeriveAutoForm(m.opts.AutoFormSecret)
	if err != nil {
		m.logf("cluster: automatic formation disabled: %v", err)
		return
	}
	if len(strings.TrimSpace(m.opts.AutoFormSecret)) < RecommendedAutoFormSecretLen {
		m.logf("cluster: the cluster secret is short (%d characters); anyone on the network who guesses it can join. %d or more is recommended", len(strings.TrimSpace(m.opts.AutoFormSecret)), RecommendedAutoFormSecretLen)
	}
	_, tokenSecret, _ := ParseJoinToken(token)
	tokenHash := hashSecret(tokenSecret)
	name := strings.TrimSpace(m.opts.AutoFormName)
	if name == "" {
		name = DefaultAutoFormName
	}
	startedAt := time.Now()
	grace := autoFormGrace + jitter(m.identity.UUID, autoFormMaxJitter)
	ticker := time.NewTicker(autoFormInterval)
	defer ticker.Stop()
	first := true
	for {
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		first = false
		if m.store.Settings().AutoFormPaused {
			continue
		}
		current := m.store.Cluster()
		switch {
		case current != nil && current.UUID != clusterUUID:
			// An operator paired this node into a different cluster on
			// purpose; that decision stands until they leave it.
			continue
		case current == nil:
			// Join an existing cluster if one is visible; otherwise, once the
			// grace period has passed, found it.
			if m.autoFormJoin(ctx, clusterUUID, token) {
				continue
			}
			if time.Since(startedAt) < grace {
				continue
			}
			info := ClusterInfo{UUID: clusterUUID, Name: name, CreatedAt: time.Now().UTC()}
			if err := m.store.CreateDerived(info, tokenHash); err != nil {
				if !errors.Is(err, ErrAlreadyClustered) {
					m.logf("cluster: automatic formation: %v", err)
				}
				continue
			}
			m.dir.Reset()
			m.reannounce()
			m.logf("cluster: founded %q (%s) from the shared secret; nodes with the same secret will join automatically", name, shortUUID(clusterUUID))
		default:
			// Already in the derived cluster: absorb any node advertising the
			// same cluster that we do not list yet (a peer that founded in
			// parallel, or a peer we were partitioned from).
			m.autoFormMerge(ctx, clusterUUID, token)
		}
	}
}

// autoFormJoin joins a visible member of the derived cluster.
func (m *Manager) autoFormJoin(ctx context.Context, clusterUUID, token string) bool {
	var targets []string
	for _, obs := range m.dir.Discovered(discoveredMaxAge) {
		if obs.ClusterUUID == clusterUUID {
			targets = append(targets, obs.Endpoint())
		}
	}
	if len(targets) == 0 {
		return false
	}
	joinCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, addr := range targets {
		if _, err := m.Join(joinCtx, token, addr); err == nil {
			return true
		} else {
			var pe *peerError
			if errors.As(err, &pe) && pe.Status == http.StatusForbidden && pe.Body.Code == "feature_not_licensed" {
				m.logf("cluster: automatic join refused: %s", pe.Body.Error)
				return false
			}
			m.logf("cluster: automatic join via %s failed: %v", addr, err)
		}
	}
	return false
}

// autoFormMerge folds in nodes of the same derived cluster we do not know.
func (m *Manager) autoFormMerge(ctx context.Context, clusterUUID, token string) {
	_, tokenSecret, err := ParseJoinToken(token)
	if err != nil {
		return
	}
	key := hashSecret(tokenSecret)
	for _, obs := range m.dir.Discovered(discoveredMaxAge) {
		if obs.ClusterUUID != clusterUUID {
			continue
		}
		if _, known := m.store.Member(obs.UUID); known {
			continue
		}
		mergeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := m.mergeVia(mergeCtx, obs.Endpoint(), clusterUUID, key)
		cancel()
		if err != nil {
			m.logf("cluster: merging with %s (%s) failed: %v", obs.Name, shortUUID(obs.UUID), err)
			continue
		}
		m.logf("cluster: merged with %s (%s), which had formed the same cluster in parallel", obs.Name, shortUUID(obs.UUID))
	}
}

// mergeVia runs the join handshake against a node of our own cluster and
// unions the member tables instead of replacing ours.
func (m *Manager) mergeVia(ctx context.Context, addr, clusterUUID, key string) error {
	reply, responder, err := m.joinHandshake(ctx, addr, clusterUUID, key)
	if err != nil {
		return err
	}
	members := []Member{responder}
	for _, card := range reply.Members {
		if card.UUID == m.identity.UUID || card.UUID == responder.UUID {
			continue
		}
		mem, err := m.memberFromCard(card)
		if err != nil {
			continue
		}
		members = append(members, mem)
	}
	for i, mem := range members {
		// The responder proved its identity over TLS and knew the token;
		// its peers are trusted the way a join trusts a member table.
		if _, err := m.store.Upsert(mem, i == 0); err != nil {
			m.logf("cluster: merge: %v", err)
			continue
		}
		m.dir.Track(mem.UUID, m.seedAddressesFor(mem))
		m.dir.DropDiscovered(mem.UUID)
	}
	m.dir.LearnAddress(responder.UUID, addr, time.Now())
	m.wakeGossip()
	return nil
}
