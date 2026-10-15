package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

const desktopFileName = "csghub-lite.desktop"

func desktopFilePath() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "autostart", desktopFileName), nil
}

func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func desktopEntry(binary string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=CSGLite
Exec=%s serve
Hidden=false
NoDisplay=true
X-GNOME-Autostart-enabled=true
Comment=Start csghub-lite server on login
`, binary)
}

// IsEnabled reports whether the XDG autostart desktop entry exists.
func IsEnabled() (bool, error) {
	path, err := desktopFilePath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Enable creates an XDG autostart desktop entry.
func Enable() error {
	binary, err := executablePath()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}

	path, err := desktopFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(desktopEntry(binary)), 0o644)
}

// Disable removes the XDG autostart desktop entry.
func Disable() error {
	path, err := desktopFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
