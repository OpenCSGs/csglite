package apps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type installDetectMode string

const (
	installDetectCLI     installDetectMode = "cli"
	installDetectDesktop installDetectMode = "desktop"
)

// installDetectProfile documents how csghub-lite discovers an app that was
// installed outside the managed installer flow. Keep this table in sync with
// docs/agent-guidelines/app-installs.md.
type installDetectProfile struct {
	mode           installDetectMode
	versionedShare string
	shareBinRel    string
	libBundleName  string
}

var installDetectProfiles = map[string]installDetectProfile{
	"claude-code":      {mode: installDetectCLI, versionedShare: "claude"},
	"open-code":        {mode: installDetectCLI, versionedShare: "opencode"},
	"open-code-review": {mode: installDetectCLI, versionedShare: "open-code-review"},
	"openclaw":         {mode: installDetectCLI},
	"csgclaw":          {mode: installDetectDesktop},
	"codex":            {mode: installDetectCLI, versionedShare: "codex"},
	"codex-app":        {mode: installDetectDesktop},
	"zcode":            {mode: installDetectDesktop},
	"pi":               {mode: installDetectCLI, shareBinRel: "pi-coding-agent/bin"},
	"kimi-code":        {mode: installDetectCLI, shareBinRel: "kimi-code/bin"},
	"dsh":              {mode: installDetectCLI, shareBinRel: "deepseek-harness/bin"},
}

// ResolveLaunchBinary returns a launchable binary path using the same lookup
// profile that install detection uses.
func ResolveLaunchBinary(appID, binaryName string) (string, bool) {
	profile, ok := installDetectProfiles[appID]
	if ok && profile.mode == installDetectDesktop {
		return "", false
	}
	return detectCLIAppBinary(binaryName, profile)
}

func detectInstalled(ctx context.Context, spec appSpec) (string, string, bool) {
	profile, ok := installDetectProfiles[spec.id]
	if !ok {
		if spec.binaryName == "" {
			return "", "", false
		}
		return detectInstalledCLI(ctx, spec, installDetectProfile{mode: installDetectCLI})
	}
	switch profile.mode {
	case installDetectDesktop:
		switch spec.id {
		case "codex-app":
			return detectCodexAppInstall()
		case "zcode":
			return detectZCodeInstall()
		case "csgclaw":
			return detectCSGClawDesktopInstall()
		default:
			return "", "", false
		}
	default:
		return detectInstalledCLI(ctx, spec, profile)
	}
}

func detectInstalledCLI(ctx context.Context, spec appSpec, profile installDetectProfile) (string, string, bool) {
	if spec.binaryName == "" {
		return "", "", false
	}
	path, ok := detectCLIAppBinary(spec.binaryName, profile)
	if !ok {
		return "", "", false
	}
	// Version probing is intentionally deferred: `--version` subprocesses
	// (notably pi/kimi) take hundreds of milliseconds each, which would block
	// the list response. detectAppVersion runs that asynchronously and
	// writes the result back to state. Return the path only here so the
	// "installed" determination stays fast.
	return path, "", true
}

// detectAppVersion runs the app's `--version` (or equivalent) subprocess and
// returns the display version. Callers should invoke this off the request
// path; it is the slow part of install detection.
func detectAppVersion(ctx context.Context, spec appSpec, path string) string {
	if len(spec.versionArgs) == 0 {
		return appDisplayVersion(spec, path)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, path, spec.versionArgs...).CombinedOutput()
	if err == nil {
		return appDisplayVersion(spec, strings.TrimSpace(string(out)))
	}
	return ""
}

func detectCLIAppBinary(binaryName string, profile installDetectProfile) (string, bool) {
	if path, ok := detectInstalledBinaryPath(binaryName); ok {
		return path, true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	if profile.versionedShare != "" {
		runtimeRoot := filepath.Join(home, ".local", "share", profile.versionedShare, "versions")
		if path, ok := findLegacyRuntimeBinary(runtimeRoot, binaryName); ok {
			return path, true
		}
	}
	if profile.shareBinRel != "" {
		candidate := filepath.Join(home, ".local", "share", profile.shareBinRel, launcherBinaryName(binaryName))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	if profile.libBundleName != "" {
		if path, ok := findLibBundleBinary(home, profile.libBundleName, binaryName); ok {
			return path, true
		}
	}
	return "", false
}

func findLibBundleBinary(home, bundleName, binaryName string) (string, bool) {
	libRoot := filepath.Join(home, ".local", "lib", bundleName)
	entries, err := os.ReadDir(libRoot)
	if err != nil {
		return "", false
	}
	name := launcherBinaryName(binaryName)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(libRoot, entry.Name(), bundleName, "bin", name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}
