package apps

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallDetectProfilesCoverSupportedApps(t *testing.T) {
	for _, spec := range appSpecs() {
		if !spec.supported || spec.installMode != "script" {
			continue
		}
		if _, ok := installDetectProfiles[spec.id]; !ok {
			t.Fatalf("app %q is missing installDetectProfiles entry", spec.id)
		}
	}
}

func TestDetectCLIAppBinaryFromVersionedShare(t *testing.T) {
	home := setTempHome(t)
	t.Setenv("PATH", "")

	runtimeDir := filepath.Join(home, ".local", "share", "codex", "versions", "1.2.3")
	binaryPath := writeFakeBinary(t, runtimeDir, "codex")

	got, ok := detectCLIAppBinary("codex", installDetectProfile{versionedShare: "codex"})
	if !ok {
		t.Fatal("expected codex binary in versioned share to be detected")
	}
	if got != binaryPath {
		t.Fatalf("detectCLIAppBinary() = %q, want %q", got, binaryPath)
	}
}

func TestDetectCLIAppBinaryFromShareBinDir(t *testing.T) {
	home := setTempHome(t)
	t.Setenv("PATH", "")

	binDir := filepath.Join(home, ".local", "share", "pi-coding-agent", "bin")
	binaryPath := writeFakeBinary(t, binDir, "pi")

	got, ok := detectCLIAppBinary("pi", installDetectProfile{shareBinRel: "pi-coding-agent/bin"})
	if !ok {
		t.Fatal("expected pi binary in share bin dir to be detected")
	}
	if got != binaryPath {
		t.Fatalf("detectCLIAppBinary() = %q, want %q", got, binaryPath)
	}
}

func TestDetectCLIAppBinaryFromLibBundle(t *testing.T) {
	home := setTempHome(t)
	t.Setenv("PATH", "")

	bundleDir := filepath.Join(home, ".local", "lib", "csgclaw", "v0.2.8", "csgclaw", "bin")
	binaryPath := writeFakeBinary(t, bundleDir, "csgclaw")

	got, ok := detectCLIAppBinary("csgclaw", installDetectProfile{libBundleName: "csgclaw"})
	if !ok {
		t.Fatal("expected csgclaw binary in lib bundle to be detected")
	}
	if got != binaryPath {
		t.Fatalf("detectCLIAppBinary() = %q, want %q", got, binaryPath)
	}
}

func TestDetectCSGClawDesktopFromManagedLaunchTarget(t *testing.T) {
	home := setTempHome(t)
	root := csgclawDesktopRuntimeRoot(home)
	target := filepath.Join(root, "versions", "0.4.6", csgclawAppBundleName)
	if runtime.GOOS == "windows" {
		target = filepath.Join(root, "versions", "0.4.6", "CSGClaw.exe")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir target dir: %v", err)
		}
		if err := os.WriteFile(target, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write target: %v", err)
		}
	} else if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir app bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "launch-target"), []byte(target+"\n"), 0o644); err != nil {
		t.Fatalf("write launch target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "version"), []byte("0.4.6\n"), 0o644); err != nil {
		t.Fatalf("write version: %v", err)
	}

	installPath, version, ok := detectCSGClawDesktopInstall()
	if !ok {
		t.Fatal("expected managed CSGClaw Desktop target to be detected")
	}
	if installPath != target || version != "0.4.6" {
		t.Fatalf("detected (%q, %q), want (%q, 0.4.6)", installPath, version, target)
	}
}

func TestDetectCodexAppInstallFromLaunchTargetWithoutLauncher(t *testing.T) {
	home := setTempHome(t)
	runtimeRoot := codexAppRuntimeRoot(home)

	target := filepath.Join(runtimeRoot, "versions", "26.616.31447", codexAppBundleName)
	if runtime.GOOS == "windows" {
		target = filepath.Join(runtimeRoot, "versions", "26.616.31447", "codex.exe")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir exe dir: %v", err)
		}
		if err := os.WriteFile(target, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write exe stub: %v", err)
		}
	} else if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir app bundle: %v", err)
	}

	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		t.Fatalf("mkdir runtime root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "launch-target"), []byte(target+"\n"), 0o644); err != nil {
		t.Fatalf("write launch target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "version"), []byte("26.616.31447\n"), 0o644); err != nil {
		t.Fatalf("write version: %v", err)
	}

	installPath, version, ok := detectCodexAppInstall()
	if !ok {
		t.Fatal("expected managed launch target without launcher to be detected")
	}
	if installPath != target {
		t.Fatalf("install path = %q, want %q", installPath, target)
	}
	if version != "26.616.31447" {
		t.Fatalf("version = %q, want 26.616.31447", version)
	}
}

func TestExistingCodexAppLauncherPrefersWindowsCmd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows launcher names")
	}

	home := setTempHome(t)
	launcherDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(launcherDir, 0o755); err != nil {
		t.Fatalf("mkdir launcher dir: %v", err)
	}
	cmdPath := filepath.Join(launcherDir, "codex-app.cmd")
	if err := os.WriteFile(cmdPath, []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatalf("write cmd launcher: %v", err)
	}

	got, ok := existingCodexAppLauncher(home)
	if !ok {
		t.Fatal("expected codex-app.cmd launcher to be detected")
	}
	if got != cmdPath {
		t.Fatalf("existingCodexAppLauncher() = %q, want %q", got, cmdPath)
	}
}
