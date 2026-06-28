package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestNewUninstallCmdExposesAllFlag(t *testing.T) {
	cmd := newUninstallCmd()

	if f := cmd.Flags().Lookup("all"); f == nil {
		t.Fatal("expected --all flag")
	}
}

func TestRunUninstallPreservesDataByDefault(t *testing.T) {
	appHome, dataFile, csghubBin, llamaBin, llamaLibs := setupUninstallTestEnv(t)

	if err := runUninstall(true, false); err != nil {
		t.Fatalf("runUninstall returned error: %v", err)
	}

	assertFileMissing(t, csghubBin)
	assertFileMissing(t, llamaBin)
	for _, lib := range llamaLibs {
		assertFileMissing(t, lib)
	}
	assertFileExists(t, appHome)
	assertFileExists(t, dataFile)
}

func TestRunUninstallStopsBackgroundServiceBeforeRemovingFiles(t *testing.T) {
	_, _, csghubBin, llamaBin, llamaLibs := setupUninstallTestEnv(t)

	restore := stubStopBackgroundServiceForUninstall(func() error {
		assertFileExists(t, csghubBin)
		assertFileExists(t, llamaBin)
		for _, lib := range llamaLibs {
			assertFileExists(t, lib)
		}
		return nil
	})
	defer restore()

	if err := runUninstall(true, false); err != nil {
		t.Fatalf("runUninstall returned error: %v", err)
	}
}

func TestRunUninstallDoesNotRemoveFilesWhenStopFails(t *testing.T) {
	appHome, dataFile, csghubBin, llamaBin, llamaLibs := setupUninstallTestEnv(t)

	restore := stubStopBackgroundServiceForUninstall(func() error {
		return errors.New("boom")
	})
	defer restore()

	err := runUninstall(true, false)
	if err == nil {
		t.Fatal("expected runUninstall to fail when stopping background service fails")
	}

	assertFileExists(t, appHome)
	assertFileExists(t, dataFile)
	assertFileExists(t, csghubBin)
	assertFileExists(t, llamaBin)
	for _, lib := range llamaLibs {
		assertFileExists(t, lib)
	}
}

func TestRunUninstallAllRemovesData(t *testing.T) {
	appHome, _, csghubBin, llamaBin, llamaLibs := setupUninstallTestEnv(t)

	if err := runUninstall(true, true); err != nil {
		t.Fatalf("runUninstall returned error: %v", err)
	}

	assertFileMissing(t, csghubBin)
	assertFileMissing(t, llamaBin)
	for _, lib := range llamaLibs {
		assertFileMissing(t, lib)
	}
	assertFileMissing(t, appHome)
}

func setupUninstallTestEnv(t *testing.T) (appHome, dataFile, csghubBin, llamaBin string, llamaLibs []string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	csghubBin = filepath.Join(binDir, platformBinaryName("csghub-lite"))
	writeExecutable(t, csghubBin)
	llamaBin = filepath.Join(binDir, platformBinaryName("llama-server"))
	writeExecutable(t, llamaBin)

	llamaLibs = []string{
		filepath.Join(binDir, "libggml-hip.so"),
		filepath.Join(binDir, "libllama.so"),
	}
	for _, lib := range llamaLibs {
		if err := os.WriteFile(lib, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write lib %s: %v", lib, err)
		}
	}

	var err error
	appHome, err = config.AppHome()
	if err != nil {
		t.Fatalf("AppHome() error: %v", err)
	}
	dataFile = filepath.Join(appHome, "models", "test", "model.gguf")
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(dataFile, []byte("model"), 0o644); err != nil {
		t.Fatalf("write data file: %v", err)
	}

	return appHome, dataFile, csghubBin, llamaBin, llamaLibs
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func platformBinaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, got err=%v", path, err)
	}
}

func stubStopBackgroundServiceForUninstall(fn func() error) func() {
	prev := stopBackgroundServiceForUninstall
	stopBackgroundServiceForUninstall = fn
	return func() {
		stopBackgroundServiceForUninstall = prev
	}
}
