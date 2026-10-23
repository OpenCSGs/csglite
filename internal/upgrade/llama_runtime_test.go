package upgrade

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/scripts"
)

func TestManagedLlamaRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR", "")

	exePath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	custom := t.TempDir()
	root := string(filepath.Separator)

	tests := []struct {
		name   string
		path   string
		envDir string
		want   bool
	}{
		{
			name: "user local bin",
			path: filepath.Join(home, ".local", "bin", "llama-server"),
			want: true,
		},
		{
			name: "user bin",
			path: filepath.Join(home, "bin", "llama-server"),
			want: true,
		},
		{
			name: "next to the csghub-lite binary",
			path: filepath.Join(filepath.Dir(exePath), "llama-server"),
			want: true,
		},
		{
			name:   "explicit install dir",
			path:   filepath.Join(custom, "llama-server"),
			envDir: custom,
			want:   true,
		},
		{
			name: "homebrew keg",
			path: filepath.Join(root, "opt", "homebrew", "Cellar", "llama.cpp", "b11541", "bin", "llama-server"),
			want: false,
		},
		{
			name: "desktop app runtime",
			path: filepath.Join(root, "Applications", "CSGLite.app", "Contents", "Resources", "runtime", "llama-server"),
			want: false,
		},
		{
			name: "user's own build",
			path: filepath.Join(root, "opt", "llama.cpp", "build", "bin", "llama-server"),
			want: false,
		},
		{
			name: "empty path",
			path: "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR", tt.envDir)
			if got := managedLlamaRuntime(tt.path); got != tt.want {
				t.Fatalf("managedLlamaRuntime(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestPinnedLlamaBuildIDMatchesBundledRef(t *testing.T) {
	ref := strings.TrimSpace(convert.BundledConverterLLamacppRef)
	want, err := strconv.Atoi(strings.TrimPrefix(ref, "b"))
	if err != nil {
		t.Fatalf("bundled llama.cpp ref %q is not a numeric b-tag: %v", ref, err)
	}
	if got := pinnedLlamaBuildID(); got != want {
		t.Fatalf("pinnedLlamaBuildID() = %d, want %d (from %q)", got, want, ref)
	}
}

func TestLlamaInstallEnv(t *testing.T) {
	dir := t.TempDir()
	tempDir := t.TempDir()
	originalPath := strings.Join([]string{filepath.Join(string(filepath.Separator), "usr", "bin"), filepath.Join(string(filepath.Separator), "bin")}, string(os.PathListSeparator))
	t.Setenv("PATH", originalPath)

	env := llamaInstallEnv(dir, tempDir)

	for _, want := range []string{
		"CSGHUB_LITE_LLAMA_CPP_TAG=" + convert.BundledConverterLLamacppRef,
		"CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR=" + dir,
		"TMPDIR=" + tempDir,
		"TMP=" + tempDir,
		"TEMP=" + tempDir,
		// The installer runs with no terminal, so it must not escalate.
		"CSGHUB_LITE_NONINTERACTIVE=1",
		"CSGHUB_LITE_AUTO_INSTALL_PATCHELF=0",
		"CSGHUB_LITE_AUTO_INSTALL_CUDA_LIBS=0",
	} {
		if !slices.Contains(env, want) {
			t.Fatalf("llamaInstallEnv() is missing %q", want)
		}
	}

	path := envValue(env, "PATH")
	wantPath := dir + string(os.PathListSeparator) + originalPath
	if path != wantPath {
		t.Fatalf("llamaInstallEnv() PATH = %q, want %q", path, wantPath)
	}

	pathEntries := 0
	for _, kv := range env {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, "PATH") {
			pathEntries++
		}
	}
	if pathEntries != 1 {
		t.Fatalf("llamaInstallEnv() has %d PATH entries, want 1", pathEntries)
	}
}

// TestLlamaInstallCommandRequestsEngineOnly guards the other half of the
// contract: the installer only skips the csghub-lite download when the caller
// passes the flag.
func TestLlamaInstallCommandRequestsEngineOnly(t *testing.T) {
	want := "--engine-only"
	if runtime.GOOS == "windows" {
		want = "-EngineOnly"
	}

	cmd, err := llamaInstallCommand(context.Background(), filepath.Join(t.TempDir(), "install.sh"))
	if err != nil {
		t.Fatalf("llamaInstallCommand() = %v", err)
	}
	if !slices.Contains(cmd.Args, want) {
		t.Fatalf("llamaInstallCommand() args = %v, want %q", cmd.Args, want)
	}
}

// TestEmbeddedInstallersSupportEngineOnlyMode guards the contract between
// AlignLlamaRuntime and the scripts it runs: without the engine-only flag the
// installer would also download and replace the csghub-lite binary and restart
// the service instead of only aligning llama-server.
func TestEmbeddedInstallersSupportEngineOnlyMode(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		markers []string
	}{
		{
			name:    "install.sh",
			script:  scripts.InstallSh,
			markers: []string{"--engine-only", "CSGHUB_LITE_NONINTERACTIVE"},
		},
		{
			name:    "install.ps1",
			script:  scripts.InstallPs1,
			markers: []string{"-EngineOnly"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.TrimSpace(tt.script) == "" {
				t.Fatalf("embedded %s is empty", tt.name)
			}
			for _, marker := range tt.markers {
				if !strings.Contains(tt.script, marker) {
					t.Fatalf("embedded %s is missing %q", tt.name, marker)
				}
			}
		})
	}
}

