package apps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const zcodeAppBundleName = "ZCode.app"

// ZCodeLaunchTarget resolves a managed or externally installed ZCode desktop target.
func ZCodeLaunchTarget() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("ZCode is installed, but the user home directory was not found")
	}
	if target, ok := readZCodeLaunchTargetFile(home); ok {
		return target, nil
	}
	if target, _, ok := findExternalZCodeTarget(home); ok {
		return target, nil
	}
	return "", fmt.Errorf("ZCode is installed, but no launch target was found")
}

func zcodeRuntimeRoot(home string) string {
	return filepath.Join(home, ".local", "share", "zcode")
}

func zcodeLauncherCandidates(home string) []string {
	dir := filepath.Join(home, ".local", "bin")
	if runtime.GOOS == "windows" {
		return []string{filepath.Join(dir, "zcode.cmd"), filepath.Join(dir, "zcode.exe")}
	}
	return []string{filepath.Join(dir, "zcode")}
}

func existingZCodeLauncher(home string) (string, bool) {
	for _, candidate := range zcodeLauncherCandidates(home) {
		if fileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func readZCodeVersionFile(home, fallback string) string {
	data, err := os.ReadFile(filepath.Join(zcodeRuntimeRoot(home), "version"))
	if err == nil {
		if version := strings.TrimSpace(string(data)); version != "" {
			return version
		}
	}
	return fallback
}

func readZCodeLaunchTargetFile(home string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(zcodeRuntimeRoot(home), "launch-target"))
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(string(data))
	if target == "" || !pathExists(target) {
		return "", false
	}
	return target, true
}

func detectZCodeInstall() (string, string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", false
	}
	if target, ok := readZCodeLaunchTargetFile(home); ok {
		return target, readZCodeVersionFile(home, zcodeTargetVersion(target)), true
	}
	if launcher, ok := existingZCodeLauncher(home); ok {
		return launcher, readZCodeVersionFile(home, launcher), true
	}
	if target, version, ok := findExternalZCodeTarget(home); ok {
		return target, version, true
	}
	return "", "", false
}

func findExternalZCodeTarget(home string) (string, string, bool) {
	switch runtime.GOOS {
	case "darwin":
		for _, bundle := range []string{
			filepath.Join(home, "Applications", zcodeAppBundleName),
			filepath.Join(string(filepath.Separator), "Applications", zcodeAppBundleName),
		} {
			if directoryExists(bundle) {
				return bundle, readDarwinBundleShortVersion(bundle), true
			}
		}
	case "windows":
		for _, binary := range windowsZCodeCandidates(home) {
			if fileExists(binary) {
				return binary, binary, true
			}
		}
	case "linux":
		if binary, err := exec.LookPath("zcode"); err == nil && fileExists(binary) {
			return binary, binary, true
		}
		for _, binary := range linuxZCodeCandidates(home) {
			if fileExists(binary) {
				return binary, binary, true
			}
		}
		for _, desktopFile := range []string{
			filepath.Join(home, ".local", "share", "applications", "zcode.desktop"),
			filepath.Join(string(filepath.Separator), "usr", "share", "applications", "zcode.desktop"),
		} {
			if target, ok := zcodeDesktopExec(desktopFile); ok {
				return target, target, true
			}
		}
	}
	return "", "", false
}

func windowsZCodeCandidates(home string) []string {
	candidates := make([]string, 0, 3)
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		candidates = append(candidates,
			filepath.Join(localAppData, "Programs", "ZCode", "ZCode.exe"),
			filepath.Join(localAppData, "ZCode", "ZCode.exe"),
		)
	}
	return append(candidates, filepath.Join(home, "AppData", "Local", "Programs", "ZCode", "ZCode.exe"))
}

func linuxZCodeCandidates(home string) []string {
	candidates := []string{
		filepath.Join(home, ".local", "bin", "zcode"),
		filepath.Join(string(filepath.Separator), "usr", "bin", "zcode"),
		filepath.Join(string(filepath.Separator), "opt", "ZCode", "zcode"),
		filepath.Join(home, "Applications", "ZCode.AppImage"),
	}
	matches, _ := filepath.Glob(filepath.Join(home, "Applications", "ZCode*.AppImage"))
	return uniqueNonEmptyPaths(append(candidates, matches...))
}

func zcodeDesktopExec(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "Exec=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Exec="))
		if strings.HasPrefix(value, `"`) {
			if end := strings.Index(value[1:], `"`); end >= 0 {
				value = value[1 : end+1]
			}
		} else if fields := strings.Fields(value); len(fields) > 0 {
			value = fields[0]
		}
		if value != "" && pathExists(value) {
			return value, true
		}
	}
	return "", false
}

func zcodeTargetVersion(target string) string {
	if runtime.GOOS == "darwin" && strings.HasSuffix(target, ".app") {
		return readDarwinBundleShortVersion(target)
	}
	return target
}

func looksLikeZCodeInstall(installPath string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || installPath == "" {
		return false
	}
	target, ok := readZCodeLaunchTargetFile(home)
	return ok && samePath(target, installPath) && pathWithinBase(target, zcodeRuntimeRoot(home))
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
