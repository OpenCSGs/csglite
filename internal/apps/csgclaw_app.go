package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const csgclawAppBundleName = "CSGClaw.app"

// CSGClawDesktopLaunchTarget resolves a managed or externally installed
// CSGClaw Desktop target.
func CSGClawDesktopLaunchTarget() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("CSGClaw Desktop is installed, but the user home directory was not found")
	}
	if target, ok := readCSGClawDesktopLaunchTargetFile(home); ok {
		return target, nil
	}
	if target, _, ok := findExternalCSGClawDesktopTarget(home); ok {
		return target, nil
	}
	return "", fmt.Errorf("CSGClaw Desktop is installed, but no launch target was found")
}

func SetCSGClawDesktopLaunchTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("path is required")
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("path not found: %s", target)
	}
	if info.IsDir() && !strings.HasSuffix(strings.ToLower(target), ".app") {
		return fmt.Errorf("path must be an executable file or a macOS .app bundle: %s", target)
	}
	if !info.IsDir() && runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(target), ".exe") {
		return fmt.Errorf("path must point to a Windows .exe file: %s", target)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fmt.Errorf("user home directory was not found")
	}
	root := csgclawDesktopRuntimeRoot(home)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", root, err)
	}
	return os.WriteFile(filepath.Join(root, "launch-target"), []byte(target+"\n"), 0o644)
}

func csgclawDesktopRuntimeRoot(home string) string {
	return filepath.Join(home, ".local", "share", "csgclaw-desktop")
}

func readCSGClawDesktopLaunchTargetFile(home string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(csgclawDesktopRuntimeRoot(home), "launch-target"))
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(string(data))
	if target == "" || !pathExists(target) {
		return "", false
	}
	return target, true
}

func readCSGClawDesktopVersionFile(home, fallback string) string {
	data, err := os.ReadFile(filepath.Join(csgclawDesktopRuntimeRoot(home), "version"))
	if err == nil {
		if version := strings.TrimSpace(string(data)); version != "" {
			return version
		}
	}
	return fallback
}

func detectCSGClawDesktopInstall() (string, string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", false
	}
	if target, ok := readCSGClawDesktopLaunchTargetFile(home); ok {
		return target, readCSGClawDesktopVersionFile(home, csgclawDesktopTargetVersion(target)), true
	}
	return findExternalCSGClawDesktopTarget(home)
}

func findExternalCSGClawDesktopTarget(home string) (string, string, bool) {
	switch runtime.GOOS {
	case "darwin":
		for _, bundle := range []string{
			filepath.Join(home, "Applications", csgclawAppBundleName),
			filepath.Join(string(filepath.Separator), "Applications", csgclawAppBundleName),
		} {
			if directoryExists(bundle) {
				return bundle, readDarwinBundleShortVersion(bundle), true
			}
		}
	case "windows":
		for _, entry := range readWindowsCodexUninstallEntries() {
			if !strings.Contains(strings.ToLower(entry.DisplayName), "csgclaw") {
				continue
			}
			version := strings.TrimSpace(entry.DisplayVersion)
			if icon := windowsDisplayIconPath(entry.DisplayIcon); icon != "" && fileExists(icon) {
				if version == "" {
					version = icon
				}
				return icon, version, true
			}
			if target, ok := findCSGClawDesktopExeInDirs([]string{entry.InstallLocation}); ok {
				if version == "" {
					version = target
				}
				return target, version, true
			}
		}
		if target, ok := findCSGClawDesktopExeInDirs(windowsCSGClawDesktopDirCandidates(home)); ok {
			return target, target, true
		}
	}
	return "", "", false
}

func windowsCSGClawDesktopDirCandidates(home string) []string {
	var candidates []string
	for _, base := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(base, "Programs", "CSGClaw"),
			filepath.Join(base, "Programs", "csgclaw-desktop"),
			filepath.Join(base, "CSGClaw"),
			filepath.Join(base, "csgclaw-desktop"),
		)
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, "AppData", "Local", "Programs", "CSGClaw"))
	}
	return uniqueNonEmptyPaths(candidates)
}

func findCSGClawDesktopExeInDirs(dirs []string) (string, bool) {
	for _, dir := range dirs {
		entries, err := os.ReadDir(strings.TrimSpace(dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(entry.Name())
			if (name == "csgclaw.exe" || name == "csgclaw-desktop.exe") && !strings.Contains(name, "unins") {
				return filepath.Join(dir, entry.Name()), true
			}
		}
	}
	return "", false
}

func csgclawDesktopTargetVersion(target string) string {
	if runtime.GOOS == "darwin" && strings.HasSuffix(strings.ToLower(target), ".app") {
		return readDarwinBundleShortVersion(target)
	}
	return target
}

func looksLikeCSGClawDesktopInstall(installPath string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || installPath == "" {
		return false
	}
	target, ok := readCSGClawDesktopLaunchTargetFile(home)
	return ok && samePath(target, installPath) && pathWithinBase(target, csgclawDesktopRuntimeRoot(home))
}
