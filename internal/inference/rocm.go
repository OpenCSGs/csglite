package inference

import (
	"bufio"
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
		simdCount, localMemSize := parseKFDNodeProperties(props)
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

func parseKFDNodeProperties(r interface{ Read([]byte) (int, error) }) (simdCount, localMemSize int64) {
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
		}
	}
	return simdCount, localMemSize
}
