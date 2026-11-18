//go:build !windows

package inference

import (
	"errors"
	"os/exec"
	"syscall"
)

// configureSGLangProcess starts SGLang in its own process group so stopping it
// also stops the worker processes it spawns. The group never includes Lite.
func configureSGLangProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalSGLangProcess signals the whole group. A missing group means the
// process already exited and is not an error.
func signalSGLangProcess(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	if err := syscall.Kill(-cmd.Process.Pid, signal); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	// Fall back to the direct process when group signaling is unavailable.
	return cmd.Process.Signal(signal)
}

// sglangGroupAlive reports whether the owned process group still has members.
// It never signals another process: the group ID is the child PID Lite set.
func sglangGroupAlive(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	err := syscall.Kill(-cmd.Process.Pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
