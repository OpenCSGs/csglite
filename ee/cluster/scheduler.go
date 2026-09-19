// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Candidate is a member as the scheduler sees it.
type Candidate struct {
	UUID     string
	Name     string
	Local    bool
	Status   *Status
	Health   Health
	Reserved int
	Breaker  bool
}

// RankRequest describes the request being placed.
type RankRequest struct {
	Model            string
	PromptTokens     int
	MaxTokens        int
	PreferLocal      bool
	AffinityUUID     string
	AffinityMaxQueue int
	// PinnedUUID restricts placement to one node (source=node:<uuid>).
	PinnedUUID string
}

// Ranked is one candidate's verdict, in explain form.
type Ranked struct {
	UUID     string   `json:"uuid"`
	Name     string   `json:"name"`
	Local    bool     `json:"local"`
	Eligible bool     `json:"eligible"`
	Excluded string   `json:"excluded,omitempty"`
	Warm     bool     `json:"warm"`
	Affinity bool     `json:"affinity"`
	Seconds  float64  `json:"estimated_seconds"`
	Factors  []string `json:"factors,omitempty"`
	Rank     int      `json:"rank"`
}

// Explain is the full scheduling decision for one request.
type Explain struct {
	Model      string   `json:"model"`
	Order      []string `json:"order"`
	Candidates []Ranked `json:"candidates"`
}

const (
	defaultPromptTPS   = 800.0
	defaultEstTokens   = 256
	defaultDiskMBps    = 800.0
	vramHeadroomFactor = 1.2
)

// defaultDecodeTPS guesses generation speed from model size when no sample
// exists yet; the estimate only steers the first requests until perf EMAs
// take over.
func defaultDecodeTPS(size int64) float64 {
	gb := float64(size) / (1 << 30)
	switch {
	case gb <= 0:
		return 30
	case gb <= 6:
		return 60
	case gb <= 12:
		return 35
	case gb <= 24:
		return 18
	default:
		return 8
	}
}

