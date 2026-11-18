package server

import (
	"strings"
	"sync"
	"time"
)

// marketplaceLocalInferenceLimits bounds what this machine can actually host.
// A zero field means "unknown", which disables that check instead of hiding
// models: a failed hardware probe must not empty the Marketplace list.
type marketplaceLocalInferenceLimits struct {
	// maxDownloadBytes is the free space on the storage volume.
	maxDownloadBytes int64
	// maxMemoryBytes is the memory a model can occupy: RAM, plus discrete GPU
	// memory when it is not shared with the CPU.
	maxMemoryBytes int64
}

const marketplaceLocalInferenceLimitsTTL = time.Minute

var (
	marketplaceLocalInferenceLimitsMu     sync.Mutex
	marketplaceLocalInferenceLimitsAt     time.Time
	marketplaceLocalInferenceLimitsDir    string
	marketplaceLocalInferenceLimitsCached marketplaceLocalInferenceLimits
)

// marketplaceResourceLimits reports the resource limits that apply to this
// server.
func (s *Server) marketplaceResourceLimits() marketplaceLocalInferenceLimits {
	return marketplaceResourceLimitsFor(s.clusterFormatsOnly(), s.probeResourceLimits)
}

// clusterFormatsOnly reports whether this node belongs to a LAN cluster. Such a
// node can schedule a model on a peer, so only the model format decides there.
func (s *Server) clusterFormatsOnly() bool {
	return s.cluster != nil && s.cluster.Store().InCluster()
}

func (s *Server) probeResourceLimits() marketplaceLocalInferenceLimits {
	if s.localInferenceLimits != nil {
		return s.localInferenceLimits()
	}
	// Models may live on a different volume than the storage root.
	dir := strings.TrimSpace(s.cfg.ModelDir)
	if dir == "" {
		dir = s.storageRoot
	}
	return cachedLocalInferenceLimits(dir)
}

func marketplaceResourceLimitsFor(clusterFormatsOnly bool, probe func() marketplaceLocalInferenceLimits) marketplaceLocalInferenceLimits {
	if clusterFormatsOnly {
		return marketplaceLocalInferenceLimits{}
	}
	return probe()
}

func cachedLocalInferenceLimits(storageRoot string) marketplaceLocalInferenceLimits {
	marketplaceLocalInferenceLimitsMu.Lock()
	defer marketplaceLocalInferenceLimitsMu.Unlock()
	// The cache is keyed by the probed directory: changing ModelDir at runtime
	// must not keep judging by the old volume for a minute.
	if !marketplaceLocalInferenceLimitsAt.IsZero() &&
		marketplaceLocalInferenceLimitsDir == storageRoot &&
		time.Since(marketplaceLocalInferenceLimitsAt) < marketplaceLocalInferenceLimitsTTL {
		return marketplaceLocalInferenceLimitsCached
	}
	marketplaceLocalInferenceLimitsCached = probeLocalInferenceLimits(storageRoot)
	marketplaceLocalInferenceLimitsDir = storageRoot
	marketplaceLocalInferenceLimitsAt = time.Now()
	return marketplaceLocalInferenceLimitsCached
}

// probeLocalInferenceLimits samples the hardware. Disk and GPU probes are
// comparatively slow on some platforms, so callers go through the cache above.
func probeLocalInferenceLimits(storageRoot string) marketplaceLocalInferenceLimits {
	limits := marketplaceLocalInferenceLimits{}
	if _, free, err := diskUsage(storageRoot); err == nil && free > 0 {
		limits.maxDownloadBytes = int64(free)
	}
	_, ramTotal, _ := getRAMInfo()
	memory := ramTotal
	if gpu := getGPUInfo(ramTotal); !gpu.SharedMemory {
		// Unified memory (Apple Silicon, integrated GPUs) is already counted in
		// RAMTotal; adding it again would double the budget.
		memory += gpu.VRAMTotal
	}
	limits.maxMemoryBytes = int64(memory)
	return limits
}
