// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"net/http"
	"testing"
	"time"
)

const gb = 1 << 30

func nodeWithModel(uuid string, model ModelStatus, opts func(*Status)) Candidate {
	st := &Status{
		UUID: uuid, Name: uuid, Licensed: true, AcceptWork: true, State: NodeStateActive, Weight: 100,
		GPUs: []GPUStatus{{Name: "GPU", VRAMTotal: 24 * gb, VRAMUsed: 2 * gb, UsageKnown: true}},
		CPU:  CPUStatus{Cores: 16},
		RAM:  RAMStatus{Total: 64 * gb, Used: 8 * gb},
		Disk: DiskStatus{Total: 1000 * gb, Free: 500 * gb, ReadMBps: 2000},
	}
	st.Models = []ModelStatus{model}
	if opts != nil {
		opts(st)
	}
	return Candidate{UUID: uuid, Name: uuid, Status: st, Health: HealthHealthy}
}

func TestRankPrefersWarmIdleNodeOverColdOne(t *testing.T) {
	warm := nodeWithModel("warm", ModelStatus{ID: "m", Size: 8 * gb, Loaded: true, Slots: 4, Active: 0}, nil)
	cold := nodeWithModel("cold", ModelStatus{ID: "m", Size: 8 * gb}, nil)
	ranked, ex := Rank(RankRequest{Model: "m"}, []Candidate{cold, warm})
	if len(ranked) != 2 || ranked[0].UUID != "warm" {
		t.Fatalf("order %v (%s)", ex.Order, summarizeExplain(ex))
	}
}

func TestRankFastColdCardBeatsQueuedSlowCard(t *testing.T) {
	// A 3060 with a queue three deep against a 4090 that has to load a
	// 6 GB model from NVMe: the 4090 should win once its measured speed is
	// known.
	slow := nodeWithModel("slow", ModelStatus{ID: "m", Size: 6 * gb, Loaded: true, Slots: 1, Active: 4, Perf: &ModelPerf{DecodeTPS: 12, PromptTPS: 400}}, nil)
	fast := nodeWithModel("fast", ModelStatus{ID: "m", Size: 6 * gb, Perf: &ModelPerf{DecodeTPS: 90, PromptTPS: 3000, LoadSeconds: 4}}, nil)
	ranked, ex := Rank(RankRequest{Model: "m", MaxTokens: 512}, []Candidate{slow, fast})
	if ranked[0].UUID != "fast" {
		t.Fatalf("expected the fast cold card first: %s", summarizeExplain(ex))
	}
}

func TestRankHardFilters(t *testing.T) {
	down := nodeWithModel("down", ModelStatus{ID: "m", Size: gb, Loaded: true}, nil)
	down.Health = HealthDown
	drain := nodeWithModel("drain", ModelStatus{ID: "m", Size: gb, Loaded: true}, func(s *Status) { s.State = NodeStateDrain; s.AcceptWork = false })
	unlicensed := nodeWithModel("unlic", ModelStatus{ID: "m", Size: gb, Loaded: true}, func(s *Status) { s.Licensed = false })
	other := nodeWithModel("other", ModelStatus{ID: "x", Size: gb, Loaded: true}, nil)
	broken := nodeWithModel("broken", ModelStatus{ID: "m", Size: gb, Loaded: true}, nil)
	broken.Breaker = true
	noVRAM := nodeWithModel("novram", ModelStatus{ID: "m", Size: 30 * gb}, nil)
	ok := nodeWithModel("ok", ModelStatus{ID: "m", Size: gb, Loaded: true}, nil)
	ranked, ex := Rank(RankRequest{Model: "m"}, []Candidate{down, drain, unlicensed, other, broken, noVRAM, ok})
	if len(ranked) != 1 || ranked[0].UUID != "ok" {
		t.Fatalf("eligible %v: %s", ex.Order, summarizeExplain(ex))
	}
	reasons := map[string]string{}
	for _, c := range ex.Candidates {
		reasons[c.UUID] = c.Excluded
	}
	if reasons["down"] == "" || reasons["drain"] == "" || reasons["unlic"] == "" || reasons["other"] == "" || reasons["broken"] == "" || reasons["novram"] == "" {
		t.Fatalf("missing exclusion reasons: %v", reasons)
	}
}

func TestRankAllowsOneEvictionWhenNothingFits(t *testing.T) {
	a := nodeWithModel("a", ModelStatus{ID: "m", Size: 30 * gb}, func(s *Status) { s.GPUs[0].VRAMUsed = 20 * gb })
	b := nodeWithModel("b", ModelStatus{ID: "m", Size: 30 * gb}, func(s *Status) { s.GPUs[0].VRAMUsed = 10 * gb })
	ranked, _ := Rank(RankRequest{Model: "m"}, []Candidate{a, b})
	if len(ranked) != 1 || ranked[0].UUID != "b" {
		t.Fatalf("expected exactly the node with the most free memory to be allowed to evict, got %+v", ranked)
	}
}

