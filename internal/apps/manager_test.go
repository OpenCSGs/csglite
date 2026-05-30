package apps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestAppSpecsRequirePTYForClaudeInstall(t *testing.T) {
	var claude appSpec
	found := false
	for _, spec := range appSpecs() {
		if spec.id == "claude-code" {
			claude = spec
			found = true
			break
		}
	}
	if !found {
		t.Fatal("claude-code spec not found")
	}
	if claude.unix == nil || !claude.unix.requiresPTY {
		t.Fatal("claude-code unix installer should require PTY")
	}
	if claude.windows == nil || !claude.windows.requiresPTY {
		t.Fatal("claude-code windows installer should require PTY")
	}
	if claude.uninstallUnix != nil && claude.uninstallUnix.requiresPTY {
		t.Fatal("claude-code unix uninstaller should not require PTY")
	}
	if claude.uninstallWin != nil && claude.uninstallWin.requiresPTY {
		t.Fatal("claude-code windows uninstaller should not require PTY")
	}
}

func TestClaudeInstallerMirrorURLsUseCurrentRepoScripts(t *testing.T) {
	var claude appSpec
	found := false
	for _, spec := range appSpecs() {
		if spec.id == "claude-code" {
			claude = spec
			found = true
			break
		}
	}
	if !found {
		t.Fatal("claude-code spec not found")
	}

	wantUnix := repoRawBaseURL + "/internal/apps/scripts/claude-code-install.sh"
	if claude.unix == nil || claude.unix.mirrorURL != wantUnix {
		got := "<nil>"
		if claude.unix != nil {
			got = claude.unix.mirrorURL
		}
		t.Fatalf("claude-code unix mirrorURL = %q, want %q", got, wantUnix)
	}

	wantWindows := repoRawBaseURL + "/internal/apps/scripts/claude-code-install.ps1"
	if claude.windows == nil || claude.windows.mirrorURL != wantWindows {
		got := "<nil>"
		if claude.windows != nil {
			got = claude.windows.mirrorURL
		}
		t.Fatalf("claude-code windows mirrorURL = %q, want %q", got, wantWindows)
	}
}

func TestAntigravityAppSpecUsesMirroredInstallerAndAgyBinary(t *testing.T) {
	var antigravity appSpec
	found := false
	for _, spec := range appSpecs() {
		if spec.id == "antigravity" {
			antigravity = spec
			found = true
			break
		}
	}
	if !found {
		t.Fatal("antigravity spec not found")
	}
	if antigravity.binaryName != "agy" {
		t.Fatalf("antigravity binaryName = %q, want agy", antigravity.binaryName)
	}
	if antigravity.latest == nil || antigravity.latest.envVar != "CSGHUB_LITE_ANTIGRAVITY_DIST_BASE_URL" {
		t.Fatalf("antigravity latest source = %#v, want mirror env override", antigravity.latest)
	}
	if antigravity.unix == nil || antigravity.unix.mirrorURL != mirrorBaseURL+"/antigravity/install.sh" {
		t.Fatalf("antigravity unix installer = %#v, want mirrored install.sh", antigravity.unix)
	}
	if antigravity.windows == nil || antigravity.windows.mirrorURL != mirrorBaseURL+"/antigravity/install.ps1" {
		t.Fatalf("antigravity windows installer = %#v, want mirrored install.ps1", antigravity.windows)
	}
}

func TestDetectInstalledBinaryPathFallsBackToCommonDirs(t *testing.T) {
	homeDir := setTempHome(t)
	t.Setenv("PATH", "")

	binDir := filepath.Join(homeDir, ".local", "bin")
	binaryPath := writeFakeBinary(t, binDir, "claude")

	got, ok := detectInstalledBinaryPath("claude")
	if !ok {
		t.Fatal("detectInstalledBinaryPath should find claude in ~/.local/bin")
	}
	if got != binaryPath {
		t.Fatalf("detectInstalledBinaryPath = %q, want %q", got, binaryPath)
	}
}

