package inference

import (
	"context"
	"sync"
)

// llama-server keeps one KV cache per slot, and picks a slot for each request
// by the longest prefix it shares with what a slot already holds. Two
// conversations from the same agent share their system prompt and tool
// definitions, so the slot holding one of them always outscores an empty
// slot: every request lands in that one slot, the others stay unused, and
// each switch between the conversations throws away everything after the
// shared prefix. Measured on Qwen3.5-2B with two slots, two interleaved agent
// conversations kept 41% of their prompt tokens cached; pinning each to its
// own slot with id_slot kept 81% and cut the wall time from 92 s to 30 s.
//
// llamaSlotScheduler does that pinning. It only acts when the server has two
// or more slots, so a default single-slot load behaves exactly as before.

type slotAffinityContextKey struct{}

// WithSlotAffinity tags a request with the conversation it belongs to, so a
// local llama-server keeps that conversation in the same slot.
func WithSlotAffinity(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, slotAffinityContextKey{}, key)
}

// SlotAffinity returns the conversation key WithSlotAffinity stored.
func SlotAffinity(ctx context.Context) string {
	key, _ := ctx.Value(slotAffinityContextKey{}).(string)
	return key
}

type llamaSlotScheduler struct {
	mu    sync.Mutex
	slots []llamaSlot
	clock uint64
}

type llamaSlot struct {
	owner    string // conversation whose context the slot holds
	busy     int    // requests currently running on the slot
	lastUsed uint64
}

func newLlamaSlotScheduler(numParallel int) *llamaSlotScheduler {
	if numParallel < 2 {
		return nil
	}
	return &llamaSlotScheduler{slots: make([]llamaSlot, numParallel)}
}

// acquire picks the slot for a request of conversation key, marks it busy and
// returns it. It returns -1 only when scheduling is off. release must be
// called once the response is done.
//
// Every request is pinned, and whichever request runs on a slot becomes its
// owner: its prompt replaces the KV cache there, so the owner is always the
// conversation whose context the slot really holds. A request without a key
// leaves the slot unowned.
//
// A conversation always goes back to its own slot, even when that slot is
// busy: llama-server then queues the request for it, which keeps the cache,
// where running on another slot would both miss the cache and overwrite
// someone else's. Any other request takes an idle slot nobody owns, then the
// idle slot used longest ago, and only when every slot is busy queues on the
// busy slot used longest ago. Leaving the choice to llama-server there would
// bring back the longest-prefix collisions this scheduler exists to avoid.
func (s *llamaSlotScheduler) acquire(key string) (int, func()) {
	if s == nil {
		return -1, func() {}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock++

	slot := -1
	if key != "" {
		slot = s.ownedSlot(key)
	}
	if slot < 0 {
		slot = s.freeSlot()
	}
	s.slots[slot].owner = key
	s.slots[slot].busy++
	s.slots[slot].lastUsed = s.clock

	var once sync.Once
	return slot, func() {
		once.Do(func() {
			s.mu.Lock()
			s.slots[slot].busy--
			s.mu.Unlock()
		})
	}
}

func (s *llamaSlotScheduler) ownedSlot(key string) int {
	for i := range s.slots {
		if s.slots[i].owner == key {
			return i
		}
	}
	return -1
}

// freeSlot picks the slot for a request that has none of its own: an idle
// slot nobody owns, then the idle slot used longest ago, then the busy slot
// used longest ago.
func (s *llamaSlotScheduler) freeSlot() int {
	idle, busy := -1, -1
	for i := range s.slots {
		slot := s.slots[i]
		if slot.busy == 0 {
			if slot.owner == "" {
				return i
			}
			if idle < 0 || slot.lastUsed < s.slots[idle].lastUsed {
				idle = i
			}
			continue
		}
		if busy < 0 || slot.lastUsed < s.slots[busy].lastUsed {
			busy = i
		}
	}
	if idle >= 0 {
		return idle
	}
	return busy
}
