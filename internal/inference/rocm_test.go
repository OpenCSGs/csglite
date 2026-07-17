package inference

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestIsROCMHostRequiresLinuxKFDAndNoNVIDIA(t *testing.T) {
	statKFD := func(path string) (os.FileInfo, error) {
		if path == "/dev/kfd" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	noNVIDIA := func() (string, error) { return "", exec.ErrNotFound }

	if !isROCMHost("linux", statKFD, noNVIDIA) {
		t.Fatal("expected ROCm host")
	}
	if isROCMHost("darwin", statKFD, noNVIDIA) {
		t.Fatal("darwin should not be ROCm host")
	}
	if isROCMHost("linux", func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, noNVIDIA) {
		t.Fatal("missing /dev/kfd should not be ROCm host")
	}
	if isROCMHost("linux", statKFD, func() (string, error) { return "/usr/bin/nvidia-smi", nil }) {
		t.Fatal("NVIDIA host should not use ROCm single-engine mode")
	}
}

func TestROCMUnifiedMemoryModeFollowsAPUDetection(t *testing.T) {
	noEnv := func(string) string { return "" }
	rocmHost := func() bool { return true }

	if !rocmUnifiedMemoryMode(noEnv, rocmHost, func() bool { return true }) {
		t.Fatal("APU-only ROCm host should enable unified memory by default")
	}
	if rocmUnifiedMemoryMode(noEnv, rocmHost, func() bool { return false }) {
		t.Fatal("discrete-GPU ROCm host should not enable unified memory")
	}
	if rocmUnifiedMemoryMode(noEnv, func() bool { return false }, func() bool { return true }) {
		t.Fatal("non-ROCm host should not enable unified memory")
	}
	if rocmUnifiedMemoryMode(func(string) string { return "0" }, rocmHost, func() bool { return true }) {
		t.Fatal("env override should disable unified memory")
	}
	if !rocmUnifiedMemoryMode(func(string) string { return "1" }, func() bool { return false }, func() bool { return false }) {
		t.Fatal("env override should force-enable unified memory")
	}
}

func TestParseKFDNodeProperties(t *testing.T) {
	apu := strings.NewReader("cpu_cores_count 0\nsimd_count 24\nlocal_mem_size 0\nfw_version 1\n")
	simd, localMem := parseKFDNodeProperties(apu)
	if simd != 24 || localMem != 0 {
		t.Fatalf("apu node: simd=%d localMem=%d, want 24, 0", simd, localMem)
	}

	dgpu := strings.NewReader("simd_count 304\nlocal_mem_size 25753026560\n")
	simd, localMem = parseKFDNodeProperties(dgpu)
	if simd != 304 || localMem != 25753026560 {
		t.Fatalf("dgpu node: simd=%d localMem=%d, want 304, 25753026560", simd, localMem)
	}

	cpu := strings.NewReader("cpu_cores_count 16\nsimd_count 0\nlocal_mem_size 0\n")
	simd, _ = parseKFDNodeProperties(cpu)
	if simd != 0 {
		t.Fatalf("cpu node: simd=%d, want 0", simd)
	}
}

func TestROCMSingleEngineModeCanBeDisabled(t *testing.T) {
	isHost := func() bool { return true }
	if !rocmSingleEngineMode(func(string) string { return "" }, isHost) {
		t.Fatal("expected ROCm single-engine mode by default")
	}
	if rocmSingleEngineMode(func(string) string { return "0" }, isHost) {
		t.Fatal("expected env override to disable ROCm single-engine mode")
	}
	if rocmSingleEngineMode(func(string) string { return "" }, func() bool { return false }) {
		t.Fatal("non-ROCm host should not use ROCm single-engine mode")
	}
}