func TestNewManagerMarksPreexistingInstallAsExternal(t *testing.T) {
	homeDir := setTempHome(t)
	t.Setenv("PATH", "")

	binDir := filepath.Join(homeDir, ".local", "bin")
	binaryPath := writeFakeBinary(t, binDir, "claude")

	mgr := NewManager(nil)
	info, err := mgr.Get(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !info.Installed {
		t.Fatal("expected claude-code to be detected as installed")
	}
	if info.Managed {
		t.Fatal("expected preexisting install to stay unmanaged")
	}
	if info.Status != "installed" {
		t.Fatalf("status = %q, want installed", info.Status)
	}
	if info.InstallPath != binaryPath {
		t.Fatalf("install path = %q, want %q", info.InstallPath, binaryPath)
	}
}

func TestRefreshAllMarksManagedInstallFromMarker(t *testing.T) {
	homeDir := setTempHome(t)
	t.Setenv("PATH", "")

	binDir := filepath.Join(homeDir, ".local", "bin")
	writeFakeBinary(t, binDir, "claude")

	mgr := NewManager(nil)
	if err := mgr.markManagedInstall("claude-code"); err != nil {
		t.Fatalf("mark managed install: %v", err)
	}
	if err := mgr.RefreshAll(context.Background()); err != nil {
		t.Fatalf("RefreshAll returned error: %v", err)
	}

	info, err := mgr.Get(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !info.Installed {
		t.Fatal("expected claude-code to remain installed")
	}
	if !info.Managed {
		t.Fatal("expected managed marker to be reflected in app state")
	}
}

func TestInstallReturnsExistingExternalAppWithoutRunningInstaller(t *testing.T) {
	homeDir := setTempHome(t)
	t.Setenv("PATH", "")

	binDir := filepath.Join(homeDir, ".local", "bin")
	writeFakeBinary(t, binDir, "claude")

	mgr := NewManager(nil)
	info, err := mgr.Install("claude-code")
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if !info.Installed {
		t.Fatal("expected existing app to remain installed")
	}
	if info.Managed {
		t.Fatal("expected existing external app to stay unmanaged")
	}
	if info.Status != "installed" {
		t.Fatalf("status = %q, want installed", info.Status)
	}
	if _, err := os.Stat(info.LogPath); !os.IsNotExist(err) {
		t.Fatalf("expected installer not to create a log file, stat err = %v", err)
	}
	if state := mgr.states["claude-code"]; state != nil && state.running {
		t.Fatal("expected installer to short-circuit for unmanaged existing app")
	}
}

func TestEnrichLatestVersionReportsMirrorUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			t.Fatalf("latest path = %q, want /latest", r.URL.Path)
		}
		_, _ = w.Write([]byte("1.2.4\n"))
	}))
	defer server.Close()

	t.Setenv("CSGHUB_LITE_CLAUDE_DIST_BASE_URL", server.URL)
	mgr := NewManager(nil)
	info := api.AIAppInfo{
		ID:        "claude-code",
		Installed: true,
		Managed:   true,
		Supported: true,
		Version:   "claude 1.2.3",
	}

	mgr.EnrichLatestVersion(context.Background(), &info)

	if info.LatestVersion != "1.2.4" {
		t.Fatalf("latest_version = %q, want 1.2.4", info.LatestVersion)
	}
	if !info.UpdateAvailable {
		t.Fatal("expected update_available to be true")
	}
}

func TestEnrichLatestVersionIgnoresMatchingMirrorVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("v1.2.3\n"))
	}))
	defer server.Close()

	t.Setenv("CSGHUB_LITE_CLAUDE_DIST_BASE_URL", server.URL)
	mgr := NewManager(nil)
	info := api.AIAppInfo{
		ID:        "claude-code",
		Installed: true,
		Managed:   true,
		Supported: true,
		Version:   "Claude Code 1.2.3",
	}

	mgr.EnrichLatestVersion(context.Background(), &info)

	if info.LatestVersion != "v1.2.3" {
		t.Fatalf("latest_version = %q, want v1.2.3", info.LatestVersion)
	}
	if info.UpdateAvailable {
		t.Fatal("expected update_available to be false")
	}
}

func TestAppUpdateAvailableComparesVersionOrder(t *testing.T) {
	tests := []struct {
		name      string
		installed string
		latest    string
		want      bool
	}{
		{
			name:      "newer installed version is not an update",
			installed: "2.1.132 (Claude Code)",
			latest:    "2.1.126",
			want:      false,
		},
		{
			name:      "newer latest version is an update",
			installed: "Claude Code 2.1.126",
			latest:    "2.1.132",
			want:      true,
		},
		{
			name:      "matching normalized versions are not an update",
			installed: "v1.2.3",
			latest:    "1.2.3",
			want:      false,
		},
		{
			name:      "missing patch segment compares as zero",
			installed: "1.2",
			latest:    "1.2.1",
			want:      true,
		},
		{
			name:      "unparseable versions fall back to normalized equality",
			installed: "dev-build",
			latest:    "other-build",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appUpdateAvailable(tt.installed, tt.latest); got != tt.want {
				t.Fatalf("appUpdateAvailable(%q, %q) = %v, want %v", tt.installed, tt.latest, got, tt.want)
			}
		})
	}
}

