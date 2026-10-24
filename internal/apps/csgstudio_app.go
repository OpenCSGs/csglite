package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	csgstudioAppID          = "csgstudio"
	csgstudioAppBundleName  = "CSGStudio.app"
	csgstudioManifestURL    = "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/csgstudio/channels/release/downloads.json"
	csgstudioManifestEnvVar = "CSGHUB_LITE_CSGSTUDIO_MANIFEST_URL"
)

// CSGStudioLaunchTarget resolves a managed or externally installed CSGStudio
// target.
func CSGStudioLaunchTarget() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("CSGStudio is installed, but the user home directory was not found")
	}
	if target, ok := readCSGStudioLaunchTargetFile(home); ok {
		return target, nil
	}
	if target, _, ok := findExternalCSGStudioTarget(home); ok {
		return target, nil
	}
	return "", fmt.Errorf("CSGStudio is installed, but no launch target was found")
}

func SetCSGStudioLaunchTarget(target string) error {
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
	root := csgstudioRuntimeRoot(home)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", root, err)
	}
	return os.WriteFile(filepath.Join(root, "launch-target"), []byte(target+"\n"), 0o644)
}

func csgstudioRuntimeRoot(home string) string {
	return filepath.Join(home, ".local", "share", "csgstudio")
}

func readCSGStudioLaunchTargetFile(home string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(csgstudioRuntimeRoot(home), "launch-target"))
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(string(data))
	if target == "" || !pathExists(target) {
		return "", false
	}
	return target, true
}

func readCSGStudioVersionFile(home, fallback string) string {
	data, err := os.ReadFile(filepath.Join(csgstudioRuntimeRoot(home), "version"))
	if err == nil {
		if version := strings.TrimSpace(string(data)); version != "" {
			return version
		}
	}
	return fallback
}

func detectCSGStudioInstall() (string, string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", false
	}
	if target, ok := readCSGStudioLaunchTargetFile(home); ok {
		return target, readCSGStudioVersionFile(home, csgstudioTargetVersion(target)), true
	}
	return findExternalCSGStudioTarget(home)
}

func findExternalCSGStudioTarget(home string) (string, string, bool) {
	switch runtime.GOOS {
	case "darwin":
		for _, bundle := range []string{
			filepath.Join(home, "Applications", csgstudioAppBundleName),
			filepath.Join(string(filepath.Separator), "Applications", csgstudioAppBundleName),
		} {
			if directoryExists(bundle) {
				return bundle, readDarwinBundleShortVersion(bundle), true
			}
		}
	case "windows":
		for _, entry := range readWindowsCodexUninstallEntries() {
			if !strings.Contains(strings.ToLower(entry.DisplayName), "csgstudio") {
				continue
			}
			version := strings.TrimSpace(entry.DisplayVersion)
			if icon := windowsDisplayIconPath(entry.DisplayIcon); icon != "" && fileExists(icon) {
				if version == "" {
					version = icon
				}
				return icon, version, true
			}
			if target, ok := findCSGStudioExeInDirs([]string{entry.InstallLocation}); ok {
				if version == "" {
					version = target
				}
				return target, version, true
			}
		}
		if target, ok := findCSGStudioExeInDirs(windowsCSGStudioDirCandidates(home)); ok {
			return target, target, true
		}
	}
	return "", "", false
}

func windowsCSGStudioDirCandidates(home string) []string {
	var candidates []string
	for _, base := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(base, "Programs", "CSGStudio"),
			filepath.Join(base, "CSGStudio"),
		)
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, "AppData", "Local", "Programs", "CSGStudio"))
	}
	return uniqueNonEmptyPaths(candidates)
}

func findCSGStudioExeInDirs(dirs []string) (string, bool) {
	for _, dir := range dirs {
		entries, err := os.ReadDir(strings.TrimSpace(dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(entry.Name(), "csgstudio.exe") {
				return filepath.Join(dir, entry.Name()), true
			}
		}
	}
	return "", false
}

func csgstudioTargetVersion(target string) string {
	if runtime.GOOS == "darwin" && strings.HasSuffix(strings.ToLower(target), ".app") {
		return readDarwinBundleShortVersion(target)
	}
	return target
}

func looksLikeCSGStudioInstall(installPath string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || installPath == "" {
		return false
	}
	target, ok := readCSGStudioLaunchTargetFile(home)
	return ok && samePath(target, installPath) && pathWithinBase(target, csgstudioRuntimeRoot(home))
}
