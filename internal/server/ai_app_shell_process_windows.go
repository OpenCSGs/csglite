//go:build windows

package server

import (
	"os"
	"os/exec"
	"strconv"
)

func prepareAIAppShellCommand(cmd *exec.Cmd) {
}

func terminateAIAppShellProcess(process *os.Process) {
	if process == nil || process.Pid <= 0 {
		return
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(process.Pid)).Run(); err != nil {
		_ = process.Kill()
	}
}
