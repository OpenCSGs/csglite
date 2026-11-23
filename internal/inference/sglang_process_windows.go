//go:build windows

package inference

import (
	"os/exec"
	"strconv"
	"syscall"
)

// configureSGLangProcess puts SGLang in its own process group. Native managed
// SGLang is not available on Windows, but the helpers still compile and behave
// conservatively if a process is started in tests.
func configureSGLangProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// signalSGLangProcess terminates the process tree. Windows has no portable
// graceful signal for a console child, so the caller's grace period elapses and
// the forced taskkill path removes the children too.
func signalSGLangProcess(cmd *exec.Cmd, force bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if !force {
		return nil
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// sglangGroupAlive is conservative on Windows: the taskkill path is the only
// way to inspect the tree, so a started process is treated as potentially
// alive until Close has run it.
func sglangGroupAlive(cmd *exec.Cmd) bool {
	return cmd != nil && cmd.Process != nil
}
