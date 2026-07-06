package inference

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/opencsgs/csglite/internal/hardware"
)

const ROCMSingleEngineEnv = "CSGHUB_LITE_ROCM_SINGLE_ENGINE"

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
