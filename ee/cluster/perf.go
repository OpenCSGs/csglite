// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// The perf store is the entry node's own measurement of how fast each member
// serves each model, kept as an exponential moving average and persisted so a
// restart does not start the scheduler blind. It is a storage concern with no
// tie to membership, discovery or the HTTP surfaces, and lived in manager.go
// only because that is where it was written.

type perfStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]*ModelPerf
	dirty   bool
	lastFl  time.Time
}

func newPerfStore(path string) *perfStore {
	ps := &perfStore{path: path, entries: map[string]*ModelPerf{}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &ps.entries)
	}
	return ps
}

func perfKey(nodeUUID, model string) string { return nodeUUID + "\x00" + model }

func (ps *perfStore) get(nodeUUID, model string) *ModelPerf {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p, ok := ps.entries[perfKey(nodeUUID, model)]
	if !ok {
		return nil
	}
	c := *p
	return &c
}

// observe folds one measurement into the EMA (alpha 0.3).
func (ps *perfStore) observe(nodeUUID, model string, decodeTPS, promptTPS, loadSeconds float64) {
	if nodeUUID == "" || model == "" {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	key := perfKey(nodeUUID, model)
	p, ok := ps.entries[key]
	if !ok {
		p = &ModelPerf{}
		ps.entries[key] = p
	}
	const alpha = 0.3
	ema := func(cur, sample float64) float64 {
		if sample <= 0 {
			return cur
		}
		if cur <= 0 {
			return sample
		}
		return cur*(1-alpha) + sample*alpha
	}
	p.DecodeTPS = ema(p.DecodeTPS, decodeTPS)
	p.PromptTPS = ema(p.PromptTPS, promptTPS)
	p.LoadSeconds = ema(p.LoadSeconds, loadSeconds)
	p.Samples++
	ps.dirty = true
	if time.Since(ps.lastFl) > 30*time.Second {
		ps.flushLocked()
	}
}

func (ps *perfStore) flush() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.flushLocked()
}

func (ps *perfStore) flushLocked() {
	if !ps.dirty || ps.path == "" {
		return
	}
	raw, err := json.MarshalIndent(ps.entries, "", "  ")
	if err != nil {
		return
	}
	if err := writeFileAtomic(ps.path, raw, 0o600); err == nil {
		ps.dirty = false
		ps.lastFl = time.Now()
	}
}
