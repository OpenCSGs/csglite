package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/hardware"
)

// marketplaceHardwareFingerprint renders the probed hardware in a stable form
// for the response cache key. A changed disk, memory or GPU set must not share
// a cached verdict with the previous hardware.
func marketplaceHardwareFingerprint(limits marketplaceLocalInferenceLimits) string {
	gpus := append([]marketplaceLocalInferenceGPU(nil), limits.gpus...)
	sort.Slice(gpus, func(i, j int) bool {
		if gpus[i].vendor != gpus[j].vendor {
			return gpus[i].vendor < gpus[j].vendor
		}
		if gpus[i].vramBytes != gpus[j].vramBytes {
			return gpus[i].vramBytes < gpus[j].vramBytes
		}
		return !gpus[i].sharedMemory && gpus[j].sharedMemory
	})
	var b strings.Builder
	fmt.Fprintf(&b, "&hw=%d,%d", limits.maxDownloadBytes, limits.maxMemoryBytes)
	for _, gpu := range gpus {
		fmt.Fprintf(&b, ";%s,%d,%t", gpu.vendor, gpu.vramBytes, gpu.sharedMemory)
	}
	return b.String()
}

// marketplaceLocalInferenceLimits bounds what this machine can actually host.
// A zero field means "unknown", which disables that check instead of hiding
// models: a failed hardware probe must not empty the Marketplace list.
type marketplaceLocalInferenceLimits struct {
	// maxDownloadBytes is the free space on the storage volume.
	maxDownloadBytes int64
	// maxMemoryBytes is the memory a model can occupy: RAM, plus discrete GPU
	// memory when it is not shared with the CPU.
	maxMemoryBytes int64
	// ramBytes and gpus keep the raw probe values for the local-model-filter
	// request, where the hub applies the same gates.
	ramBytes int64
	gpus     []marketplaceLocalInferenceGPU
}

// marketplaceLocalInferenceGPU is one physical GPU as the hub request reports
// it. A shared-memory GPU (Apple Silicon, integrated graphics) reports
// vramBytes=0 so the hub does not count system memory twice.
type marketplaceLocalInferenceGPU struct {
	vendor       string
	vramBytes    int64
	sharedMemory bool
}

// marketplaceLocalInferenceMaxGPUs caps the reported GPU list. Beyond it the
// largest discrete GPUs win, because the hub budget is the sum of their VRAM.
const marketplaceLocalInferenceMaxGPUs = 16

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
	limits.ramBytes = int64(ramTotal)
	limits.gpus = detectMarketplaceLocalInferenceGPUs(ramTotal)
	memory := uint64(0)
	if ramTotal > 0 {
		memory = ramTotal
	}
	for _, gpu := range limits.gpus {
		// Unified memory (Apple Silicon, integrated GPUs) is already counted in
		// RAM; adding it again would double the budget.
		if !gpu.sharedMemory && gpu.vramBytes > 0 {
			memory += uint64(gpu.vramBytes)
		}
	}
	limits.maxMemoryBytes = int64(memory)
	return limits
}

// marketplaceLocalModelFilterHardware converts the probe into the request payload.
// Values stay raw: the hub does its own gate arithmetic.
func marketplaceLocalModelFilterHardware(limits marketplaceLocalInferenceLimits, clusterSkipHardware bool) csghub.LocalModelFilterHardware {
	if clusterSkipHardware {
		return csghub.LocalModelFilterHardware{OS: runtime.GOOS, Arch: runtime.GOARCH}
	}
	gpus := make([]csghub.LocalModelFilterGPU, 0, len(limits.gpus))
	for _, gpu := range limits.gpus {
		gpus = append(gpus, csghub.LocalModelFilterGPU{
			Vendor:       gpu.vendor,
			VRAMBytes:    gpu.vramBytes,
			SharedMemory: gpu.sharedMemory,
		})
	}
	return csghub.LocalModelFilterHardware{
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		FreeDiskBytes: limits.maxDownloadBytes,
		RAMBytes:      limits.ramBytes,
		GPUs:          gpus,
	}
}

