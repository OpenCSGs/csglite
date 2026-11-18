package hardware

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCPULimits(t *testing.T) {
	if got := parseCPUMax("400000 100000"); got != 4 {
		t.Fatalf("parseCPUMax = %d, want 4", got)
	}
	if got := parseCPUMax("max 100000"); got != 0 {
		t.Fatalf("unlimited parseCPUMax = %d, want 0", got)
	}
	if got := parseCPUMax("garbage"); got != 0 {
		t.Fatalf("invalid parseCPUMax = %d, want 0", got)
	}
	if got := parseCPUQuota("400000", "100000"); got != 4 {
		t.Fatalf("parseCPUQuota = %d, want 4", got)
	}
	if got := parseCPUQuota("-1", "100000"); got != 0 {
		t.Fatalf("unlimited parseCPUQuota = %d, want 0", got)
	}
	if got := parseCPUQuota("150000", "100000"); got != 2 {
		t.Fatalf("partial parseCPUQuota = %d, want 2 (rounded up)", got)
	}
}

func TestParseMemoryLimit(t *testing.T) {
	if got := parseMemoryLimit("8589934592"); got != 8<<30 {
		t.Fatalf("parseMemoryLimit = %d, want 8GiB", got)
	}
	if got := parseMemoryLimit("max"); got != 0 {
		t.Fatalf("unlimited parseMemoryLimit = %d, want 0", got)
	}
	if got := parseMemoryLimit("9223372036854771712"); got != 0 {
		t.Fatalf("v1 unlimited parseMemoryLimit = %d, want 0", got)
	}
}

func TestReadCgroupV2AndV1(t *testing.T) {
	v2 := t.TempDir()
	writeFile(t, filepath.Join(v2, "cpu.max"), "200000 100000\n")
	writeFile(t, filepath.Join(v2, "memory.max"), "4294967296\n")
	limits, ok := readCgroupV2(v2)
	if !ok || limits.CPUCores != 2 || limits.MemoryBytes != 4<<30 || limits.Source != "cgroup-v2" {
		t.Fatalf("v2 limits = %+v ok=%t", limits, ok)
	}

	v1 := t.TempDir()
	writeFile(t, filepath.Join(v1, "cpu", "cpu.cfs_quota_us"), "400000\n")
	writeFile(t, filepath.Join(v1, "cpu", "cpu.cfs_period_us"), "100000\n")
	writeFile(t, filepath.Join(v1, "memory", "memory.limit_in_bytes"), "8589934592\n")
	limits, ok = readCgroupV1(v1)
	if !ok || limits.CPUCores != 4 || limits.MemoryBytes != 8<<30 || limits.Source != "cgroup-v1" {
		t.Fatalf("v1 limits = %+v ok=%t", limits, ok)
	}
}

func TestRecommendedMaxJobsHonoursCPUAndMemory(t *testing.T) {
	cases := []struct {
		cores  int
		memory uint64
		want   int
	}{
		{4, 8 << 30, 2},  // the QA container
		{16, 3 << 30, 1}, // many cores, too little RAM for several compilers
		{16, 6 << 30, 2},
		{16, 12 << 30, 3},
		{16, 32 << 30, 4},
		{1, 0, 1},
	}
	for _, tc := range cases {
		if got := recommendedMaxJobsFor(tc.cores, tc.memory); got != tc.want {
			t.Fatalf("recommendedMaxJobsFor(%d, %d) = %d, want %d", tc.cores, tc.memory, got, tc.want)
		}
	}
}

func TestEffectiveCPUCores(t *testing.T) {
	if got := effectiveCPUCores(32, Limits{CPUCores: 4}); got != 4 {
		t.Fatalf("effectiveCPUCores = %d, want 4", got)
	}
	if got := effectiveCPUCores(2, Limits{CPUCores: 4}); got != 2 {
		t.Fatalf("effectiveCPUCores = %d, want host 2", got)
	}
	if got := effectiveCPUCores(8, Limits{}); got != 8 {
		t.Fatalf("effectiveCPUCores = %d, want 8", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