// Rank filters and orders candidates by estimated completion time.
func Rank(req RankRequest, cands []Candidate) ([]Ranked, Explain) {
	estTokens := req.MaxTokens
	if estTokens <= 0 || estTokens > 4096 {
		estTokens = defaultEstTokens
	}
	promptTokens := req.PromptTokens
	if promptTokens <= 0 {
		promptTokens = 512
	}

	out := make([]Ranked, 0, len(cands))
	needsEviction := 0
	for _, c := range cands {
		r := Ranked{UUID: c.UUID, Name: c.Name, Local: c.Local}
		st := c.Status
		switch {
		case req.PinnedUUID != "" && c.UUID != req.PinnedUUID:
			r.Excluded = "not the pinned node"
		case st == nil:
			r.Excluded = "no status yet"
		case c.Health == HealthDown || c.Health == HealthProbing:
			r.Excluded = "node is " + string(c.Health)
		case c.Health == HealthUnknown:
			r.Excluded = "node not reached yet"
		case !st.Licensed:
			r.Excluded = "node exceeds its license node cap"
		case !st.AcceptWork || st.State != NodeStateActive:
			r.Excluded = "node state is " + string(st.State)
		case c.Breaker:
			r.Excluded = "model failed on this node recently"
		}
		if r.Excluded != "" {
			out = append(out, r)
			continue
		}
		m, ok := st.Model(req.Model)
		if !ok {
			r.Excluded = "model not present"
			out = append(out, r)
			continue
		}
		perf := ModelPerf{}
		if m.Perf != nil {
			perf = *m.Perf
		}
		decodeTPS := perf.DecodeTPS
		if decodeTPS <= 0 {
			decodeTPS = defaultDecodeTPS(m.Size)
			r.Factors = append(r.Factors, fmt.Sprintf("decode speed estimated %.0f tok/s (no samples)", decodeTPS))
		} else {
			r.Factors = append(r.Factors, fmt.Sprintf("decode %.1f tok/s measured", decodeTPS))
		}
		promptTPS := perf.PromptTPS
		if promptTPS <= 0 {
			promptTPS = defaultPromptTPS
		}
		perRequest := float64(promptTokens)/promptTPS + float64(estTokens)/decodeTPS

		var seconds float64
		active := m.Active + c.Reserved
		warm := m.Loaded || m.Loading
		r.Warm = warm
		switch {
		case m.Loaded:
			slots := m.Slots
			if slots <= 0 {
				slots = 1
			}
			queued := active - slots + 1
			if queued < 0 {
				queued = 0
			}
			if queued > 0 {
				seconds += float64(queued) * perRequest
				r.Factors = append(r.Factors, fmt.Sprintf("%d request(s) queued ahead", queued))
			}
		case m.Loading:
			load := perf.LoadSeconds
			if load <= 0 {
				load = float64(m.Size) / (defaultDiskMBps * 1e6)
			}
			seconds += load / 2 // already part way through
			r.Factors = append(r.Factors, "model is loading now")
		default:
			load := perf.LoadSeconds
			if load <= 0 {
				mbps := defaultDiskMBps
				if st.Disk.ReadMBps > 0 {
					mbps = float64(st.Disk.ReadMBps)
				}
				load = float64(m.Size) / (mbps * 1e6)
			}
			if st.Disk.IOBusy || len(st.Jobs.Pulling) > 0 || len(st.Jobs.Converting) > 0 {
				load *= 2
				r.Factors = append(r.Factors, "disk busy with a download or conversion")
			}
			seconds += load
			r.Factors = append(r.Factors, fmt.Sprintf("cold start ~%.0fs", load))
			// Memory check: VRAM on discrete GPUs, RAM on unified memory.
			need := uint64(float64(m.Size) * vramHeadroomFactor)
			free, known := freeMemoryForModel(st)
			if known && free < need {
				r.Excluded = "not enough free memory to load without evicting"
				needsEviction++
				out = append(out, r)
				continue
			}
			if active > 0 {
				seconds += float64(active) * perRequest
			}
		}
		seconds += perRequest

		// Multiplicative penalties.
		mult := 1.0
		for _, g := range st.GPUs {
			if g.Throttled || (g.Temperature != nil && *g.Temperature >= 85) {
				mult *= 1.5
				r.Factors = append(r.Factors, "GPU thermal throttling")
				break
			}
		}
		for _, g := range st.GPUs {
			if g.PowerLimit != nil && g.PowerDraw != nil && *g.PowerLimit > 0 && float64(*g.PowerDraw) >= 0.95*float64(*g.PowerLimit) {
				mult *= 1.2
				r.Factors = append(r.Factors, "GPU at power limit")
				break
			}
		}
		if m.NGPULayers >= 0 && m.NGPULayers < 999 && m.Loaded && st.CPU.Load1 != nil && st.CPU.Cores > 0 && *st.CPU.Load1/float64(st.CPU.Cores) > 0.8 {
			mult *= 1.5
			r.Factors = append(r.Factors, "partial GPU offload under CPU contention")
		}
		if st.RAM.Total > 0 && !st.RAM.Unified {
			ramFree := st.RAM.Total - min(st.RAM.Used, st.RAM.Total)
			if ramFree < uint64(float64(m.Size)*0.5) {
				mult *= 1.3
				r.Factors = append(r.Factors, "little RAM left for the weight cache")
			}
		}
		if st.Inflight == 0 && active == 0 {
			for _, g := range st.GPUs {
				if g.Util != nil && *g.Util >= 60 {
					mult *= 1.3
					r.Factors = append(r.Factors, "GPU busy with another process")
					break
				}
			}
		}
		weight := st.Weight
		if weight <= 0 {
			weight = 100
		}
		if weight != 100 {
			mult *= 100.0 / float64(weight)
			r.Factors = append(r.Factors, fmt.Sprintf("operator weight %d", weight))
		}
		seconds *= mult
		r.Seconds = math.Round(seconds*100) / 100
		r.Eligible = true
		if req.AffinityUUID != "" && c.UUID == req.AffinityUUID && m.Loaded {
			slots := m.Slots
			if slots <= 0 {
				slots = 1
			}
			queued := active - slots + 1
			if queued <= req.AffinityMaxQueue {
				r.Affinity = true
				r.Factors = append(r.Factors, "session affinity")
			} else {
				r.Factors = append(r.Factors, fmt.Sprintf("affinity skipped: %d queued > %d", queued, req.AffinityMaxQueue))
			}
		}
		out = append(out, r)
	}

	// If nothing fits without eviction, allow exactly one node to evict.
	eligible := 0
	for _, r := range out {
		if r.Eligible {
			eligible++
		}
	}
	if eligible == 0 && needsEviction > 0 {
		bestIdx, bestFree := -1, uint64(0)
		for i, r := range out {
			if r.Excluded != "not enough free memory to load without evicting" {
				continue
			}
			c := candidateByUUID(cands, r.UUID)
			free, _ := freeMemoryForModel(c.Status)
			if bestIdx == -1 || free > bestFree {
				bestIdx, bestFree = i, free
			}
		}
		if bestIdx >= 0 {
			out[bestIdx].Eligible = true
			out[bestIdx].Excluded = ""
			out[bestIdx].Seconds = 3600
			out[bestIdx].Factors = append(out[bestIdx].Factors, "will evict other models to load")
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Eligible != b.Eligible {
			return a.Eligible
		}
		if !a.Eligible {
			return a.UUID < b.UUID
		}
		if a.Affinity != b.Affinity {
			return a.Affinity
		}
		if math.Abs(a.Seconds-b.Seconds) > 1e-9 {
			return a.Seconds < b.Seconds
		}
		if req.PreferLocal && a.Local != b.Local {
			return a.Local
		}
		return stableTiebreak(req.Model, a.UUID) < stableTiebreak(req.Model, b.UUID)
	})
	ex := Explain{Model: req.Model}
	for i := range out {
		out[i].Rank = i + 1
		if out[i].Eligible {
			ex.Order = append(ex.Order, out[i].UUID)
		}
	}
	ex.Candidates = out
	ranked := make([]Ranked, 0, len(ex.Order))
	for _, r := range out {
		if r.Eligible {
			ranked = append(ranked, r)
		}
	}
	return ranked, ex
}