// marketplaceLocalModelFilterBoostOrgs maps the detected vendors to the hub
// namespaces that publish builds for them. The server only sorts by namespace,
// so the mapping belongs here.
func marketplaceLocalModelFilterBoostOrgs(limits marketplaceLocalInferenceLimits) []string {
	seen := make(map[string]struct{}, len(limits.gpus))
	orgs := make([]string, 0, len(limits.gpus))
	for _, gpu := range limits.gpus {
		org := ""
		switch gpu.vendor {
		case "nvidia":
			org = "nvidia"
		case "amd":
			org = "amd"
		}
		if org == "" {
			continue
		}
		if _, ok := seen[org]; ok {
			continue
		}
		seen[org] = struct{}{}
		orgs = append(orgs, org)
	}
	return orgs
}

// detectMarketplaceLocalInferenceGPUs enumerates every GPU. The existing
// getGPUInfo returns only the best card, which understates a multi-GPU machine.
func detectMarketplaceLocalInferenceGPUs(ramTotal uint64) []marketplaceLocalInferenceGPU {
	var gpus []marketplaceLocalInferenceGPU
	if detected, ok := detectNVIDIAGPUs(); ok {
		gpus = detected
	} else if detected, ok := detectROCmGPUs(ramTotal); ok {
		gpus = detected
	} else {
		switch runtime.GOOS {
		case "darwin":
			gpus, _ = detectDarwinGPUs(ramTotal)
		case "windows":
			gpus, _ = detectWindowsGPUs(ramTotal)
		}
	}
	return marketplaceLimitGPUs(gpus)
}

// marketplaceLimitGPUs keeps the largest discrete cards when a machine reports
// more GPUs than the request should carry. Shared-memory cards sort last.
func marketplaceLimitGPUs(gpus []marketplaceLocalInferenceGPU) []marketplaceLocalInferenceGPU {
	if len(gpus) <= marketplaceLocalInferenceMaxGPUs {
		return gpus
	}
	sorted := append([]marketplaceLocalInferenceGPU(nil), gpus...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].sharedMemory != sorted[j].sharedMemory {
			return !sorted[i].sharedMemory
		}
		return sorted[i].vramBytes > sorted[j].vramBytes
	})
	return sorted[:marketplaceLocalInferenceMaxGPUs]
}

// marketplaceGPUProbeTimeout bounds the external hardware probes. A hung
// vendor tool must not block the marketplace list.
const marketplaceGPUProbeTimeout = 5 * time.Second

func probeCommandOutput(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func detectNVIDIAGPUs() ([]marketplaceLocalInferenceGPU, bool) {
	binary, err := hardware.ResolveNVIDIASMI()
	if err != nil {
		return nil, false
	}
	out, err := probeCommandOutput(marketplaceGPUProbeTimeout, binary,
		"--query-gpu=name,memory.total",
		"--format=csv,noheader,nounits")
	if err != nil {
		return nil, false
	}
	gpus := parseNVIDIAGPUList(out)
	if len(gpus) == 0 {
		return nil, false
	}
	return gpus, true
}

func parseNVIDIAGPUList(out []byte) []marketplaceLocalInferenceGPU {
	var gpus []marketplaceLocalInferenceGPU
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}
		shared := isNVIDIAUnifiedMemoryGPU(name)
		var vram int64
		if !shared {
			if mib, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64); err == nil {
				vram = int64(mib) * 1024 * 1024
			}
		}
		gpus = append(gpus, marketplaceLocalInferenceGPU{
			vendor:       "nvidia",
			vramBytes:    vram,
			sharedMemory: shared,
		})
	}
	return gpus
}