func TestRankPenaltiesAndAffinity(t *testing.T) {
	hot := nodeWithModel("hot", ModelStatus{ID: "m", Size: gb, Loaded: true, Slots: 4}, func(s *Status) { temp := 90; s.GPUs[0].Temperature = &temp })
	cool := nodeWithModel("cool", ModelStatus{ID: "m", Size: gb, Loaded: true, Slots: 4}, nil)
	ranked, _ := Rank(RankRequest{Model: "m"}, []Candidate{hot, cool})
	if ranked[0].UUID != "cool" {
		t.Fatalf("thermal throttling not penalised: %+v", ranked)
	}
	// Affinity wins even against a slightly better node while the queue is short.
	ranked, _ = Rank(RankRequest{Model: "m", AffinityUUID: "hot", AffinityMaxQueue: 2}, []Candidate{hot, cool})
	if ranked[0].UUID != "hot" || !ranked[0].Affinity {
		t.Fatalf("affinity ignored: %+v", ranked)
	}
	// ...but not when the affinity node is queued too deep.
	deep := nodeWithModel("hot", ModelStatus{ID: "m", Size: gb, Loaded: true, Slots: 1, Active: 5}, nil)
	ranked, _ = Rank(RankRequest{Model: "m", AffinityUUID: "hot", AffinityMaxQueue: 2}, []Candidate{deep, cool})
	if ranked[0].UUID != "cool" {
		t.Fatalf("deep queue should break affinity: %+v", ranked)
	}
	// Operator weight demotes a node.
	light := nodeWithModel("light", ModelStatus{ID: "m", Size: gb, Loaded: true, Slots: 4}, func(s *Status) { s.Weight = 20 })
	ranked, _ = Rank(RankRequest{Model: "m"}, []Candidate{light, cool})
	if ranked[0].UUID != "cool" {
		t.Fatalf("weight ignored: %+v", ranked)
	}
}

func TestRankPinnedNode(t *testing.T) {
	a := nodeWithModel("a", ModelStatus{ID: "m", Size: gb, Loaded: true}, nil)
	b := nodeWithModel("b", ModelStatus{ID: "m", Size: gb, Loaded: true}, nil)
	ranked, _ := Rank(RankRequest{Model: "m", PinnedUUID: "b"}, []Candidate{a, b})
	if len(ranked) != 1 || ranked[0].UUID != "b" {
		t.Fatalf("pin ignored: %+v", ranked)
	}
}

func TestRankIsStableAcrossEntryNodes(t *testing.T) {
	a := nodeWithModel("a", ModelStatus{ID: "m", Size: gb, Loaded: true, Slots: 4}, nil)
	b := nodeWithModel("b", ModelStatus{ID: "m", Size: gb, Loaded: true, Slots: 4}, nil)
	first, _ := Rank(RankRequest{Model: "m"}, []Candidate{a, b})
	second, _ := Rank(RankRequest{Model: "m"}, []Candidate{b, a})
	if first[0].UUID != second[0].UUID {
		t.Fatal("tie-break depends on input order")
	}
	if Rendezvous("thread-1", []string{"a", "b", "c"}) != Rendezvous("thread-1", []string{"c", "b", "a"}) {
		t.Fatal("rendezvous depends on order")
	}
	moved := 0
	for i := 0; i < 200; i++ {
		key := "k" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		before := Rendezvous(key, []string{"a", "b", "c", "d"})
		after := Rendezvous(key, []string{"a", "b", "c"})
		if before != "d" && before != after {
			moved++
		}
	}
	if moved != 0 {
		t.Fatalf("%d keys not on the departed node moved", moved)
	}
}

func TestUsageFromTail(t *testing.T) {
	body := []byte(`data: {"choices":[{"delta":{"content":"x"}}]}

data: {"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":40}}

data: [DONE]
`)
	p, c := usageFromTail(body)
	if p != 120 || c != 40 {
		t.Fatalf("usage %d/%d", p, c)
	}
	p, c = usageFromTail([]byte(`{"id":"x","usage":{"input_tokens":7,"output_tokens":9}}`))
	if p != 7 || c != 9 {
		t.Fatalf("anthropic usage %d/%d", p, c)
	}
}

// A node that answers 429 is busy, not broken. Trying it again on the very
// next request only moves the rejection around the cluster, so it is held out
// for as long as it asked and the scheduler must honour that.
func TestRankSkipsANodeInsideItsRequestedCooldown(t *testing.T) {
	busy := nodeWithModel("busy", ModelStatus{ID: "m", Size: 1 * gb, Loaded: true, Slots: 4}, nil)
	busy.Cooling = true
	free := nodeWithModel("free", ModelStatus{ID: "m", Size: 1 * gb, Loaded: true, Slots: 4}, nil)
	ranked, _ := Rank(RankRequest{Model: "m"}, []Candidate{busy, free})
	for _, r := range ranked {
		if r.UUID == "busy" && r.Excluded == "" {
			t.Fatal("a node inside its cooldown was still eligible")
		}
		if r.UUID == "free" && r.Excluded != "" {
			t.Fatalf("the idle node was excluded: %s", r.Excluded)
		}
	}
}

// Retry-After is allowed to be a number of seconds or an absolute date, and a
// node that sends neither still has to be held out for a sane default.
func TestRetryAfterUntilReadsBothHeaderForms(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	if got := retryAfterUntil("30", now); !got.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("seconds form gave %v", got)
	}
	if got := retryAfterUntil(now.Add(2*time.Minute).Format(http.TimeFormat), now); !got.After(now.Add(time.Minute)) {
		t.Fatalf("date form gave %v", got)
	}
	if got := retryAfterUntil("", now); !got.Equal(now.Add(defaultRateLimitCooldown)) {
		t.Fatalf("missing header gave %v, want the default cooldown", got)
	}
	if got := retryAfterUntil("nonsense", now); !got.Equal(now.Add(defaultRateLimitCooldown)) {
		t.Fatalf("unparsable header gave %v, want the default cooldown", got)
	}
}
