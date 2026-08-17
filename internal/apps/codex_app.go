package apps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const codexAppBundleName = "Codex.app"

var xmlPlistStringPattern = regexp.MustCompile(`<key>([^<]+)</key>\s*<string>([^<]*)</string>`)

// CodexAppLaunchTarget resolves the local Codex App bundle path for desktop launch.
func CodexAppLaunchTarget() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("Codex App is installed, but the user home directory was not found")
	}

	if target, ok := readCodexAppLaunchTargetFile(home); ok {
		return target, nil
	}
	if bundle, ok := findDarwinCodexAppBundle(home); ok {
		return bundle, nil
	}
	if binary, _, ok := findWindowsCodexAppBinary(home); ok {
		return binary, nil
	}
	if binary, _, ok := findWindowsExternalCodexApp(); ok {
		return binary, nil
	}

	return "", fmt.Errorf("Codex App is installed, but no launch target was found")
}

// SetCodexAppLaunchTarget records a user-specified Codex App location in the
// launch-target file, which sits at the top of the detection and launch
// resolution order. This is the manual escape hatch for installs that live
// outside every scanned location.
func SetCodexAppLaunchTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("path is required")
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("path not found: %s", target)
	}
	if err := validateCodexAppLaunchTarget(target, info.IsDir()); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fmt.Errorf("user home directory was not found")
	}
	runtimeRoot := codexAppRuntimeRoot(home)
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", runtimeRoot, err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "launch-target"), []byte(target+"\n"), 0o644); err != nil {
		return fmt.Errorf("saving launch target: %w", err)
	}
	return nil
}

func validateCodexAppLaunchTarget(target string, isDir bool) error {
	if isDir {
		if strings.HasSuffix(strings.ToLower(target), ".app") {
			return nil
		}
		return fmt.Errorf("path must be an executable file or a macOS .app bundle: %s", target)
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(target), ".exe") {
		return fmt.Errorf("path must point to a Windows .exe file: %s", target)
	}
	return nil
}

func codexAppRuntimeRoot(home string) string {
	return filepath.Join(home, ".local", "share", "codex-app")
}

func codexAppLauncherCandidates(home string) []string {
	dir := filepath.Join(home, ".local", "bin")
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(dir, "codex-app.cmd"),
			filepath.Join(dir, "codex-app.exe"),
		}
	}
	return []string{filepath.Join(dir, "codex-app")}
}

func existingCodexAppLauncher(home string) (string, bool) {
	for _, candidate := range codexAppLauncherCandidates(home) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func readCodexAppVersionFile(home, fallback string) string {
	versionPath := filepath.Join(codexAppRuntimeRoot(home), "version")
	data, err := os.ReadFile(versionPath)
	if err != nil {
		return fallback
	}
	if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
		return trimmed
	}
	return fallback
}

func detectCodexAppInstall() (string, string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", false
	}
	if launcherPath, ok := existingCodexAppLauncher(home); ok {
		return launcherPath, readCodexAppVersionFile(home, launcherPath), true
	}
	if target, ok := readCodexAppLaunchTargetFile(home); ok {
		return target, readCodexAppVersionFile(home, readCodexAppTargetVersion(target)), true
	}
	if bundle, ok := findDarwinCodexAppBundle(home); ok {
		return bundle, readDarwinBundleShortVersion(bundle), true
	}
	if binary, version, ok := findWindowsCodexAppBinary(home); ok {
		return binary, version, true
	}
	if binary, version, ok := findWindowsExternalCodexApp(); ok {
		return binary, version, true
	}
	return "", "", false
}

// windowsUninstallEntry mirrors the relevant values of a Windows registry
// Uninstall entry (HK{CU,LM}\...\CurrentVersion\Uninstall\<key>).
type windowsUninstallEntry struct {
	DisplayName     string
	DisplayVersion  string
	InstallLocation string
	DisplayIcon     string
}

// findWindowsExternalCodexApp discovers a Codex App that the user installed
// outside the managed flow: first via registry Uninstall entries, then by
// scanning common per-user and system install directories.
func findWindowsExternalCodexApp() (string, string, bool) {
	if runtime.GOOS != "windows" {
		return "", "", false
	}
	if binary, version, ok := resolveCodexAppFromUninstallEntries(readWindowsCodexUninstallEntries()); ok {
		return binary, version, ok
	}
	if binary, ok := findCodexAppExeInDirs(windowsCodexAppDirCandidates(os.Getenv)); ok {
		return binary, binary, true
	}
	return "", "", false
}

func resolveCodexAppFromUninstallEntries(entries []windowsUninstallEntry) (string, string, bool) {
	for _, entry := range entries {
		name := strings.ToLower(strings.TrimSpace(entry.DisplayName))
		if !strings.Contains(name, "codex") {
			continue
		}
		version := strings.TrimSpace(entry.DisplayVersion)

		if icon := windowsDisplayIconPath(entry.DisplayIcon); icon != "" {
			if info, err := os.Stat(icon); err == nil && !info.IsDir() {
				if version == "" {
					version = icon
				}
				return icon, version, true
			}
		}

		location := strings.TrimSpace(entry.InstallLocation)
		if location == "" {
			continue
		}
		if binary, ok := findCodexAppExeInDirs([]string{location}); ok {
			if version == "" {
				version = binary
			}
			return binary, version, true
		}
	}
	return "", "", false
}