func TestAlignLlamaRuntimeSkipsUnmanagedRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR", "")

	// A copy outside the directories csghub-lite installs into: the user's own
	// build has to keep its version.
	own := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(own, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("writing fake llama-server: %v", err)
	}
	t.Setenv("CSGHUB_LITE_LLAMA_SERVER", own)

	tempDir := t.TempDir()
	if err := AlignLlamaRuntime(context.Background(), tempDir); err != nil {
		t.Fatalf("AlignLlamaRuntime() = %v, want nil", err)
	}
	assertEmptyDir(t, tempDir)
}

func TestAlignLlamaRuntimeRespectsAutoInstallOptOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// A managed copy that already reports the pinned build. Without the opt-out
	// check AlignLlamaRuntime returns silently here, so the log line is the only
	// observable difference: dropping the check fails this test rather than
	// starting a real installer run.
	dir := t.TempDir()
	binary := filepath.Join(dir, "llama-server")
	writeFakeLlamaServer(t, binary, fmt.Sprintf("version: 0.6.0-dev (build %d, commit aaaaaaa)", pinnedLlamaBuildID()))
	t.Setenv("CSGHUB_LITE_LLAMA_SERVER", binary)
	t.Setenv("CSGHUB_LITE_LLAMA_SERVER_INSTALL_DIR", dir)
	t.Setenv("CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER", "0")

	logs := captureLogs(t)

	tempDir := t.TempDir()
	if err := AlignLlamaRuntime(context.Background(), tempDir); err != nil {
		t.Fatalf("AlignLlamaRuntime() = %v, want nil", err)
	}
	if !strings.Contains(logs.String(), "CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER=0") {
		t.Fatalf("AlignLlamaRuntime() did not report the opt-out, logs:\n%s", logs.String())
	}
	assertEmptyDir(t, tempDir)
}

func TestCanReplaceLlamaServer(t *testing.T) {
	dir := t.TempDir()
	if !canReplaceLlamaServer(dir) {
		t.Fatalf("canReplaceLlamaServer(%q) = false, want true", dir)
	}
	assertEmptyDir(t, dir)

	missing := filepath.Join(t.TempDir(), "missing")
	if canReplaceLlamaServer(missing) {
		t.Fatalf("canReplaceLlamaServer(%q) = true for a directory that does not exist", missing)
	}
}

func writeFakeLlamaServer(t *testing.T, path, versionLine string) {
	t.Helper()

	// Never executed by these tests: every case stops before the installer runs.
	script := "#!/bin/sh\nprintf '%s\\n' '" + versionLine + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("writing fake llama-server: %v", err)
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	previous := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	return buf
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) == 0 {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	t.Fatalf("expected no work in %s, found %v", dir, names)
}
