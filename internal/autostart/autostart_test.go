package autostart

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnableDisableRoundTrip(t *testing.T) {
	switch runtime.GOOS {
	case "linux":
		testLinuxRoundTrip(t)
	case "darwin":
		testDarwinRoundTrip(t)
	default:
		t.Skipf("autostart round-trip test not implemented for %s", runtime.GOOS)
	}
}

func testLinuxRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	enabled, err := IsEnabled()
	if err != nil {
		t.Fatalf("IsEnabled: %v", err)
	}
	if enabled {
		t.Fatal("expected autostart to be disabled initially")
	}

	if err := Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	enabled, err = IsEnabled()
	if err != nil {
		t.Fatalf("IsEnabled after Enable: %v", err)
	}
	if !enabled {
		t.Fatal("expected autostart to be enabled after Enable()")
	}

	// Keep the expected file name local to the test so the package still
	// compiles on non-Linux hosts where desktopFileName is not defined.
	path := filepath.Join(tmp, "autostart", "csghub-lite.desktop")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading desktop file: %v", err)
	}
	content := string(data)
	if len(content) == 0 {
		t.Fatal("desktop file is empty")
	}

	if err := Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	enabled, err = IsEnabled()
	if err != nil {
		t.Fatalf("IsEnabled after Disable: %v", err)
	}
	if enabled {
		t.Fatal("expected autostart to be disabled after Disable()")
	}
}

func testDarwinRoundTrip(t *testing.T) {
	// On macOS, the plist goes into ~/Library/LaunchAgents which is a real
	// system directory. Only run this test when explicitly opted in.
	if os.Getenv("CSGHUB_TEST_AUTOSTART") == "" {
		t.Skip("skipping macOS autostart test; set CSGHUB_TEST_AUTOSTART=1 to run")
	}

	enabled, err := IsEnabled()
	if err != nil {
		t.Fatalf("IsEnabled: %v", err)
	}
	if enabled {
		t.Skip("autostart is already enabled on this machine; skipping to avoid side-effects")
	}

	if err := Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	defer func() { _ = Disable() }()

	enabled, err = IsEnabled()
	if err != nil {
		t.Fatalf("IsEnabled after Enable: %v", err)
	}
	if !enabled {
		t.Fatal("expected autostart to be enabled")
	}

	if err := Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	enabled, err = IsEnabled()
	if err != nil {
		t.Fatalf("IsEnabled after Disable: %v", err)
	}
	if enabled {
		t.Fatal("expected autostart to be disabled")
	}
}

func TestDisableWhenNotEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}

	// Disable should be a no-op when not enabled.
	if err := Disable(); err != nil {
		t.Fatalf("Disable when not enabled: %v", err)
	}
}
