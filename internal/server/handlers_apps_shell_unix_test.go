//go:build !windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAIAppShellCloseAllTerminatesProcessGroup(t *testing.T) {
	manager, _, childPID := startTestAIAppShellWithChild(t, "codex")
	if !processExists(childPID) {
		t.Fatalf("child process %d is not running before CloseAll", childPID)
	}

	manager.CloseAll()
	manager.mu.RLock()
	remaining := len(manager.sessions)
	manager.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("sessions after CloseAll = %d, want 0", remaining)
	}
	if processStillExists(childPID, 3*time.Second) {
		t.Fatalf("child process %d still exists after CloseAll", childPID)
	}
}

func TestAIAppShellDetachedSessionTerminatesProcessGroup(t *testing.T) {
	oldDetachedTimeout := aiAppShellDetachedTimeout
	aiAppShellDetachedTimeout = time.Second
	t.Cleanup(func() {
		aiAppShellDetachedTimeout = oldDetachedTimeout
	})

	_, session, childPID := startTestAIAppShellWithChild(t, "open-code")
	attach := session.Attach()
	session.Detach(attach.events)

	if processStillExists(childPID, 3*time.Second) {
		t.Fatalf("child process %d still exists after detached timeout", childPID)
	}
}

func startTestAIAppShellWithChild(t *testing.T, appID string) (*aiAppShellManager, *aiAppShellSession, int) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is unavailable")
	}
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent.pid")
	childPath := filepath.Join(dir, "child.pid")
	script := fmt.Sprintf("echo $$ > %q; sleep 60 & echo $! > %q; wait", parentPath, childPath)

	manager := newAIAppShellManager()
	session, err := manager.Create(appID, "Test Shell", "test/model", aiAppPreparedLaunch{
		Binary: sh,
		Args:   []string{"-c", script},
		Env:    os.Environ(),
		Dir:    dir,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if session == nil || session.cmd == nil || session.cmd.Process == nil {
		t.Fatal("session process is unavailable")
	}
	t.Cleanup(manager.CloseAll)

	childPID := waitForPIDFile(t, childPath)
	return manager, session, childPID
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid file %s was not written", path)
	return 0
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func processStillExists(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return processExists(pid)
}
