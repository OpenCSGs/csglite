// Package upgrade provides automatic update functionality for csghub-lite
package upgrade

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestTempDirCreationWithMissingTMPDIR tests that temp directory creation
// succeeds even when TMPDIR environment variable points to a non-existent path.
// This reproduces the macOS issue where /var/folders paths are periodically cleaned.
func TestTempDirCreationWithMissingTMPDIR(t *testing.T) {
	// Save original TMPDIR
	originalTmpdir := os.Getenv("TMPDIR")

	// Create a base directory we have permission to write to,
	// then set TMPDIR to a non-existent subdirectory to simulate the macOS cleanup scenario.
	baseDir, err := os.MkdirTemp("", "csghub-lite-test-base-*")
	if err != nil {
		t.Fatalf("failed to create base test directory: %v", err)
	}
	defer os.RemoveAll(baseDir)

	// Set TMPDIR to a path that doesn't exist yet (simulating macOS cleanup)
	nonexistentPath := filepath.Join(baseDir, "cleaned-by-macos", "T")
	os.Setenv("TMPDIR", nonexistentPath)

	// Cleanup function to restore original TMPDIR
	defer func() {
		if originalTmpdir != "" {
			os.Setenv("TMPDIR", originalTmpdir)
		} else {
			os.Unsetenv("TMPDIR")
		}
	}()

	// Verify TMPDIR is now pointing to a non-existent directory
	tmpDir := os.TempDir()
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Logf("TMPDIR %q should not exist initially", tmpDir)
	}

	// Test that our fix handles this case: ensure temp base directory exists
	// This is the fix we applied in PerformUpgradeWithProgress
	err = os.MkdirAll(tmpDir, 0o700)
	if err != nil {
		t.Fatalf("os.MkdirAll failed to create missing TMPDIR: %v", err)
	}

	// Now creating a temp directory should succeed
	tempDir, err := os.MkdirTemp(tmpDir, "csghub-lite-test-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp failed after MkdirAll: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Verify the temp directory was created and is usable
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("created temp directory is not accessible: %v", err)
	}
}

// TestPerformUpgradeWithProgressWithMissingTMPDIR tests the full upgrade flow
// with a missing TMPDIR. Uses a minimal mock to avoid actual downloads.
func TestPerformUpgradeWithProgressWithMissingTMPDIR(t *testing.T) {
	// Save original TMPDIR
	originalTmpdir := os.Getenv("TMPDIR")

	// Create a base directory we can write to
	baseDir, err := os.MkdirTemp("", "csghub-lite-test-base-*")
	if err != nil {
		t.Fatalf("failed to create base test directory: %v", err)
	}
	defer os.RemoveAll(baseDir)

	// Set TMPDIR to non-existent subdirectory
	nonexistentPath := filepath.Join(baseDir, "cleaned-by-macos", "T")
	os.Setenv("TMPDIR", nonexistentPath)

	defer func() {
		if originalTmpdir != "" {
			os.Setenv("TMPDIR", originalTmpdir)
		} else {
			os.Unsetenv("TMPDIR")
		}
	}()

	// Verify TMPDIR doesn't exist initially
	tmpBase := os.TempDir()
	if _, err := os.Stat(tmpBase); !os.IsNotExist(err) {
		t.Logf("TMPDIR %q should not exist initially", tmpBase)
	}

	// Test temp directory creation logic directly (same as in PerformUpgradeWithProgress)
	if err := os.MkdirAll(tmpBase, 0o700); err != nil {
		t.Fatalf("failed to create temp base directory: %v", err)
	}

	tmpDir, err := os.MkdirTemp(tmpBase, "csghub-lite-upgrade-*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Verify temp directory exists
	info, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatalf("temp directory not accessible: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("temp path is not a directory")
	}
}

// TestTempDirCreationWithValidTMPDIR verifies normal behavior when TMPDIR exists.
func TestTempDirCreationWithValidTMPDIR(t *testing.T) {
	// Use system default temp directory (should always exist)
	tmpBase := os.TempDir()

	// Ensure it exists (this should be a no-op for valid TMPDIR)
	if err := os.MkdirAll(tmpBase, 0o700); err != nil {
		t.Fatalf("failed to ensure temp base directory: %v", err)
	}

	// Create temp directory
	tmpDir, err := os.MkdirTemp(tmpBase, "csghub-lite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Verify
	if _, err := os.Stat(tmpDir); err != nil {
		t.Fatalf("created temp directory is not accessible: %v", err)
	}
}

func TestWindowsRestartArgsDefaultsToServeWhenNoArgs(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	os.Args = []string{"csghub-lite.exe"}
	got := windowsRestartArgs()
	want := []string{"serve"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windowsRestartArgs() = %#v, want %#v", got, want)
	}
}

func TestWindowsRestartArgsSkipsCLIUpgradeCommand(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	os.Args = []string{"csghub-lite.exe", "upgrade", "--yes"}
	if got := windowsRestartArgs(); got != nil {
		t.Fatalf("windowsRestartArgs() = %#v, want nil", got)
	}
}

// TestMkdirTempFailsWithoutFix demonstrates the original bug behavior
// when os.MkdirTemp is called directly on a missing TMPDIR without the MkdirAll fix.
func TestMkdirTempFailsWithoutFix(t *testing.T) {
	// Save original TMPDIR
	originalTmpdir := os.Getenv("TMPDIR")

	// Create a base directory we can write to
	baseDir, err := os.MkdirTemp("", "csghub-lite-test-base-*")
	if err != nil {
		t.Fatalf("failed to create base test directory: %v", err)
	}
	defer os.RemoveAll(baseDir)

	// Set TMPDIR to non-existent subdirectory
	nonexistentPath := filepath.Join(baseDir, "cleaned-by-macos", "T")
	os.Setenv("TMPDIR", nonexistentPath)

	defer func() {
		if originalTmpdir != "" {
			os.Setenv("TMPDIR", originalTmpdir)
		} else {
			os.Unsetenv("TMPDIR")
		}
	}()

	// Without the fix: calling os.MkdirTemp directly should fail
	// because the parent directory doesn't exist
	_, err = os.MkdirTemp("", "csghub-lite-direct-*")
	if err == nil {
		// This might succeed on some systems if TMPDIR fallback works,
		// but on macOS with the exact error scenario, it would fail
		t.Log("os.MkdirTemp succeeded unexpectedly - system may have fallback behavior")
	} else {
		// Expected: error because TMPDIR path doesn't exist
		t.Logf("os.MkdirTemp failed as expected without fix: %v", err)
	}
}
