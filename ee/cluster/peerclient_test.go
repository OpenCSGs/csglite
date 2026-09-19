// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import "testing"

// Forwarded inference is the hot path: a client built per request pays a fresh
// TLS handshake every time and abandons a transport holding idle connections.
// The same member must therefore get the same client back.
func TestPeerClientCacheReusesOneClientPerMember(t *testing.T) {
	id, err := LoadOrCreateIdentity(t.TempDir(), "n")
	if err != nil {
		t.Fatal(err)
	}
	c := newPeerClientCache(id)

	first := c.get("uuid-a", "fp-1")
	if second := c.get("uuid-a", "fp-1"); second != first {
		t.Fatal("the same member got a second client; every request would re-handshake")
	}
	if other := c.get("uuid-b", "fp-1"); other == first {
		t.Fatal("two members share one client, so a peer could be reached with the wrong pin")
	}

	// A member that re-pins with a new certificate must not keep being reached
	// through a client that still trusts the old one.
	if repinned := c.get("uuid-a", "fp-2"); repinned == first {
		t.Fatal("a new fingerprint reused the client pinned to the old certificate")
	}

	// An unpinned caller (join, seed probe) gets a throwaway client: pooling a
	// connection that trusts any certificate buys nothing.
	if a, b := c.get("", ""), c.get("", ""); a == b {
		t.Fatal("unpinned clients are pooled")
	}

	c.closeAll()
	if after := c.get("uuid-a", "fp-2"); after == nil {
		t.Fatal("cache unusable after closeAll")
	}
}
