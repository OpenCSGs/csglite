// Copyright (c) OpenCSG. Enterprise Edition. See ee/LICENSE.

package cluster

import (
	"net/http"
	"sync"
)

// peerClientCache hands out one HTTP client per pinned member.
//
// A client owns a connection pool, so building one per call meant every
// forwarded inference request paid a fresh TLS handshake and left a transport
// behind holding idle connections nothing would ever reuse. Keeping one client
// per member makes the second request to a node reuse the first one's
// connection, which is the difference between a handshake and a write on the
// hot path.
//
// The pinned certificate fingerprint is part of the identity of a client: when
// a member re-pins with a new certificate the old client would still trust the
// old one, so it is closed and replaced rather than reused.
type peerClientCache struct {
	identity *Identity

	mu      sync.Mutex
	clients map[string]*pinnedClient
}

type pinnedClient struct {
	fingerprint string
	client      *http.Client
}

func newPeerClientCache(id *Identity) *peerClientCache {
	return &peerClientCache{identity: id, clients: map[string]*pinnedClient{}}
}

// get returns the shared client for a pinned member. An unpinned caller (empty
// uuid, used by joins and seed probes) gets a throwaway client instead: those
// calls happen once and trust any certificate, so pooling them would keep a
// permissive connection alive for no gain.
func (c *peerClientCache) get(uuid, fingerprint string) *http.Client {
	if uuid == "" {
		return peerClient(c.identity, "", "")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.clients[uuid]; ok {
		if existing.fingerprint == fingerprint {
			return existing.client
		}
		existing.client.CloseIdleConnections()
		delete(c.clients, uuid)
	}
	client := peerClient(c.identity, uuid, fingerprint)
	c.clients[uuid] = &pinnedClient{fingerprint: fingerprint, client: client}
	return client
}

// forget drops a member's client, for example after it leaves the cluster.
func (c *peerClientCache) forget(uuid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.clients[uuid]; ok {
		existing.client.CloseIdleConnections()
		delete(c.clients, uuid)
	}
}

// closeAll releases every pooled connection. Called when the manager stops.
func (c *peerClientCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for uuid, existing := range c.clients {
		existing.client.CloseIdleConnections()
		delete(c.clients, uuid)
	}
}
