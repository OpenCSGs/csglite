package inference

import (
	"os"
	"os/exec"
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
