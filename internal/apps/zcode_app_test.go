package apps

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectZCodeInstallFromManagedLaunchTarget(t *testing.T) {
	home := setTempHome(t)
	target := writeZCodeTestTarget(t, filepath.Join(zcodeRuntimeRoot(home), "versions", "3.3.4"))
	if err := os.MkdirAll(zcodeRuntimeRoot(home), 0o755); err != nil {
		t.Fatalf("mkdir runtime root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(zcodeRuntimeRoot(home), "launch-target"), []byte(target+"\n"), 0o644); err != nil {
		t.Fatalf("write launch target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(zcodeRuntimeRoot(home), "version"), []byte("3.3.4\n"), 0o644); err != nil {
		t.Fatalf("write version: %v", err)
	}

	installPath, version, ok := detectZCodeInstall()
	if !ok {
		t.Fatal("expected managed ZCode target to be detected")
	}
	if installPath != target || version != "3.3.4" {
		t.Fatalf("detected (%q, %q), want (%q, 3.3.4)", installPath, version, target)
	}
	got, err := ZCodeLaunchTarget()
	if err != nil {
		t.Fatalf("ZCodeLaunchTarget() error: %v", err)
	}
	if got != target {
		t.Fatalf("ZCodeLaunchTarget() = %q, want %q", got, target)
	}
}

func TestZCodeLaunchTargetRejectsMissingManagedTarget(t *testing.T) {
	home := setTempHome(t)
	if err := os.MkdirAll(zcodeRuntimeRoot(home), 0o755); err != nil {
		t.Fatalf("mkdir runtime root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(zcodeRuntimeRoot(home), "launch-target"), []byte(filepath.Join(home, "missing")+"\n"), 0o644); err != nil {
		t.Fatalf("write launch target: %v", err)
	}
	if _, ok := readZCodeLaunchTargetFile(home); ok {
		t.Fatal("missing launch target should not be accepted")
	}
}

func TestDetectZCodeExternalMacApplication(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS application detection")
	}
	home := setTempHome(t)
	bundle := filepath.Join(home, "Applications", zcodeAppBundleName)
	contents := filepath.Join(bundle, "Contents")
	if err := os.MkdirAll(contents, 0o755); err != nil {
		t.Fatalf("mkdir app bundle: %v", err)
	}
	plist := []byte(`<?xml version="1.0"?><plist><dict><key>CFBundleShortVersionString</key><string>3.3.4</string></dict></plist>`)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), plist, 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}

	path, version, ok := detectZCodeInstall()
	if !ok || path != bundle || version != "3.3.4" {
		t.Fatalf("detected (%q, %q, %v), want (%q, 3.3.4, true)", path, version, ok, bundle)
	}
}

func TestZCodeDesktopExecParsesQuotedTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ZCode AppImage")
	if err := os.WriteFile(target, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	desktop := filepath.Join(dir, "zcode.desktop")
	if err := os.WriteFile(desktop, []byte("[Desktop Entry]\nExec=\""+target+"\" %U\n"), 0o644); err != nil {
		t.Fatalf("write desktop entry: %v", err)
	}
	got, ok := zcodeDesktopExec(desktop)
	if !ok || got != target {
		t.Fatalf("zcodeDesktopExec() = (%q, %v), want (%q, true)", got, ok, target)
	}
}

func writeZCodeTestTarget(t *testing.T, versionDir string) string {
	t.Helper()
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	switch runtime.GOOS {
	case "darwin":
		target := filepath.Join(versionDir, zcodeAppBundleName)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("mkdir app target: %v", err)
		}
		return target
	case "windows":
		target := filepath.Join(versionDir, "ZCode.exe")
		if err := os.WriteFile(target, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write exe target: %v", err)
		}
		return target
	default:
		target := filepath.Join(versionDir, "ZCode.AppImage")
		if err := os.WriteFile(target, []byte("stub"), 0o755); err != nil {
			t.Fatalf("write AppImage target: %v", err)
		}
		return target
	}
}
