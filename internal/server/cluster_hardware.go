package server

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/hardware"
)

// clusterGPUStatuses lists every accelerator with the live figures the
// cluster scheduler weighs. NVIDIA reports per-GPU utilization, temperature
// and power; other vendors fall back to the single-GPU summary /api/system
// already computes.
func clusterGPUStatuses(systemMemoryTotal uint64) []cluster.GPUStatus {
	if gpus, ok := nvidiaGPUStatuses(); ok {
		return gpus
	}
	info := getGPUInfo(systemMemoryTotal)
	if info.Name == "" {
		return []cluster.GPUStatus{}
	}
	g := cluster.GPUStatus{Index: 0, Name: info.Name, VRAMTotal: info.VRAMTotal, VRAMUsed: info.VRAMUsed, UsageKnown: info.UsageAvailable, Shared: info.SharedMemory}
	if g.Shared && g.VRAMTotal == 0 {
		g.VRAMTotal = systemMemoryTotal
	}
	return []cluster.GPUStatus{g}
}

func nvidiaGPUStatuses() ([]cluster.GPUStatus, bool) {
	binary, err := hardware.ResolveNVIDIASMI()
	if err != nil {
		return nil, false
	}
	out, err := exec.Command(binary,
		"--query-gpu=index,name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw,power.limit,clocks_throttle_reasons.active",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, false
	}
	return parseNVIDIAGPUStatuses(out)
}

// parseNVIDIAGPUStatuses parses the CSV from nvidia-smi. Fields a driver does
// not support arrive as "[N/A]" or "[Not Supported]" and are left nil.
func parseNVIDIAGPUStatuses(out []byte) ([]cluster.GPUStatus, bool) {
	var gpus []cluster.GPUStatus
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		idx, _ := strconv.Atoi(fields[0])
		g := cluster.GPUStatus{Index: idx, Name: fields[1]}
		if g.Name == "" {
			continue
		}
		usedMiB, errUsed := strconv.ParseUint(fields[2], 10, 64)
		totalMiB, errTotal := strconv.ParseUint(fields[3], 10, 64)
		if errUsed == nil && errTotal == nil {
			g.VRAMUsed = usedMiB << 20
			g.VRAMTotal = totalMiB << 20
			g.UsageKnown = true
		} else {
			g.Shared = isNVIDIAUnifiedMemoryGPU(g.Name)
		}
		if len(fields) > 4 {
			g.Util = parseIntField(fields[4])
		}
		if len(fields) > 5 {
			g.Temperature = parseIntField(fields[5])
		}
		if len(fields) > 6 {
			g.PowerDraw = parseIntField(fields[6])
		}
		if len(fields) > 7 {
			g.PowerLimit = parseIntField(fields[7])
		}
		if len(fields) > 8 {
			// clocks_throttle_reasons.active is a bitmask; 0x0 means none,
			// 0x1 is "GPU idle" which is not a problem. Anything else is a
			// real throttle (power cap, thermal, sync boost, ...).
			raw := strings.ToLower(strings.TrimPrefix(fields[8], "0x"))
			if v, err := strconv.ParseUint(raw, 16, 64); err == nil && v > 1 {
				g.Throttled = true
			}
		}
		gpus = append(gpus, g)
	}
	return gpus, len(gpus) > 0
}

func parseIntField(v string) *int {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "[") {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	n := int(f + 0.5)
	return &n
}

// cpuLoad1 reads the one-minute load average where the platform has one.
func cpuLoad1() (float64, bool) {
	switch runtime.GOOS {
	case "linux":
		out, err := exec.Command("cat", "/proc/loadavg").Output()
		if err != nil {
			return 0, false
		}
		fields := strings.Fields(string(out))
		if len(fields) == 0 {
			return 0, false
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		return v, err == nil
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return 0, false
		}
		fields := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
		if len(fields) == 0 {
			return 0, false
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		return v, err == nil
	}
	return 0, false
}

// cpuUtilQuick returns a CPU utilisation percentage when it can be had
// without a one-second sample (Linux /proc/stat delta is kept by the caller's
// cadence; here we accept the cheap load-based estimate elsewhere).
func cpuUtilQuick() (float64, bool) {
	if runtime.GOOS != "linux" {
		return 0, false
	}
	usage, _ := getCPUInfo()
	if usage <= 0 {
		return 0, false
	}
	return usage, true
}
