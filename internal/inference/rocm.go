package inference

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/opencsgs/csglite/internal/hardware"
)

const ROCMSingleEngineEnv = "CSGHUB_LITE_ROCM_SINGLE_ENGINE"

const ROCMUnifiedMemoryEnv = "CSGHUB_LITE_ROCM_UNIFIED_MEMORY"

const kfdTopologyNodesDir = "/sys/class/kfd/kfd/topology/nodes"

func IsROCMHost() bool {
	return isROCMHost(runtime.GOOS, os.Stat, hardware.ResolveNVIDIASMI)
}

func ROCMSingleEngineMode() bool {
	return rocmSingleEngineMode(os.Getenv, IsROCMHost)
}

func rocmSingleEngineMode(getenv func(string) string, isROCMHost func() bool) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(ROCMSingleEngineEnv))) {
	case "0", "false", "no", "off":
		return false
	}
	return isROCMHost()
}

func isROCMHost(goos string, stat func(string) (os.FileInfo, error), resolveNVIDIA func() (string, error)) bool {
	if goos != "linux" {
		return false
	}
	if _, err := stat("/dev/kfd"); err != nil {
		return false
	}
	if _, err := resolveNVIDIA(); err == nil {
		return false
	} else if err != exec.ErrNotFound {
		return false
	}
	return true
}

// ROCMUnifiedMemoryMode reports whether llama-server should run with
// GGML_CUDA_ENABLE_UNIFIED_MEMORY=1. On AMD APUs the ROCm backend misreports
// free device memory (llama.cpp reads /proc/meminfo MemAvailable), so its
// fit feature keeps full GPU offload while the real hipMalloc is capped by
// the small VRAM carve-out + GTT and fails. hipMallocManaged (unified memory)
// allocates from system RAM instead, which is what upstream recommends for
// APUs (see ggml-org/llama.cpp#18159). Discrete ROCm GPUs are left alone
// because unified memory hurts their performance.
func ROCMUnifiedMemoryMode() bool {
	return rocmUnifiedMemoryMode(os.Getenv, IsROCMHost, isKFDAPUOnlyHost)
}

func rocmUnifiedMemoryMode(getenv func(string) string, isROCMHost func() bool, isAPUOnly func() bool) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(ROCMUnifiedMemoryEnv))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return isROCMHost() && isAPUOnly()
}

// isKFDAPUOnlyHost reports whether every GPU node in the KFD topology is an
// integrated GPU. The kernel exposes local_mem_size 0 for APUs (system memory
// is shared), while discrete GPUs report their VRAM size.
func isKFDAPUOnlyHost() bool {
	nodes, err := os.ReadDir(kfdTopologyNodesDir)
	if err != nil {
		return false
	}
	gpuNodes := 0
	for _, node := range nodes {
		props, err := os.Open(filepath.Join(kfdTopologyNodesDir, node.Name(), "properties"))
		if err != nil {
			continue
		}
		simdCount, localMemSize, _ := parseKFDNodeProperties(props)
		props.Close()
		if simdCount <= 0 {
			continue // CPU-only node
		}
		gpuNodes++
		if localMemSize > 0 {
			return false // discrete GPU with dedicated VRAM
		}
	}
	return gpuNodes > 0
}

func parseKFDNodeProperties(r interface{ Read([]byte) (int, error) }) (simdCount, localMemSize, gfxTargetVersion int64) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "simd_count":
			simdCount = value
		case "local_mem_size":
			localMemSize = value
		case "gfx_target_version":
			gfxTargetVersion = value
		}
	}
	return simdCount, localMemSize, gfxTargetVersion
}

// gfxTargetVersionToArch converts a KFD gfx_target_version (e.g. 110003)
// to the canonical gfx ISA string (e.g. "gfx1100"). The packed decimal
// format is major*10000 + minor*100 + stepping, where minor and stepping
// are hex nibbles (e.g. 110501 -> gfx1151). Returns "" for zero/invalid input.
func gfxTargetVersionToArch(version int64) string {
	if version <= 0 {
		return ""
	}
	major := version / 10000
	minor := (version / 100) % 100
	stepping := version % 100
	if major == 0 {
		return ""
	}
	return fmt.Sprintf("gfx%d%x%x", major, minor, stepping)
}