func TestFetchLatestVersionParsesGitHubReleaseTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirrors https://csgclaw.opencsg.com/releases/latest (GitHub-compatible JSON).
		if r.URL.Path != "/releases/latest" {
			t.Fatalf("latest path = %q, want /releases/latest", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.8"}`))
	}))
	defer server.Close()

	mgr := NewManager(nil)
	latest, err := mgr.fetchLatestVersion(context.Background(), appSpec{
		id: "csgclaw",
		latest: &latestVersionSource{
			baseURL: server.URL + "/releases/latest",
			format:  "github-release",
		},
	})
	if err != nil {
		t.Fatalf("fetchLatestVersion returned error: %v", err)
	}
	if latest != "v0.2.8" {
		t.Fatalf("latest = %q, want v0.2.8", latest)
	}
}

func TestFetchLatestVersionParsesVersionJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			t.Fatalf("latest path = %q, want /releases/latest", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"v0.2.8","count":6}`))
	}))
	defer server.Close()

	mgr := NewManager(nil)
	latest, err := mgr.fetchLatestVersion(context.Background(), appSpec{
		id: "csgclaw",
		latest: &latestVersionSource{
			baseURL: server.URL + "/releases/latest",
			format:  "version-json",
		},
	})
	if err != nil {
		t.Fatalf("fetchLatestVersion returned error: %v", err)
	}
	if latest != "v0.2.8" {
		t.Fatalf("latest = %q, want v0.2.8", latest)
	}
}

func TestNewManagerBackfillsLegacyManagedClaudeInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy Claude launcher backfill is covered on Unix-style installs")
	}

	homeDir := setTempHome(t)
	t.Setenv("PATH", "")

	runtimeDir := filepath.Join(homeDir, ".local", "share", "claude", "versions", "test-version")
	runtimePath := writeFakeBinary(t, runtimeDir, "claude")
	launcherDir := filepath.Join(homeDir, ".local", "bin")
	if err := os.MkdirAll(launcherDir, 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	launcherPath := filepath.Join(launcherDir, "claude")
	if err := os.Symlink(runtimePath, launcherPath); err != nil {
		t.Fatalf("create launcher symlink: %v", err)
	}

	mgr := NewManager(nil)
	info, err := mgr.Get(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !info.Installed {
		t.Fatal("expected legacy Claude install to be detected")
	}
	if !info.Managed {
		t.Fatal("expected legacy Claude install to be backfilled as managed")
	}
	if info.InstallPath != launcherPath {
		t.Fatalf("install path = %q, want %q", info.InstallPath, launcherPath)
	}
	if _, err := os.Stat(mgr.managedMarkerPath("claude-code")); err != nil {
		t.Fatalf("expected managed marker to be backfilled: %v", err)
	}
}

func TestNewManagerBackfillsLegacyManagedOpenClawLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy OpenClaw launcher backfill is currently Unix-specific")
	}

	homeDir := setTempHome(t)
	t.Setenv("PATH", "")

	targetDir := filepath.Join(homeDir, "mock-bin")
	targetPath := writeFakeBinary(t, targetDir, "openclaw")
	launcherPath := writeOpenClawLauncher(t, filepath.Join(homeDir, "bin"), targetPath)

	mgr := NewManager(nil)
	info, err := mgr.Get(context.Background(), "openclaw")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !info.Installed {
		t.Fatal("expected legacy OpenClaw launcher to be detected")
	}
	if !info.Managed {
		t.Fatal("expected legacy OpenClaw launcher to be backfilled as managed")
	}
	if info.InstallPath != launcherPath {
		t.Fatalf("install path = %q, want %q", info.InstallPath, launcherPath)
	}
	if _, err := os.Stat(mgr.managedMarkerPath("openclaw")); err != nil {
		t.Fatalf("expected managed marker to be backfilled: %v", err)
	}
}

func TestNewManagerKeepsHomeBinOpenClawBinaryUnmanaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("home bin launcher distinction is Unix-specific")
	}

	homeDir := setTempHome(t)
	t.Setenv("PATH", "")

	binaryPath := writeFakeBinary(t, filepath.Join(homeDir, "bin"), "openclaw")

	mgr := NewManager(nil)
	info, err := mgr.Get(context.Background(), "openclaw")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !info.Installed {
		t.Fatal("expected OpenClaw binary to be detected as installed")
	}
	if info.Managed {
		t.Fatal("expected plain home-bin OpenClaw binary to remain unmanaged")
	}
	if info.InstallPath != binaryPath {
		t.Fatalf("install path = %q, want %q", info.InstallPath, binaryPath)
	}
}

func TestWriteTempScriptRecreatesMissingTMPDIR(t *testing.T) {
	setTempHome(t)

	staleTempDir := filepath.Join(t.TempDir(), "stale-tmpdir")
	t.Setenv("TMPDIR", staleTempDir)

	content := []byte("#!/usr/bin/env bash\necho ok\n")
	mgr := &Manager{}
	scriptPath, err := mgr.writeTempScript("claude-code", nil, content)
	if err != nil {
		t.Fatalf("writeTempScript returned error: %v", err)
	}
	defer os.Remove(scriptPath)

	if got := filepath.Dir(scriptPath); got != staleTempDir {
		t.Fatalf("script dir = %q, want %q", got, staleTempDir)
	}
	if _, err := os.Stat(staleTempDir); err != nil {
		t.Fatalf("stale temp dir should be recreated: %v", err)
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read temp script: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("temp script contents = %q, want %q", string(data), string(content))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(scriptPath)
		if err != nil {
			t.Fatalf("stat temp script: %v", err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("temp script mode = %v, want executable bit set", info.Mode().Perm())
		}
	}
}

func TestSummarizeFailureLogsPrefersExplicitError(t *testing.T) {
	lines := []string{
		"2026-03-28 23:13:43 INFO: preparing uninstaller",
		"2026-03-28 23:13:44 npm error ENOTEMPTY: directory not empty, rename '/opt/homebrew/lib/node_modules/openclaw' -> '/opt/homebrew/lib/node_modules/.openclaw-2N5mgx4q'",
		"2026-03-28 23:13:45 ERROR: OpenClaw binary is still available at /Users/test/bin/openclaw",
	}

	got := summarizeFailureLogs(lines)
	want := "OpenClaw binary is still available at /Users/test/bin/openclaw"
	if got != want {
		t.Fatalf("summarizeFailureLogs = %q, want %q", got, want)
	}
}

func TestSummarizeFailureLogsReturnsActionableNPMError(t *testing.T) {
	lines := []string{
		"2026-03-28 23:13:44 npm error code ENOTEMPTY",
		"2026-03-28 23:13:44 npm error syscall rename",
		"2026-03-28 23:13:44 npm error path /opt/homebrew/lib/node_modules/openclaw",
		"2026-03-28 23:13:44 npm error dest /opt/homebrew/lib/node_modules/.openclaw-2N5mgx4q",
		"2026-03-28 23:13:44 npm error errno -66",
		"2026-03-28 23:13:44 npm error ENOTEMPTY: directory not empty, rename '/opt/homebrew/lib/node_modules/openclaw' -> '/opt/homebrew/lib/node_modules/.openclaw-2N5mgx4q'",
		"2026-03-28 23:13:44 npm error A complete log of this run can be found in: /Users/test/.npm/_logs/debug.log",
	}

	got := summarizeFailureLogs(lines)
	want := "npm error ENOTEMPTY: directory not empty, rename '/opt/homebrew/lib/node_modules/openclaw' -> '/opt/homebrew/lib/node_modules/.openclaw-2N5mgx4q'"
	if got != want {
		t.Fatalf("summarizeFailureLogs = %q, want %q", got, want)
	}
}

func TestStripLogTimestamp(t *testing.T) {
	got := stripLogTimestamp("2026-03-28 23:13:44 npm error ENOTEMPTY: directory not empty")
	want := "npm error ENOTEMPTY: directory not empty"
	if got != want {
		t.Fatalf("stripLogTimestamp = %q, want %q", got, want)
	}
}

func setTempHome(t *testing.T) string {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(homeDir, "AppData", "Roaming"))
	}
	return homeDir
}

func writeFakeBinary(t *testing.T, dir, name string) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}

	commandPath := filepath.Join(dir, name)
	content := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo test-version; fi\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(dir, name+".cmd")
		content = "@echo off\r\nif \"%1\"==\"--version\" echo test-version\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return commandPath
}

func writeOpenClawLauncher(t *testing.T, dir, targetPath string) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	launcherPath := filepath.Join(dir, "openclaw")
	content := "#!/usr/bin/env bash\n" +
		"export PATH=\"" + filepath.Dir(targetPath) + ":$PATH\"\n" +
		"exec \"" + targetPath + "\" \"$@\"\n"
	if err := os.WriteFile(launcherPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	return launcherPath
}
