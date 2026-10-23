package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	plistLabel = "com.opencsg.csghub-lite"
	plistName  = plistLabel + ".plist"
)

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistName), nil
}

func executablePath() (string, error) {
	return os.Executable()
}

func plistContent(binary string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <false/>
</dict>
</plist>
`, plistLabel, binary)
}

// IsEnabled reports whether the LaunchAgent plist exists.
func IsEnabled() (bool, error) {
	path, err := plistPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Enable creates a LaunchAgent plist so csghub-lite starts on login.
func Enable() error {
	binary, err := executablePath()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}
	// Resolve symlinks so the plist points to the real binary.
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return fmt.Errorf("resolving executable symlink: %w", err)
	}

	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(plistContent(binary)), 0o644); err != nil {
		return err
	}
	removeQuarantine(path)
	return loadLaunchAgent(path)
}

// Disable removes the LaunchAgent plist.
func Disable() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	unloadLaunchAgent(path, plistLabel)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PlistContainsBinary checks whether the current plist references the
// running binary — useful for tests. Exported only for testing.
func PlistContainsBinary() (bool, error) {
	path, err := plistPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	binary, err := executablePath()
	if err != nil {
		return false, err
	}
	binary, _ = filepath.EvalSymlinks(binary)
	return strings.Contains(string(data), binary), nil
}

// removeQuarantine strips the com.apple.quarantine extended attribute from
// the plist. macOS 27's launchd refuses to load quarantined plists, and a
// plist created by a downloaded binary can inherit the attribute.
func removeQuarantine(path string) {
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()
}

// loadLaunchAgent registers the LaunchAgent with launchd. It prefers the
// classic `launchctl load -w` (the -w flag persists across logins) because
// `launchctl bootstrap` returns spurious EIO errors on macOS 26+. If the
// agent is already loaded, it is unloaded first and then reloaded.
func loadLaunchAgent(path string) error {
	if err := exec.Command("launchctl", "load", "-w", path).Run(); err == nil {
		return nil
	}
	// Already loaded — unload then reload.
	_ = exec.Command("launchctl", "unload", path).Run()
	if err := exec.Command("launchctl", "load", "-w", path).Run(); err == nil {
		return nil
	}
	// Last-resort fallback to the modern API.
	uid := os.Getuid()
	return exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), path).Run()
}

// unloadLaunchAgent deregisters the LaunchAgent from launchd. It prefers
// `launchctl unload -w` and falls back to `launchctl bootout`. Errors are
// ignored when the agent is not currently loaded.
func unloadLaunchAgent(path, label string) {
	if err := exec.Command("launchctl", "unload", "-w", path).Run(); err == nil {
		return
	}
	uid := os.Getuid()
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, label)).Run()
}