// windowsDisplayIconPath extracts the executable path from a DisplayIcon
// value, which may carry an icon index suffix such as `C:\...\Codex.exe,0`.
func windowsDisplayIconPath(displayIcon string) string {
	icon := strings.Trim(strings.TrimSpace(displayIcon), `"`)
	if idx := strings.LastIndex(icon, ","); idx >= 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(icon[idx+1:])); err == nil {
			icon = strings.TrimSpace(icon[:idx])
		}
	}
	icon = strings.Trim(icon, `"`)
	if !strings.EqualFold(filepath.Ext(icon), ".exe") {
		return ""
	}
	return icon
}

func windowsCodexAppDirCandidates(getenv func(string) string) []string {
	var candidates []string
	appendDirs := func(base string) {
		if base == "" {
			return
		}
		candidates = append(candidates,
			filepath.Join(base, "Programs", "Codex"),
			filepath.Join(base, "Programs", "codex-app"),
			filepath.Join(base, "Codex"),
		)
	}
	appendDirs(getenv("LOCALAPPDATA"))
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if base := getenv(env); base != "" {
			candidates = append(candidates, filepath.Join(base, "Codex"))
		}
	}
	return candidates
}

func findCodexAppExeInDirs(dirs []string) (string, bool) {
	for _, dir := range dirs {
		if binary, ok := findCodexAppExeInDir(dir); ok {
			return binary, true
		}
	}
	return "", false
}

func findCodexAppExeInDir(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var fallback string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".exe") || strings.Contains(name, "unins") {
			continue
		}
		if name == "codex.exe" || name == "codex-app.exe" {
			return filepath.Join(dir, entry.Name()), true
		}
		if fallback == "" && strings.HasPrefix(name, "codex") {
			fallback = filepath.Join(dir, entry.Name())
		}
	}
	if fallback != "" {
		return fallback, true
	}
	return "", false
}

func readCodexAppTargetVersion(target string) string {
	if runtime.GOOS == "darwin" && strings.HasSuffix(target, ".app") {
		return readDarwinBundleShortVersion(target)
	}
	return target
}

func findWindowsCodexAppBinary(home string) (string, string, bool) {
	if runtime.GOOS != "windows" {
		return "", "", false
	}
	runtimeRoot := filepath.Join(codexAppRuntimeRoot(home), "versions")
	entries, err := os.ReadDir(runtimeRoot)
	if err != nil {
		return "", "", false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		versionDir := filepath.Join(runtimeRoot, entry.Name())
		dirEntries, err := os.ReadDir(versionDir)
		if err != nil {
			continue
		}
		for _, file := range dirEntries {
			if file.IsDir() {
				continue
			}
			name := strings.ToLower(file.Name())
			if !strings.HasSuffix(name, ".exe") {
				continue
			}
			candidate := filepath.Join(versionDir, file.Name())
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, readCodexAppVersionFile(home, entry.Name()), true
			}
		}
	}
	return "", "", false
}

func looksLikeCodexAppInstall(installPath string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || installPath == "" {
		return false
	}
	if launcherPath, ok := existingCodexAppLauncher(home); ok && samePath(installPath, launcherPath) {
		target, ok := readCodexAppLaunchTargetFile(home)
		return ok && target != ""
	}
	return false
}

func readCodexAppLaunchTargetFile(home string) (string, bool) {
	targetPath := filepath.Join(codexAppRuntimeRoot(home), "launch-target")
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(string(data))
	if target == "" {
		return "", false
	}
	if _, err := os.Stat(target); err != nil {
		return "", false
	}
	return target, true
}

func darwinCodexAppBundleCandidates(home string) []string {
	candidates := make([]string, 0, 2)
	if home != "" {
		candidates = append(candidates, filepath.Join(home, "Applications", codexAppBundleName))
	}
	candidates = append(candidates, filepath.Join("/Applications", codexAppBundleName))
	return candidates
}

func findDarwinCodexAppBundle(home string) (string, bool) {
	if runtime.GOOS != "darwin" {
		return "", false
	}
	return findExistingDirectory(darwinCodexAppBundleCandidates(home))
}

func findExistingDirectory(paths []string) (string, bool) {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func readDarwinBundleShortVersion(bundlePath string) string {
	plistPath := filepath.Join(bundlePath, "Contents", "Info.plist")
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return bundlePath
	}
	if version := parseXMLPlistString(data, "CFBundleShortVersionString"); version != "" {
		return version
	}
	if runtime.GOOS == "darwin" {
		if version := readDefaultsBundleVersion(bundlePath); version != "" {
			return version
		}
	}
	return bundlePath
}

func parseXMLPlistString(data []byte, key string) string {
	matches := xmlPlistStringPattern.FindAllSubmatch(data, -1)
	for _, match := range matches {
		if string(match[1]) != key {
			continue
		}
		version := strings.TrimSpace(string(match[2]))
		if version != "" {
			return version
		}
	}
	return ""
}

func readDefaultsBundleVersion(bundlePath string) string {
	out, err := exec.Command("defaults", "read", bundlePath, "CFBundleShortVersionString").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