// detectROCMGfxArch reads KFD topology to find the gfx_target_version of the
// first GPU node with dedicated VRAM. Falls back to any GPU node (APU).
// Returns 0 if no GPU is found or KFD is unavailable.
func detectROCMGfxTargetVersion() int64 {
	nodes, err := os.ReadDir(kfdTopologyNodesDir)
	if err != nil {
		return 0
	}
	var apuVersion int64
	for _, node := range nodes {
		props, err := os.Open(filepath.Join(kfdTopologyNodesDir, node.Name(), "properties"))
		if err != nil {
			continue
		}
		simdCount, localMemSize, gfxTargetVersion := parseKFDNodeProperties(props)
		props.Close()
		if simdCount <= 0 {
			continue
		}
		if localMemSize > 0 {
			return gfxTargetVersion
		}
		if apuVersion == 0 {
			apuVersion = gfxTargetVersion
		}
	}
	return apuVersion
}

// ROCMGfxArch returns the detected AMD GPU architecture (e.g. "gfx1151")
// on ROCm hosts, or "" if detection fails or the host is not ROCm.
func ROCMGfxArch() string {
	if !IsROCMHost() {
		return ""
	}
	return gfxTargetVersionToArch(detectROCMGfxTargetVersion())
}

// ROCMFreeVRAM returns the free VRAM in bytes on ROCm hosts. On discrete GPUs
// this is free VRAM; on APUs it is system RAM available (via /proc/meminfo).
// Returns 0 if the host is not ROCm or the value cannot be determined.
func ROCMFreeVRAM() uint64 {
	if !IsROCMHost() {
		return 0
	}
	if v := rocmSMIFreeVRAM(); v > 0 {
		return v
	}
	if isKFDAPUOnlyHost() {
		return readProcMemAvailable()
	}
	return drmFreeVRAM()
}

func rocmSMIFreeVRAM() uint64 {
	binary, err := exec.LookPath("rocm-smi")
	if err != nil {
		return 0
	}
	out, err := exec.Command(binary, "--showmeminfo", "vram", "--json").Output()
	if err != nil {
		return 0
	}
	var cards map[string]map[string]string
	if err := jsonUnmarshal(out, &cards); err != nil {
		return 0
	}
	var bestFree uint64
	for _, card := range cards {
		totalStr := strings.TrimSpace(card["VRAM Total Memory (B)"])
		usedStr := strings.TrimSpace(card["VRAM Total Used Memory (B)"])
		if totalStr == "" {
			continue
		}
		total, _ := strconv.ParseUint(totalStr, 10, 64)
		used, _ := strconv.ParseUint(usedStr, 10, 64)
		if total > used && total-used > bestFree {
			bestFree = total - used
		}
	}
	return bestFree
}

func drmFreeVRAM() uint64 {
	const drmDir = "/sys/class/drm"
	entries, err := os.ReadDir(drmDir)
	if err != nil {
		return 0
	}
	var bestFree uint64
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "card") || strings.Contains(entry.Name(), "-") {
			continue
		}
		totalData, err := os.ReadFile(filepath.Join(drmDir, entry.Name(), "device", "mem_info_vram_total"))
		if err != nil {
			continue
		}
		usedData, err := os.ReadFile(filepath.Join(drmDir, entry.Name(), "device", "mem_info_vram_used"))
		if err != nil {
			continue
		}
		total, _ := strconv.ParseUint(strings.TrimSpace(string(totalData)), 10, 64)
		used, _ := strconv.ParseUint(strings.TrimSpace(string(usedData)), 10, 64)
		if total > used && total-used > bestFree {
			bestFree = total - used
		}
	}
	return bestFree
}

func readProcMemAvailable() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				val, _ := strconv.ParseUint(fields[1], 10, 64)
				return val * 1024
			}
		}
	}
	return 0
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