func candidateByUUID(cands []Candidate, id string) Candidate {
	for _, c := range cands {
		if c.UUID == id {
			return c
		}
	}
	return Candidate{}
}

// freeMemoryForModel returns the memory a cold load can use and whether the
// figure is known at all (no GPU telemetry means "do not filter").
func freeMemoryForModel(st *Status) (uint64, bool) {
	if st == nil {
		return 0, false
	}
	unified := st.RAM.Unified
	for _, g := range st.GPUs {
		if g.Shared {
			unified = true
		}
	}
	if unified || len(st.GPUs) == 0 {
		if st.RAM.Total == 0 {
			return 0, false
		}
		return st.RAM.Total - min(st.RAM.Used, st.RAM.Total), true
	}
	known := false
	for _, g := range st.GPUs {
		if g.UsageKnown && g.VRAMTotal > 0 {
			known = true
		}
	}
	if !known {
		return 0, false
	}
	return st.VRAMFree(), true
}

// stableTiebreak spreads same-score nodes deterministically per model, so two
// entry nodes agree and one node does not always win ties.
func stableTiebreak(model, nodeUUID string) uint32 {
	var h uint32 = 2166136261
	for _, b := range []byte(model + "\x00" + nodeUUID) {
		h ^= uint32(b)
		h *= 16777619
	}
	return h
}

// summarizeExplain renders a one-line log of the decision.
func summarizeExplain(ex Explain) string {
	parts := make([]string, 0, len(ex.Candidates))
	for _, r := range ex.Candidates {
		if r.Eligible {
			parts = append(parts, fmt.Sprintf("%s=%.1fs", shortUUID(r.UUID), r.Seconds))
		} else {
			parts = append(parts, fmt.Sprintf("%s=x(%s)", shortUUID(r.UUID), r.Excluded))
		}
	}
	return strings.Join(parts, " ")
}

func shortUUID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
