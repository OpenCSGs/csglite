package apps

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectCodexAppInstallFromUserApplications(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin Applications detection")
	}

	home := setTempHome(t)
	bundle := writeDarwinCodexAppBundle(t, filepath.Join(home, "Applications"), "26.616.31447")

	installPath, version, ok := detectCodexAppInstall()
	if !ok {
		t.Fatal("expected Codex App in ~/Applications to be detected")
	}
	if installPath != bundle {
		t.Fatalf("install path = %q, want %q", installPath, bundle)
	}
	if version != "26.616.31447" {
		t.Fatalf("version = %q, want 26.616.31447", version)
	}

	mgr := NewManager(nil)
	info, err := mgr.Get(t.Context(), "codex-app")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !info.Installed {
		t.Fatal("expected codex-app to be detected as installed")
	}
	if info.Managed {
		t.Fatal("expected Applications install to remain unmanaged")
	}
}

func TestCodexAppLaunchTargetPrefersManagedLaunchTarget(t *testing.T) {
	home := setTempHome(t)

	managedBundle := filepath.Join(home, ".local", "share", "codex-app", "versions", "26.527.31326", codexAppBundleName)
	if runtime.GOOS == "windows" {
		managedBundle = filepath.Join(home, ".local", "share", "codex-app", "versions", "26.527.31326", "Codex.exe")
		if err := os.MkdirAll(filepath.Dir(managedBundle), 0o755); err != nil {
			t.Fatalf("mkdir exe dir: %v", err)
		}
		if err := os.WriteFile(managedBundle, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write exe stub: %v", err)
		}
	} else if err := os.MkdirAll(managedBundle, 0o755); err != nil {
		t.Fatalf("mkdir managed bundle: %v", err)
	}

	userBundle := writeDarwinCodexAppBundle(t, filepath.Join(home, "Applications"), "1.0.0")
	runtimeRoot := codexAppRuntimeRoot(home)
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		t.Fatalf("mkdir runtime root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "launch-target"), []byte(managedBundle+"\n"), 0o644); err != nil {
		t.Fatalf("write launch target: %v", err)
	}

	got, err := CodexAppLaunchTarget()
	if err != nil {
		t.Fatalf("CodexAppLaunchTarget() error: %v", err)
	}
	if got != managedBundle {
		t.Fatalf("CodexAppLaunchTarget() = %q, want %q", got, managedBundle)
	}
	_ = userBundle
}

func TestCodexAppLaunchTargetFallsBackToUserApplications(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin Applications fallback")
	}

	home := setTempHome(t)
	bundle := writeDarwinCodexAppBundle(t, filepath.Join(home, "Applications"), "26.616.31447")

	got, err := CodexAppLaunchTarget()
	if err != nil {
		t.Fatalf("CodexAppLaunchTarget() error: %v", err)
	}
	if got != bundle {
		t.Fatalf("CodexAppLaunchTarget() = %q, want %q", got, bundle)
	}
}

func TestResolveCodexAppFromUninstallEntriesDisplayIcon(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Codex.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write exe stub: %v", err)
	}

	entries := []windowsUninstallEntry{
		{DisplayName: "Some Other Tool", DisplayIcon: exe},
		{DisplayName: "Codex", DisplayVersion: "1.2.3", DisplayIcon: exe + ",0"},
	}

	binary, version, ok := resolveCodexAppFromUninstallEntries(entries)
	if !ok {
		t.Fatal("expected Codex uninstall entry to resolve")
	}
	if binary != exe {
		t.Fatalf("binary = %q, want %q", binary, exe)
	}
	if version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", version)
	}
}

func TestResolveCodexAppFromUninstallEntriesInstallLocation(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "codex-app.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write exe stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unins000.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatalf("write uninstaller stub: %v", err)
	}

	entries := []windowsUninstallEntry{
		{DisplayName: "Codex App", InstallLocation: dir},
	}

	binary, _, ok := resolveCodexAppFromUninstallEntries(entries)
	if !ok {
		t.Fatal("expected Codex uninstall entry to resolve")
	}
	if binary != exe {
		t.Fatalf("binary = %q, want %q", binary, exe)
	}
}

