package hardware

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Limits is the CPU and memory budget this process actually has. Inside a
// container the cgroup limit is usually far below the host's physical size,
// and compilation must be sized from it rather than from runtime.NumCPU().
type Limits struct {
	CPUCores    int
	MemoryBytes uint64
	// Source is "cgroup-v2", "cgroup-v1" or "host".
	Source string
}

const cgroupRoot = "/sys/fs/cgroup"

// ContainerLimits reports the effective CPU and memory limits. It is
// conservative: an unreadable cgroup file falls back to the host values.
func ContainerLimits() Limits {
	if runtime.GOOS != "linux" {
		return Limits{CPUCores: runtime.NumCPU(), Source: "host"}
	}
	if limits, ok := readCgroupV2(cgroupRoot); ok {
		return limits
	}
	if limits, ok := readCgroupV1(cgroupRoot); ok {
		return limits
	}
	return Limits{CPUCores: runtime.NumCPU(), Source: "host"}
}

func readCgroupV2(root string) (Limits, bool) {
	cpuMax, err := os.ReadFile(filepath.Join(root, "cpu.max"))
	if err != nil {
		return Limits{}, false
	}
	limits := Limits{Source: "cgroup-v2", CPUCores: parseCPUMax(string(cpuMax))}
	if data, err := os.ReadFile(filepath.Join(root, "memory.max")); err == nil {
		limits.MemoryBytes = parseMemoryLimit(string(data))
	}
	if limits.CPUCores == 0 {
		limits.CPUCores = runtime.NumCPU()
	}
	return limits, true
}

func readCgroupV1(root string) (Limits, bool) {
	quota, err := os.ReadFile(filepath.Join(root, "cpu", "cpu.cfs_quota_us"))
	if err != nil {
		return Limits{}, false
	}
	period, _ := os.ReadFile(filepath.Join(root, "cpu", "cpu.cfs_period_us"))
	limits := Limits{Source: "cgroup-v1", CPUCores: parseCPUQuota(string(quota), string(period))}
	if data, err := os.ReadFile(filepath.Join(root, "memory", "memory.limit_in_bytes")); err == nil {
		limits.MemoryBytes = parseMemoryLimit(string(data))
	}
	if limits.CPUCores == 0 {
		limits.CPUCores = runtime.NumCPU()
	}
	return limits, true
}

func parseCPUMax(value string) int {
	fields := strings.Fields(value)
	if len(fields) != 2 || fields[0] == "max" {
		return 0
	}
	quota, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || quota <= 0 {
		return 0
	}
	period, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || period <= 0 {
		return 0
	}
	return int((quota + period - 1) / period)
}

func parseCPUQuota(quotaValue, periodValue string) int {
	quota, err := strconv.ParseInt(strings.TrimSpace(quotaValue), 10, 64)
	if err != nil || quota <= 0 {
		return 0
	}
	period, err := strconv.ParseInt(strings.TrimSpace(periodValue), 10, 64)
	if err != nil || period <= 0 {
		period = 100000
	}
	return int((quota + period - 1) / period)
}

func parseMemoryLimit(value string) uint64 {
	value = strings.TrimSpace(value)
	if value == "" || value == "max" {
		return 0
	}
	limit, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	// cgroup v1 reports an effectively unlimited value as a huge page-aligned
	// number. Treat anything above the address-space sanity bound as unknown.
	if limit >= 1<<60 {
		return 0
	}
	return limit
}

// EffectiveCPUCores returns the smaller of the host CPU count and the cgroup
// quota. It never returns zero.
func EffectiveCPUCores() int {
	return effectiveCPUCores(runtime.NumCPU(), ContainerLimits())
}

func effectiveCPUCores(host int, limits Limits) int {
	if limits.CPUCores > 0 && limits.CPUCores < host {
		return limits.CPUCores
	}
	return host
}

// RecommendedMaxJobs is the conservative compilation parallelism for a
// container-sized CPU and memory budget. Half the effective cores (at most
// four) leaves headroom for the model process; low memory reduces the job
// count further because each compiler/ninja worker needs hundreds of MiB. A
// 4-CPU/8Gi container therefore compiles with two jobs, and a 16-CPU/3Gi
// container with one.
func RecommendedMaxJobs() int {
	return recommendedMaxJobsFor(EffectiveCPUCores(), ContainerLimits().MemoryBytes)
}

func recommendedMaxJobsFor(cores int, memoryBytes uint64) int {
	jobs := cores / 2
	if jobs < 1 {
		jobs = 1
	}
	if jobs > 4 {
		jobs = 4
	}
	switch {
	case memoryBytes > 0 && memoryBytes < 4<<30:
		if jobs > 1 {
			jobs = 1
		}
	case memoryBytes > 0 && memoryBytes < 8<<30:
		if jobs > 2 {
			jobs = 2
		}
	case memoryBytes > 0 && memoryBytes < 16<<30:
		if jobs > 3 {
			jobs = 3
		}
	}
	return jobs
}
