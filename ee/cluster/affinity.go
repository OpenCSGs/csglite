// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"sync"
	"time"
)

// affinityTTL matches the provider pool's conversation affinity window.
const affinityTTL = 30 * time.Minute

type affinityEntry struct {
	uuid    string
	expires time.Time
}

// Affinity remembers which node last served a conversation. It is the
// entry-local override on top of the deterministic rendezvous baseline.
type Affinity struct {
	mu      sync.Mutex
	entries map[string]affinityEntry
	now     func() time.Time
}

// NewAffinity creates an empty table.
func NewAffinity() *Affinity {
	return &Affinity{entries: map[string]affinityEntry{}, now: time.Now}
}

// Lookup returns the node that last served key, if still within the TTL.
func (a *Affinity) Lookup(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.entries[key]
	if !ok {
		return "", false
	}
	if a.now().After(e.expires) {
		delete(a.entries, key)
		return "", false
	}
	return e.uuid, true
}

// Record notes that node served key and slides the TTL.
func (a *Affinity) Record(key, nodeUUID string) {
	if key == "" || nodeUUID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries[key] = affinityEntry{uuid: nodeUUID, expires: a.now().Add(affinityTTL)}
	if len(a.entries) > 10000 {
		now := a.now()
		for k, e := range a.entries {
			if now.After(e.expires) {
				delete(a.entries, k)
			}
		}
	}
}

// Rendezvous picks the node with the highest hash of (key, node): every entry
// node computes the same answer without sharing state, and when a node
// leaves only the keys that mapped to it move.
func Rendezvous(key string, nodes []string) string {
	if key == "" || len(nodes) == 0 {
		return ""
	}
	sorted := append([]string(nil), nodes...)
	sort.Strings(sorted)
	best, bestScore := "", uint64(0)
	for _, n := range sorted {
		sum := sha256.Sum256([]byte(key + "\x00" + n))
		score := binary.BigEndian.Uint64(sum[:8])
		if best == "" || score > bestScore {
			best, bestScore = n, score
		}
	}
	return best
}

type affinityKeyContext struct{}

// WithAffinityKey attaches the conversation key the server derived from the
// request (thread id, trace id or leading-message hash).
func WithAffinityKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, affinityKeyContext{}, key)
}

// AffinityKeyFromContext reads the key set by WithAffinityKey.
func AffinityKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	key, _ := ctx.Value(affinityKeyContext{}).(string)
	return key
}