func TestWindowsDisplayIconPath(t *testing.T) {
	cases := map[string]string{
		`C:\Tools\Codex\Codex.exe,0`:   `C:\Tools\Codex\Codex.exe`,
		`"C:\Tools\Codex\Codex.exe"`:   `C:\Tools\Codex\Codex.exe`,
		`C:\Tools\Codex\Codex.exe`:     `C:\Tools\Codex\Codex.exe`,
		`C:\Tools\Codex\codex.ico`:     "",
		`C:\Tools\Codex\Codex.exe,ico`: "",
	}
	for input, want := range cases {
		if got := windowsDisplayIconPath(input); got != want {
			t.Fatalf("windowsDisplayIconPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWindowsCodexAppDirCandidates(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "LOCALAPPDATA":
			return `C:\Users\me\AppData\Local`
		case "ProgramFiles":
			return `C:\Program Files`
		default:
			return ""
		}
	}

	candidates := windowsCodexAppDirCandidates(getenv)
	want := []string{
		filepath.Join(`C:\Users\me\AppData\Local`, "Programs", "Codex"),
		filepath.Join(`C:\Users\me\AppData\Local`, "Programs", "codex-app"),
		filepath.Join(`C:\Users\me\AppData\Local`, "Codex"),
		filepath.Join(`C:\Program Files`, "Codex"),
	}
	if len(candidates) != len(want) {
		t.Fatalf("candidates = %v, want %v", candidates, want)
	}
	for i := range want {
		if candidates[i] != want[i] {
			t.Fatalf("candidates[%d] = %q, want %q", i, candidates[i], want[i])
		}
	}
}

func TestFindCodexAppExeInDirPrefersCanonicalNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"codex-updater.exe", "Codex.exe", "unins000.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stub"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	binary, ok := findCodexAppExeInDir(dir)
	if !ok {
		t.Fatal("expected exe to be found")
	}
	if filepath.Base(binary) != "Codex.exe" {
		t.Fatalf("binary = %q, want Codex.exe", binary)
	}
}

func TestSetCodexAppLaunchTarget(t *testing.T) {
	home := setTempHome(t)

	target := filepath.Join(home, "MyTools", "Codex.app")
	if runtime.GOOS == "windows" {
		target = filepath.Join(home, "MyTools", "Codex.exe")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir target dir: %v", err)
		}
		if err := os.WriteFile(target, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write exe stub: %v", err)
		}
	} else if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir app bundle: %v", err)
	}

	if err := SetCodexAppLaunchTarget(target); err != nil {
		t.Fatalf("SetCodexAppLaunchTarget() error: %v", err)
	}

	installPath, _, ok := detectCodexAppInstall()
	if !ok {
		t.Fatal("expected manual launch target to be detected")
	}
	if installPath != target {
		t.Fatalf("install path = %q, want %q", installPath, target)
	}

	got, err := CodexAppLaunchTarget()
	if err != nil {
		t.Fatalf("CodexAppLaunchTarget() error: %v", err)
	}
	if got != target {
		t.Fatalf("CodexAppLaunchTarget() = %q, want %q", got, target)
	}
}

func TestSetCodexAppLaunchTargetRejectsInvalidPaths(t *testing.T) {
	home := setTempHome(t)

	if err := SetCodexAppLaunchTarget(""); err == nil {
		t.Fatal("expected error for empty path")
	}
	if err := SetCodexAppLaunchTarget(filepath.Join(home, "missing", "Codex.exe")); err == nil {
		t.Fatal("expected error for missing path")
	}

	plainDir := filepath.Join(home, "not-a-bundle")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatalf("mkdir plain dir: %v", err)
	}
	if err := SetCodexAppLaunchTarget(plainDir); err == nil {
		t.Fatal("expected error for plain directory")
	}
}

func writeDarwinCodexAppBundle(t *testing.T, parentDir, version string) string {
	t.Helper()

	bundle := filepath.Join(parentDir, codexAppBundleName)
	contentsDir := filepath.Join(bundle, "Contents")
	if err := os.MkdirAll(contentsDir, 0o755); err != nil {
		t.Fatalf("mkdir bundle contents: %v", err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleShortVersionString</key>
  <string>` + version + `</string>
</dict>
</plist>
`
	if err := os.WriteFile(filepath.Join(contentsDir, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatalf("write Info.plist: %v", err)
	}
	return bundle
}