func detectROCmGPUs(ramTotal uint64) ([]marketplaceLocalInferenceGPU, bool) {
	if runtime.GOOS != "linux" {
		return nil, false
	}
	if _, err := os.Stat("/dev/kfd"); err != nil {
		return nil, false
	}
	if binary, err := exec.LookPath("rocm-smi"); err == nil {
		if out, err := probeCommandOutput(marketplaceGPUProbeTimeout, binary, "--showproductname", "--showmeminfo", "vram", "--json"); err == nil {
			if gpus := parseROCmSMIList(out); len(gpus) > 0 {
				return gpus, true
			}
		}
	}
	info, ok := getKFDGPUInfo(ramTotal)
	if !ok {
		return nil, false
	}
	gpu := marketplaceLocalInferenceGPU{vendor: "amd", sharedMemory: info.SharedMemory}
	if !info.SharedMemory {
		gpu.vramBytes = int64(info.VRAMTotal)
	}
	return []marketplaceLocalInferenceGPU{gpu}, true
}

func parseROCmSMIList(out []byte) []marketplaceLocalInferenceGPU {
	var cards map[string]map[string]string
	if err := json.Unmarshal(out, &cards); err != nil {
		return nil
	}
	gpus := make([]marketplaceLocalInferenceGPU, 0, len(cards))
	for _, card := range cards {
		name := strings.TrimSpace(card["Card model"])
		totalStr := strings.TrimSpace(card["VRAM Total Memory (B)"])
		if name == "" || totalStr == "" {
			continue
		}
		total, _ := strconv.ParseUint(totalStr, 10, 64)
		gpus = append(gpus, marketplaceLocalInferenceGPU{
			vendor:    "amd",
			vramBytes: int64(total),
		})
	}
	return gpus
}

func detectDarwinGPUs(ramTotal uint64) ([]marketplaceLocalInferenceGPU, bool) {
	out, err := probeCommandOutput(marketplaceGPUProbeTimeout, "system_profiler", "-json", "SPDisplaysDataType")
	if err != nil {
		return nil, false
	}
	var payload struct {
		Displays []map[string]interface{} `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, false
	}
	gpus := make([]marketplaceLocalInferenceGPU, 0, len(payload.Displays))
	for _, display := range payload.Displays {
		name := stringField(display, "sppci_model", "_name")
		vendor := strings.ToLower(stringField(display, "spdisplays_vendor"))
		total := parseMemoryBytes(
			stringField(display, "spdisplays_vram"),
			stringField(display, "spdisplays_vram_shared"),
			stringField(display, "spdisplays_vram_dynamic"),
		)
		if name == "" && total == 0 {
			continue
		}
		shared := strings.Contains(vendor, "apple") || strings.Contains(strings.ToLower(name), "apple")
		gpu := marketplaceLocalInferenceGPU{vendor: "apple", sharedMemory: shared}
		if !shared {
			gpu.vramBytes = int64(total)
		}
		gpus = append(gpus, gpu)
	}
	if len(gpus) == 0 {
		return nil, false
	}
	return gpus, true
}

func detectWindowsGPUs(ramTotal uint64) ([]marketplaceLocalInferenceGPU, bool) {
	out, err := runPowerShellCommand(windowsGPUQuery)
	if err != nil {
		return nil, false
	}
	var payload windowsGPUPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, false
	}
	gpus := make([]marketplaceLocalInferenceGPU, 0, len(payload.Controllers))
	for _, controller := range payload.Controllers {
		name := strings.TrimSpace(controller.Name)
		if name == "" || isWindowsVirtualDisplayAdapter(name) {
			continue
		}
		total := windowsControllerVRAM(controller, payload.Registry)
		gpu := marketplaceLocalInferenceGPU{vendor: windowsGPUVendor(name)}
		if total < windowsSharedVRAMThreshold && ramTotal > 0 {
			gpu.sharedMemory = true
		} else {
			gpu.vramBytes = int64(total)
		}
		gpus = append(gpus, gpu)
	}
	if len(gpus) == 0 {
		return nil, false
	}
	return gpus, true
}

func windowsGPUVendor(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "nvidia"), strings.Contains(name, "geforce"), strings.Contains(name, "quadro"):
		return "nvidia"
	case strings.Contains(name, "amd"), strings.Contains(name, "radeon"), strings.Contains(name, "ati"):
		return "amd"
	case strings.Contains(name, "intel"):
		return "intel"
	default:
		return ""
	}
}
