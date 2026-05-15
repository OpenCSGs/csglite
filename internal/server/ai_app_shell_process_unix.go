//go:build !windows

package server

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func prepareAIAppShellCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateAIAppShellProcess(process *os.Process) {
	if process == nil || process.Pid <= 0 {
		return
	}
	pgid := -process.Pid
	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
		_ = process.Kill()
		return
	}
	time.Sleep(300 * time.Millisecond)
	if processGroupExists(process.Pid) {
		_ = syscall.Kill(pgid, syscall.SIGKILL)
	}
}

func processGroupExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || err == syscall.EPERM
}
