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
	apu := strings.NewReader("cpu_cores_count 0\nsimd_count 24\nlocal_mem_size 0\ngfx_target_version 115003\nfw_version 1\n")
	simd, localMem, gfxVer := parseKFDNodeProperties(apu)
	if simd != 24 || localMem != 0 || gfxVer != 115003 {
		t.Fatalf("apu node: simd=%d localMem=%d gfx=%d, want 24, 0, 115003", simd, localMem, gfxVer)
	}

	dgpu := strings.NewReader("simd_count 304\nlocal_mem_size 25753026560\ngfx_target_version 110003\n")
	simd, localMem, gfxVer = parseKFDNodeProperties(dgpu)
	if simd != 304 || localMem != 25753026560 || gfxVer != 110003 {
		t.Fatalf("dgpu node: simd=%d localMem=%d gfx=%d, want 304, 25753026560, 110003", simd, localMem, gfxVer)
	}

	cpu := strings.NewReader("cpu_cores_count 16\nsimd_count 0\nlocal_mem_size 0\n")
	simd, _, _ = parseKFDNodeProperties(cpu)
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

func TestGfxTargetVersionToArch(t *testing.T) {
	cases := []struct {
		input int64
		want  string
	}{
		{100300, "gfx1030"},
		{110501, "gfx1151"},
		{110003, "gfx1103"},
		{90402, "gfx942"},
		{90010, "gfx90a"},
		{0, ""},
		{-1, ""},
	}
	for _, tc := range cases {
		got := gfxTargetVersionToArch(tc.input)
		if got != tc.want {
			t.Errorf("gfxTargetVersionToArch(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestGfxTargetVersionToHSAOverride(t *testing.T) {
	cases := []struct {
		input int64
		want  string
	}{
		{100300, "10.3.0"},
		{110501, "11.5.1"},
		{110003, "11.0.3"},
		{90402, "9.4.2"},
		{90010, "9.0.10"},
		{0, ""},
		{-1, ""},
	}
	for _, tc := range cases {
		got := gfxTargetVersionToHSAOverride(tc.input)
		if got != tc.want {
			t.Errorf("gfxTargetVersionToHSAOverride(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
