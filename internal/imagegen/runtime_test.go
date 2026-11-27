package imagegen

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/model"
)

func TestTorchIndexURL(t *testing.T) {
	t.Setenv(mirrorModeEnv, "official")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")
	tests := []struct {
		hw   HardwareKind
		want string
	}{
		{HardwareCUDA, aliyunTorchRoot + "/cu128"},
		{HardwareROCm, aliyunTorchRoot + "/rocm7.1"},
		{HardwareMPS, ""},
		{HardwareCPU, aliyunTorchRoot + "/cpu"},
	}
	for _, tt := range tests {
		if got := TorchIndexURL(tt.hw); got != tt.want {
			t.Fatalf("TorchIndexURL(%q) = %q, want %q", tt.hw, got, tt.want)
		}
	}
}

func TestResolvePackageIndexesAliyun(t *testing.T) {
	t.Setenv(mirrorModeEnv, "aliyun")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")

	tests := []struct {
		hw            HardwareKind
		wantTorchLink string
		wantPyPI      string
	}{
		{HardwareCUDA, "https://mirrors.aliyun.com/pytorch-wheels/cu128", "https://mirrors.aliyun.com/pypi/simple"},
		{HardwareROCm, "https://mirrors.aliyun.com/pytorch-wheels/rocm7.1", "https://mirrors.aliyun.com/pypi/simple"},
		{HardwareCPU, "https://mirrors.aliyun.com/pytorch-wheels/cpu", "https://mirrors.aliyun.com/pypi/simple"},
		{HardwareMPS, "", "https://mirrors.aliyun.com/pypi/simple"},
	}
	for _, tt := range tests {
		got := ResolvePackageIndexes(tt.hw)
		if got.Mirror != PackageMirrorAliyun || got.TorchFindLinksURL != tt.wantTorchLink || got.PyPIIndexURL != tt.wantPyPI {
			t.Fatalf("ResolvePackageIndexes(%q) = %#v, want torch links %q pypi %q", tt.hw, got, tt.wantTorchLink, tt.wantPyPI)
		}
		if got.TorchIndexURL != "" {
			t.Fatalf("ResolvePackageIndexes(%q) torch index = %q, want empty for Aliyun find-links install", tt.hw, got.TorchIndexURL)
		}
	}
}

func TestResolvePackageIndexesDefaultsToOfficialOutsideChina(t *testing.T) {
	t.Setenv(mirrorModeEnv, "")
	t.Setenv(regionEnv, "")
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_MESSAGES", "C")
	t.Setenv("LC_CTYPE", "C")
	t.Setenv("LANG", "C")
	t.Setenv("TZ", "UTC")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")

	got := ResolvePackageIndexes(HardwareCUDA)
	if got.Mirror != PackageMirrorOfficial {
		t.Fatalf("default package mirror = %q, want %q", got.Mirror, PackageMirrorOfficial)
	}
	if got.TorchIndexURL != "https://download.pytorch.org/whl/cu128" {
		t.Fatalf("default CUDA torch index = %q", got.TorchIndexURL)
	}
	if got.TorchFindLinksURL != "" {
		t.Fatalf("default CUDA torch find-links = %q, want empty", got.TorchFindLinksURL)
	}
	if got.PyPIIndexURL != "" {
		t.Fatalf("default PyPI index = %q, want empty", got.PyPIIndexURL)
	}
}

func TestResolvePackageIndexesUsesTsinghuaInChina(t *testing.T) {
	t.Setenv(mirrorModeEnv, "")
	t.Setenv(regionEnv, "cn")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")

	got := ResolvePackageIndexes(HardwareCUDA)
	if got.Mirror != PackageMirrorTsinghua {
		t.Fatalf("package mirror = %q, want %q", got.Mirror, PackageMirrorTsinghua)
	}
	if got.PyPIIndexURL != tsinghuaPyPIIndex {
		t.Fatalf("Tsinghua PyPI index = %q, want %q", got.PyPIIndexURL, tsinghuaPyPIIndex)
	}
	torchIndexes := torchInstallIndexes(HardwareCUDA)
	if torchIndexes.Mirror != PackageMirrorAliyun || torchIndexes.PyPIIndexURL != aliyunPyPIIndex || torchIndexes.TorchFindLinksURL != aliyunTorchRoot+"/cu128" {
		t.Fatalf("torch indexes = %#v, want Aliyun CUDA wheels", torchIndexes)
	}
	if mlxPackageIndexes().PyPIIndexURL != tsinghuaPyPIIndex {
		t.Fatalf("MLX index = %q, want %q", mlxPackageIndexes().PyPIIndexURL, tsinghuaPyPIIndex)
	}
}

func TestTorchInstallIgnoresGeneralMirror(t *testing.T) {
	t.Setenv(mirrorModeEnv, "official")
	t.Setenv(pypiIndexOverrideEnv, tsinghuaPyPIIndex)
	t.Setenv(torchIndexOverrideEnv, "")

	got := torchInstallIndexes(HardwareROCm)
	if got.PyPIIndexURL != aliyunPyPIIndex || got.TorchFindLinksURL != aliyunTorchRoot+"/rocm7.1" || got.TorchIndexURL != "" {
		t.Fatalf("torch indexes = %#v, want Aliyun ROCm wheels", got)
	}

	t.Setenv(torchIndexOverrideEnv, "https://example.test/torch")
	got = torchInstallIndexes(HardwareCUDA)
	if got.TorchIndexURL != "https://example.test/torch" || got.TorchFindLinksURL != "" || got.PyPIIndexURL != "" {
		t.Fatalf("torch override indexes = %#v", got)
	}
}

func TestResolvePackageIndexesHonorsInternationalRegion(t *testing.T) {
	t.Setenv(mirrorModeEnv, "")
	t.Setenv(regionEnv, "INTL")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")

	got := ResolvePackageIndexes(HardwareCUDA)
	if got.Mirror != PackageMirrorOfficial {
		t.Fatalf("package mirror = %q, want %q", got.Mirror, PackageMirrorOfficial)
	}
	if got.TorchIndexURL != "https://download.pytorch.org/whl/cu128" {
		t.Fatalf("official CUDA torch index = %q", got.TorchIndexURL)
	}
	if got.PyPIIndexURL != "" {
		t.Fatalf("official PyPI index = %q, want empty", got.PyPIIndexURL)
	}
}

func TestAliyunCUDAUsesUnpinnedTorchPackages(t *testing.T) {
	t.Setenv(mirrorModeEnv, "aliyun")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")

	manager := NewRuntimeManagerAt(t.TempDir())
	cmd := manager.InstallCommand(HardwareCUDA)

	for _, want := range torchPackages {
		if !hasString(cmd, want) {
			t.Fatalf("InstallCommand(CUDA) missing %q in %#v", want, cmd)
		}
	}
	for _, value := range cmd {
		if hasTorchVersionPin(value) {
			t.Fatalf("InstallCommand(CUDA) should not pin PyTorch package versions: %#v", cmd)
		}
	}
	if !hasString(cmd, "--find-links") {
		t.Fatalf("InstallCommand(CUDA) should use Aliyun wheel links: %#v", cmd)
	}
	if !hasString(cmd, "uv") {
		t.Fatalf("InstallCommand(CUDA) should install packages with uv: %#v", cmd)
	}
}

func TestOfficialCUDAUsesUnpinnedTorchPackages(t *testing.T) {
	t.Setenv(mirrorModeEnv, "official")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")

	got := torchPackageSpecs(HardwareCUDA, ResolvePackageIndexes(HardwareCUDA))
	if len(got) != len(torchPackages) {
		t.Fatalf("official CUDA packages = %#v, want %#v", got, torchPackages)
	}
	for i := range got {
		if got[i] != torchPackages[i] {
			t.Fatalf("official CUDA packages = %#v, want %#v", got, torchPackages)
		}
		if hasTorchVersionPin(got[i]) {
			t.Fatalf("official CUDA packages should not pin versions: %#v", got)
		}
	}
}

func TestRuntimeStatusIsLazyAndDoesNotInstall(t *testing.T) {
	manager := NewRuntimeManagerAt(t.TempDir())
	status := manager.Status(context.Background())
	if status.Ready {
		t.Fatalf("fresh runtime should not be ready")
	}
	if status.RuntimeDir == "" || status.VenvDir == "" {
		t.Fatalf("status missing runtime paths: %#v", status)
	}
	if len(status.InstallCommand) == 0 {
		t.Fatalf("status should include an install command hint")
	}
	if hasString(status.InstallCommand, "funasr") {
		t.Fatalf("image runtime status should not expose ASR install command: %#v", status.InstallCommand)
	}
	if !hasString(status.InstallCommand, "diffusers>=0.34.0") {
		t.Fatalf("image runtime status should include diffusers install command: %#v", status.InstallCommand)
	}
}

func TestRuntimeManagersUseSeparateRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	imageRuntime, err := NewRuntimeManager()
	if err != nil {
		t.Fatal(err)
	}
	asrRuntime, err := NewASRRuntimeManager()
	if err != nil {
		t.Fatal(err)
	}

	wantImage := filepath.Join(home, config.AppDir, runtimeDirName)
	wantASR := filepath.Join(home, config.AppDir, asrRuntimeDirName)
	if imageRuntime.RootDir() != wantImage {
		t.Fatalf("image runtime root = %q, want %q", imageRuntime.RootDir(), wantImage)
	}
	if asrRuntime.RootDir() != wantASR {
		t.Fatalf("ASR runtime root = %q, want %q", asrRuntime.RootDir(), wantASR)
	}
}

func TestInstallSubprocessEnvKeepsTempUnderStorageRoot(t *testing.T) {
	root := t.TempDir()
	imageRuntime := NewRuntimeManagerAt(filepath.Join(root, runtimeDirName))
	asrRuntime := NewRuntimeManagerAt(filepath.Join(root, asrRuntimeDirName))

	for _, m := range []*RuntimeManager{imageRuntime, asrRuntime} {
		env := m.installSubprocessEnv()
		want := map[string]string{
			"UV_CACHE_DIR": filepath.Join(root, uvCacheDirName),
			"TMPDIR":       filepath.Join(root, tmpDirName),
			"TMP":          filepath.Join(root, tmpDirName),
			"TEMP":         filepath.Join(root, tmpDirName),
		}
		if len(env) != len(want) {
			t.Fatalf("install subprocess env = %#v, want %d entries", env, len(want))
		}
		for _, entry := range env {
			key, value, _ := strings.Cut(entry, "=")
			if want[key] != value {
				t.Fatalf("install subprocess env %s = %q, want %q", key, value, want[key])
			}
			delete(want, key)
		}
		if len(want) != 0 {
			t.Fatalf("install subprocess env missing entries: %v", want)
		}
		if _, err := os.Stat(filepath.Join(root, tmpDirName)); err != nil {
			t.Fatalf("temp dir not created under the storage root: %v", err)
		}
	}
}

func TestVerifyPythonScriptWithFakePython(t *testing.T) {
	ctx := context.Background()
	if err := verifyPythonScript(ctx, fakePython(t, 0, ""), "import torch"); err != nil {
		t.Fatalf("verifyPythonScript success returned error: %v", err)
	}

	err := verifyPythonScript(ctx, fakePython(t, 1, strings.Repeat("x", 2200)+"libtorchaudio.pyd failed"), "import torch")
	if err == nil {
		t.Fatal("verifyPythonScript failure returned nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "libtorchaudio.pyd failed") {
		t.Fatalf("verifyPythonScript error missing output tail: %q", msg)
	}
	if len(msg) > len("python import check failed: ")+2048 {
		t.Fatalf("verifyPythonScript error was not truncated: length=%d", len(msg))
	}
}

func writeFakePythonAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallBrokenWindowsTorchaudio(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake python")
	}
	ctx := context.Background()
	m := NewRuntimeManagerAt(t.TempDir())
	marker := filepath.Join(t.TempDir(), "uninstalled")
	t.Setenv("TEST_UNINSTALL_MARKER", marker)

	brokenPython := filepath.Join(t.TempDir(), "python")
	writeFakePythonAt(t, brokenPython, `case "$*" in
  *importlib.util*) echo '[]' ;;
  *"import torchaudio"*) exit 1 ;;
  *"pip uninstall"*) : > "$TEST_UNINSTALL_MARKER" ;;
esac
exit 0
`)
	if err := m.uninstallBrokenWindowsTorchaudio(ctx, brokenPython); err != nil {
		t.Fatalf("uninstallBrokenWindowsTorchaudio error = %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("broken torchaudio should have been uninstalled via pip")
	}

	// Healthy torchaudio must be left alone.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	healthyPython := filepath.Join(t.TempDir(), "python")
	writeFakePythonAt(t, healthyPython, `case "$*" in
  *importlib.util*) echo '[]' ;;
  *"pip uninstall"*) : > "$TEST_UNINSTALL_MARKER" ;;
esac
exit 0
`)
	if err := m.uninstallBrokenWindowsTorchaudio(ctx, healthyPython); err != nil {
		t.Fatalf("uninstallBrokenWindowsTorchaudio error = %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("healthy torchaudio should not be uninstalled")
	}

	// Missing torchaudio: nothing to probe or uninstall.
	missingPython := filepath.Join(t.TempDir(), "python")
	writeFakePythonAt(t, missingPython, `case "$*" in
  *importlib.util*) echo '["torchaudio"]' ;;
  *) : > "$TEST_UNINSTALL_MARKER" ;;
esac
exit 0
`)
	if err := m.uninstallBrokenWindowsTorchaudio(ctx, missingPython); err != nil {
		t.Fatalf("uninstallBrokenWindowsTorchaudio error = %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("missing torchaudio should be a no-op")
	}
}

func TestCandidateHostPythonsOrderNewestFirst(t *testing.T) {
	candidates := candidateHostPythons()
	wantHead := []string{"python3.14", "python3.13", "python3.12", "python3.11", "python3.10"}
	if len(candidates) < len(wantHead)+2 {
		t.Fatalf("candidateHostPythons() too short: %#v", candidates)
	}
	for i, want := range wantHead {
		if candidates[i] != want {
			t.Fatalf("candidateHostPythons()[%d] = %q, want %q (full: %#v)", i, candidates[i], want, candidates)
		}
	}
	if tail := candidates[len(candidates)-2:]; tail[0] != "python3" || tail[1] != "python" {
		t.Fatalf("candidateHostPythons() should end with python3, python: %#v", candidates)
	}
}

func TestParsePythonMinorAndSupport(t *testing.T) {
	tests := []struct {
		version   string
		minor     int
		ok        bool
		supported bool
	}{
		{"3.9.6", 9, true, false},
		{"3.10.0", 10, true, true},
		{"3.11", 11, true, true},
		{"3.13.7", 13, true, true},
		{"3.14.7", 14, true, true},
		{"3.15.0", 15, true, false},
		{"2.7.18", 0, false, false},
		{"garbage", 0, false, false},
	}
	for _, tt := range tests {
		minor, ok := parsePythonMinor(tt.version)
		if ok != tt.ok || (ok && minor != tt.minor) {
			t.Fatalf("parsePythonMinor(%q) = (%d, %v), want (%d, %v)", tt.version, minor, ok, tt.minor, tt.ok)
		}
		if got := hostPythonSupported(tt.version); got != tt.supported {
			t.Fatalf("hostPythonSupported(%q) = %v, want %v", tt.version, got, tt.supported)
		}
	}
}

// findHostPython must prefer the newest versioned interpreter on PATH even
// when the unversioned python3/python names resolve to something older, and
// must never pick an interpreter outside the supported range.
func TestFindHostPythonPrefersVersionedInterpreters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	writeFakePythonAt(t, filepath.Join(dir, "python3.13"), probeScriptOutput("3.13.1"))
	writeFakePythonAt(t, filepath.Join(dir, "python3.11"), probeScriptOutput("3.11.9"))
	writeFakePythonAt(t, filepath.Join(dir, "python3"), probeScriptOutput("3.9.6"))
	writeFakePythonAt(t, filepath.Join(dir, "python"), probeScriptOutput("3.9.6"))

	path, err := findHostPythonFrom(context.Background(), candidateHostPythons(), nil)
	if err != nil {
		t.Fatalf("findHostPython error = %v", err)
	}
	if path != filepath.Join(dir, "python3.13") {
		t.Fatalf("findHostPython = %q, want newest versioned interpreter %q", path, filepath.Join(dir, "python3.13"))
	}
}

func TestFindHostPythonFallsBackToOlderVersionedInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	writeFakePythonAt(t, filepath.Join(dir, "python3.11"), probeScriptOutput("3.11.9"))
	writeFakePythonAt(t, filepath.Join(dir, "python3"), probeScriptOutput("3.9.6"))
	writeFakePythonAt(t, filepath.Join(dir, "python"), probeScriptOutput("3.9.6"))

	path, err := findHostPythonFrom(context.Background(), candidateHostPythons(), nil)
	if err != nil {
		t.Fatalf("findHostPython error = %v", err)
	}
	if path != filepath.Join(dir, "python3.11") {
		t.Fatalf("findHostPython = %q, want %q", path, filepath.Join(dir, "python3.11"))
	}
}

func TestFindHostPythonRejectsOnlyUnsupportedPythons(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeFakePythonAt(t, filepath.Join(dir, "python3"), probeScriptOutput("3.9.6"))
	writeFakePythonAt(t, filepath.Join(dir, "python"), probeScriptOutput("3.9.6"))

	if path, err := findHostPythonFrom(context.Background(), candidateHostPythons(), nil); err == nil {
		t.Fatalf("findHostPython = %q with only 3.9 on PATH, want error", path)
	}
}

// findHostPython must fall back to well-known absolute interpreter
// locations when PATH is minimal (GUI apps get /usr/bin:/bin:... from
// launchd and never see Homebrew or python.org installs). The lists are
// injected so the test never touches real interpreter locations.
func TestFindHostPythonFallsBackToWellKnownPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses absolute unix paths")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir) // minimal PATH without any python

	wellKnown := filepath.Join(dir, "well-known", "python3.11")
	writeFakePythonAt(t, wellKnown, probeScriptOutput("3.11.9"))

	path, err := findHostPythonFrom(context.Background(), candidateHostPythons(), []string{wellKnown})
	if err != nil {
		t.Fatalf("findHostPython error = %v", err)
	}
	if path != wellKnown {
		t.Fatalf("findHostPython = %q, want well-known path %q", path, wellKnown)
	}

	// A well-known path that fails the probe is skipped, not fatal.
	writeFakePythonAt(t, wellKnown, "exit 1\n")
	if path, err := findHostPythonFrom(context.Background(), candidateHostPythons(), []string{wellKnown, filepath.Join(dir, "well-known", "python3.12")}); err == nil {
		t.Fatalf("findHostPython = %q with broken well-known python, want error", path)
	}
}

func TestWellKnownHostPythonPathsSkipUnsupportedVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("generates unix absolute paths")
	}
	paths := wellKnownHostPythonPaths()
	if len(paths) == 0 {
		t.Fatalf("wellKnownHostPythonPaths() empty on %s", runtime.GOOS)
	}
	for _, p := range paths {
		if strings.Contains(p, fmt.Sprintf("python3.%d", minimumHostPythonMinor-1)) {
			t.Fatalf("well-known paths must stay within the supported range: %q", p)
		}
	}
	// Newest first.
	if !strings.Contains(paths[0], fmt.Sprintf("python3.%d", maxHostPythonMinor)) {
		t.Fatalf("first well-known path = %q, want newest minor %d", paths[0], maxHostPythonMinor)
	}
}

func TestFindHostPythonHonorsOverrideEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	python := filepath.Join(dir, "custom-python")
	writeFakePythonAt(t, python, probeScriptOutput("3.12.4"))
	t.Setenv(hostPythonEnv, python)

	path, err := findHostPython()
	if err != nil {
		t.Fatalf("findHostPython error = %v", err)
	}
	if path != python {
		t.Fatalf("findHostPython = %q, want override %q", path, python)
	}

	writeFakePythonAt(t, python, probeScriptOutput("3.9.6"))
	if path, err := findHostPython(); err == nil {
		t.Fatalf("findHostPython = %q with unsupported override version, want error", path)
	}

	t.Setenv(hostPythonEnv, filepath.Join(dir, "missing-python"))
	if path, err := findHostPython(); err == nil {
		t.Fatalf("findHostPython = %q with missing override path, want error", path)
	}
}

// probeScriptOutput builds a fake python body mimicking the real probe in
// probePython: it prints sys.executable and the interpreter version.
func probeScriptOutput(version string) string {
	return fmt.Sprintf("printf '%%s\\n%%s\\n' \"$0\" %q\n", version)
}

func TestCheckVenvPythonMissingVenvUsesNewestHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeFakePythonAt(t, filepath.Join(dir, "python3.11"), probeScriptOutput("3.11.9"))

	m := NewRuntimeManagerAt(filepath.Join(dir, "asr-runtime"))
	recreate, hostPython, err := m.checkVenvPython(context.Background())
	if err != nil {
		t.Fatalf("checkVenvPython error = %v", err)
	}
	if !recreate {
		t.Fatal("missing venv should request recreation")
	}
	if hostPython != filepath.Join(dir, "python3.11") {
		t.Fatalf("hostPython = %q, want %q", hostPython, filepath.Join(dir, "python3.11"))
	}
}

func TestCheckVenvPythonKeepsFreshVenv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeFakePythonAt(t, filepath.Join(dir, "python3.11"), probeScriptOutput("3.11.9"))

	m := NewRuntimeManagerAt(filepath.Join(dir, "asr-runtime"))
	if err := os.MkdirAll(filepath.Dir(m.PythonPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakePythonAt(t, m.PythonPath(), probeScriptOutput("3.10.4"))

	recreate, _, err := m.checkVenvPython(context.Background())
	if err != nil {
		t.Fatalf("checkVenvPython error = %v", err)
	}
	if recreate {
		t.Fatal("healthy supported venv should not be recreated")
	}
}

func TestCheckVenvPythonDetectsStaleVenv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeFakePythonAt(t, filepath.Join(dir, "python3.11"), probeScriptOutput("3.11.9"))

	m := NewRuntimeManagerAt(filepath.Join(dir, "asr-runtime"))
	if err := os.MkdirAll(filepath.Dir(m.PythonPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakePythonAt(t, m.PythonPath(), probeScriptOutput("3.9.6"))

	recreate, hostPython, err := m.checkVenvPython(context.Background())
	if err != nil {
		t.Fatalf("checkVenvPython error = %v", err)
	}
	if !recreate {
		t.Fatal("3.9 venv should be flagged for recreation")
	}
	if hostPython != filepath.Join(dir, "python3.11") {
		t.Fatalf("hostPython = %q, want %q", hostPython, filepath.Join(dir, "python3.11"))
	}
}

func TestASRStatusDetectsStaleVenvVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeFakePythonAt(t, filepath.Join(dir, "python3.13"), probeScriptOutput("3.13.1"))

	m := NewRuntimeManagerAt(filepath.Join(dir, "asr-runtime"))
	if err := os.MkdirAll(filepath.Dir(m.PythonPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakePythonAt(t, m.PythonPath(), probeScriptOutput("3.9.6"))

	status := m.ASRStatus(context.Background())
	if status.Ready {
		t.Fatal("stale 3.9.6 venv should not be ready")
	}
	if status.Error == "" {
		t.Fatal("stale venv should produce an error message")
	}
	if !strings.Contains(status.Error, "3.9.6") {
		t.Fatalf("status.Error = %q, want mention of 3.9.6", status.Error)
	}
	if !strings.Contains(status.Error, pythonVersionRangeHint) {
		t.Fatalf("status.Error = %q, want mention of %s", status.Error, pythonVersionRangeHint)
	}
}

func TestEnsureVenvForInstallRecreatesStaleVenv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	hostPython := filepath.Join(dir, "python3.11")
	writeFakePythonAt(t, hostPython, probeScriptOutput("3.11.9"))

	// The stale venv python must fail the probe so the fresh-venv shortcut
	// cannot apply; it mimics a 3.9 interpreter that cannot report versions
	// correctly after its framework was removed.
	m := NewRuntimeManagerAt(filepath.Join(dir, "asr-runtime"))
	if err := os.MkdirAll(filepath.Dir(m.PythonPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakePythonAt(t, m.PythonPath(), "exit 1\n")

	// The fake host answers the version probe normally, and treats
	// `-m venv <target>` as creating the venv (touching the directory).
	// Note: PATH points at the fake dir, so use builtin-safe commands only.
	venvDir := m.VenvDir()
	t.Setenv("TEST_VENV_TARGET", venvDir)
	writeFakePythonAt(t, hostPython, `case "$*" in
  *-m\ venv*)
    /bin/rm -rf "$TEST_VENV_TARGET"
    /bin/mkdir -p "$TEST_VENV_TARGET"
    exit 0
    ;;
esac
printf '%s\n%s\n' "$0" "3.11.9"
`)

	progress := func(string, int, int) {}
	if err := m.ensureVenvForInstall(context.Background(), progress, 1, 1, false); err != nil {
		t.Fatalf("ensureVenvForInstall error = %v", err)
	}
	if _, err := os.Stat(venvDir); err != nil {
		t.Fatalf("venv directory was not recreated: %v", err)
	}
}

func TestEnsureVenvForInstallKeepsHealthyVenvByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	hostPython := filepath.Join(dir, "python3.13")
	writeFakePythonAt(t, hostPython, probeScriptOutput("3.13.1"))

	// Healthy 3.11 venv, newer 3.13 host on PATH: the default install
	// (upgradePackages=false) must not delete the working venv.
	m := NewRuntimeManagerAt(filepath.Join(dir, "asr-runtime"))
	if err := os.MkdirAll(filepath.Dir(m.PythonPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakePythonAt(t, m.PythonPath(), probeScriptOutput("3.11.9"))

	progress := func(string, int, int) {}
	if err := m.ensureVenvForInstall(context.Background(), progress, 1, 1, false); err != nil {
		t.Fatalf("ensureVenvForInstall error = %v", err)
	}
	if _, err := os.Stat(m.PythonPath()); err != nil {
		t.Fatalf("healthy venv should be kept: %v", err)
	}
}

func TestEnsureVenvForInstallUpgradesOnExplicitUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	hostPython := filepath.Join(dir, "python3.13")
	writeFakePythonAt(t, hostPython, probeScriptOutput("3.13.1"))

	// Same layout as above, but upgradePackages=true must recreate the venv
	// so it moves to the newer host interpreter.
	m := NewRuntimeManagerAt(filepath.Join(dir, "asr-runtime"))
	if err := os.MkdirAll(filepath.Dir(m.PythonPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakePythonAt(t, m.PythonPath(), probeScriptOutput("3.11.9"))

	venvDir := m.VenvDir()
	t.Setenv("TEST_VENV_TARGET", venvDir)
	writeFakePythonAt(t, hostPython, `case "$*" in
  *-m\ venv*)
    /bin/rm -rf "$TEST_VENV_TARGET"
    /bin/mkdir -p "$TEST_VENV_TARGET"
    exit 0
    ;;
esac
printf '%s\n%s\n' "$0" "3.13.1"
`)

	progress := func(string, int, int) {}
	if err := m.ensureVenvForInstall(context.Background(), progress, 1, 1, true); err != nil {
		t.Fatalf("ensureVenvForInstall error = %v", err)
	}
	if _, err := os.Stat(venvDir); err != nil {
		t.Fatalf("venv directory was not recreated: %v", err)
	}
}

// The venv-creating subprocess must run with the temp directory under the
// storage root, so venv creation scratch files never land in the system temp
// directory.
func TestEnsureVenvForInstallPassesTempDirToVenv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	hostPython := filepath.Join(dir, "python3.11")
	writeFakePythonAt(t, hostPython, probeScriptOutput("3.11.9"))

	m := NewRuntimeManagerAt(filepath.Join(dir, "asr-runtime"))
	if err := os.MkdirAll(filepath.Dir(m.PythonPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakePythonAt(t, m.PythonPath(), "exit 1\n")

	marker := filepath.Join(t.TempDir(), "tmpdir")
	t.Setenv("TEST_TMPDIR_MARKER", marker)
	venvDir := m.VenvDir()
	t.Setenv("TEST_VENV_TARGET", venvDir)
	writeFakePythonAt(t, hostPython, `case "$*" in
  *-m\ venv*)
    /bin/rm -rf "$TEST_VENV_TARGET"
    /bin/mkdir -p "$TEST_VENV_TARGET"
    printf '%s' "$TMPDIR" > "$TEST_TMPDIR_MARKER"
    exit 0
    ;;
esac
printf '%s\n%s\n' "$0" "3.11.9"
`)

	progress := func(string, int, int) {}
	if err := m.ensureVenvForInstall(context.Background(), progress, 1, 1, false); err != nil {
		t.Fatalf("ensureVenvForInstall error = %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal("venv creation did not receive TMPDIR")
	}
	if want := filepath.Join(filepath.Dir(m.RootDir()), tmpDirName); string(got) != want {
		t.Fatalf("venv creation TMPDIR = %q, want %q", string(got), want)
	}
}

func TestBaseASRDependenciesExcludeQwenASR(t *testing.T) {
	for _, pkg := range requiredASRPythonPackages {
		if pkg == "qwen_asr" {
			t.Fatalf("base ASR import requirements should not include qwen_asr: %#v", requiredASRPythonPackages)
		}
	}
	for _, pkg := range asrPythonPackages {
		if pkg == "qwen-asr" {
			t.Fatalf("base ASR install packages should not include qwen-asr: %#v", asrPythonPackages)
		}
	}
	manager := NewRuntimeManagerAt(filepath.Join(t.TempDir(), asrRuntimeDirName))
	cmd := manager.ASRInstallCommand(HardwareCPU)
	if hasString(cmd, "qwen-asr") {
		t.Fatalf("ASR install command should not include qwen-asr: %#v", cmd)
	}
	if !hasString(cmd, "funasr") {
		t.Fatalf("ASR install command should include funasr: %#v", cmd)
	}
	for _, value := range cmd {
		if hasTorchVersionPin(value) {
			t.Fatalf("ASR install command should not pin PyTorch package versions: %#v", cmd)
		}
	}

	status := manager.ASRStatus(context.Background())
	if hasString(status.InstallCommand, "diffusers>=0.34.0") {
		t.Fatalf("ASR status should not expose image runtime install command: %#v", status.InstallCommand)
	}
	if hasString(status.InstallCommand, "qwen-asr") {
		t.Fatalf("ASR status should not include qwen-asr by default: %#v", status.InstallCommand)
	}
	if !hasString(status.InstallCommand, "funasr") {
		t.Fatalf("ASR status install command should include funasr: %#v", status.InstallCommand)
	}
}

func TestMigrateLegacyRuntimeDir(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, legacyRuntimeDirName)
	runtimeDir := filepath.Join(root, runtimeDirName)
	if err := os.MkdirAll(filepath.Join(legacyDir, venvDirName), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyRuntimeDir(legacyDir, runtimeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, venvDirName)); err != nil {
		t.Fatalf("migrated runtime missing venv: %v", err)
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy runtime still exists or stat failed: %v", err)
	}
}

func TestMigrateLegacyRuntimeDirKeepsExistingAIRuntime(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, legacyRuntimeDirName)
	runtimeDir := filepath.Join(root, runtimeDirName)
	if err := os.MkdirAll(filepath.Join(legacyDir, "legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runtimeDir, venvDirName), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyRuntimeDir(legacyDir, runtimeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, venvDirName)); err != nil {
		t.Fatalf("existing AI runtime changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "legacy")); err != nil {
		t.Fatalf("legacy runtime should remain when AI runtime exists: %v", err)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasTorchVersionPin(value string) bool {
	return strings.HasPrefix(value, "torch==") ||
		strings.HasPrefix(value, "torchvision==") ||
		strings.HasPrefix(value, "torchaudio==")
}

func fakePython(t *testing.T, exitCode int, output string) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "python.cmd")
		script := "@echo off\r\n"
		if output != "" {
			script += "echo " + output + " 1>&2\r\n"
		}
		script += "exit /b " + strconv.Itoa(exitCode) + "\r\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := filepath.Join(dir, "python")
	script := "#!/bin/sh\n"
	if output != "" {
		script += "printf '%s' '" + output + "' >&2\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTorchInstallIndexArgsAliyunCUDA(t *testing.T) {
	indexes := PackageIndexes{
		Mirror:            PackageMirrorAliyun,
		TorchFindLinksURL: "https://mirrors.aliyun.com/pytorch-wheels/cu128",
		PyPIIndexURL:      aliyunPyPIIndex,
	}
	got := torchInstallIndexArgs(indexes)
	want := []string{
		"--index-url", aliyunPyPIIndex,
		"--find-links", "https://mirrors.aliyun.com/pytorch-wheels/cu128",
	}
	if len(got) != len(want) {
		t.Fatalf("torchInstallIndexArgs() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("torchInstallIndexArgs() = %#v, want %#v", got, want)
		}
	}
}

func TestRequiredPythonPackagesUseImportNames(t *testing.T) {
	for _, name := range requiredPythonPackages {
		if name == "protobuf" {
			t.Fatalf("protobuf package must be checked via google.protobuf import name")
		}
	}
}

func TestModelTTSPackagesFor(t *testing.T) {
	packages := modelTTSPackagesFor("modelscope/hexgrad/Kokoro-82M", "/models/Kokoro-82M")
	if len(packages) == 0 {
		t.Fatal("modelTTSPackagesFor() = none, want the kokoro packages")
	}
	// Chinese synthesis needs misaki's zh extra; without it the base package
	// installs and then fails at synthesis time.
	var hasChineseExtra bool
	for _, pkg := range packages {
		if pkg == "misaki[zh]" {
			hasChineseExtra = true
		}
	}
	if !hasChineseExtra {
		t.Fatalf("packages = %v, want misaki[zh] for Chinese support", packages)
	}
	if got := modelTTSPackagesFor("Qwen/Qwen3-0.6B", "/models/Qwen3-0.6B"); got != nil {
		t.Fatalf("modelTTSPackagesFor() = %v, want nil for a model needing no extras", got)
	}
}

func TestTTSOverlayPackagesForPlatform(t *testing.T) {
	got := ttsOverlayPackagesFor("qwen3-tts", "darwin", "arm64")
	want := map[string]bool{"mlx-audio": true, "mlx": true, "mlx-metal": true}
	for _, pkg := range got {
		delete(want, pkg)
	}
	if len(want) != 0 {
		t.Fatalf("darwin/arm64 overlay = %v, missing %v", got, want)
	}
	// MLX is Apple Silicon only; everywhere else the torch path is the only one
	// and nothing is overlaid.
	for _, platform := range [][2]string{{"darwin", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"windows", "amd64"}} {
		if got := ttsOverlayPackagesFor("qwen3-tts", platform[0], platform[1]); got != nil {
			t.Fatalf("%s/%s overlay = %v, want none", platform[0], platform[1], got)
		}
	}
	if got := ttsOverlayPackagesFor("kokoro", "darwin", "arm64"); got != nil {
		t.Fatalf("kokoro overlay = %v, want none", got)
	}
	// mlx-metal has no importable module; its presence is probed through
	// mlx.core, or the overlay would be reinstalled on every start.
	names := importNamesFor(got)
	var probesCore bool
	for _, name := range names {
		if name == "mlx_metal" {
			t.Fatalf("import names %v probe mlx_metal, which never imports", names)
		}
		if name == "mlx.core" {
			probesCore = true
		}
	}
	if !probesCore {
		t.Fatalf("import names %v do not probe mlx.core for mlx-metal", names)
	}
}

func TestImageOverlayPackagesForPlatform(t *testing.T) {
	got := imageOverlayPackagesFor(model.ImageBackendMLXQwen21, "darwin", "arm64")
	want := map[string]bool{"mflux==0.20.0": true, "mlx": true, "mlx-metal": true}
	for _, pkg := range got {
		delete(want, pkg)
	}
	if len(want) != 0 {
		t.Fatalf("darwin/arm64 image overlay = %v, missing %v", got, want)
	}
	for _, platform := range [][2]string{{"darwin", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"windows", "amd64"}} {
		if got := imageOverlayPackagesFor(model.ImageBackendMLXQwen21, platform[0], platform[1]); got != nil {
			t.Fatalf("%s/%s image overlay = %v, want none", platform[0], platform[1], got)
		}
	}
	if got := imageOverlayPackagesFor(model.ImageBackendDiffusers, "darwin", "arm64"); got != nil {
		t.Fatalf("diffusers image overlay = %v, want none", got)
	}
}

func TestImportNamesFor(t *testing.T) {
	got := importNamesFor([]string{"kokoro", "misaki[zh]", "mflux==0.20.0"})
	want := map[string]bool{"kokoro": true, "misaki": true, "ordered_set": true, "mflux": true}
	for _, name := range got {
		delete(want, name)
	}
	if len(want) > 0 {
		t.Fatalf("importNamesFor() = %v, missing %v", got, want)
	}
}

// transformers must never be declared with an exact pin: the backends share one
// venv, so a pin lets whichever was installed last decide the version for all of
// them and the environment thrashes between installs.
func TestTransformersIsNeverPinned(t *testing.T) {
	lists := map[string][]string{
		"tts": ttsPythonPackages,
		"asr": asrPythonPackages,
	}
	for name, specs := range lists {
		for _, spec := range specs {
			if !strings.HasPrefix(spec, "transformers") {
				continue
			}
			if strings.Contains(spec, "==") {
				t.Errorf("%s package list pins transformers (%q); declare a >= floor instead", name, spec)
			}
		}
	}
	if got := transformersConstraint(); !strings.HasPrefix(got, "transformers>=") {
		t.Errorf("transformersConstraint() = %q, want a >= floor", got)
	}
}

// A name says nothing about the version behind it: a machine can carry a
// python3.11 binary while plain python3 is far newer. Selecting the first name
// that resolves would pick the older interpreter and constrain every wheel the
// runtime installs, so the highest supported version has to win.
func TestFindHostPythonPrefersTheHighestVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake pythons; Windows goes through findWindowsHostPython")
	}
	dir := t.TempDir()
	fake := func(name, version string) string {
		path := filepath.Join(dir, name)
		writeFakePythonAt(t, path, probeScriptOutput(version))
		return path
	}

	t.Run("a newer bare python3 beats an older versioned binary", func(t *testing.T) {
		older := fake("python3.11-old", "3.11.9")
		newer := fake("python3-new", "3.13.1")
		// The older binary is listed first, so only the probed version can decide.
		got, err := findHostPythonFrom(context.Background(), nil, []string{older, newer})
		if err != nil {
			t.Fatalf("findHostPythonFrom: %v", err)
		}
		if got != newer {
			t.Fatalf("selected %q, want the newer interpreter %q", got, newer)
		}
	})

	t.Run("an unsupported interpreter is skipped", func(t *testing.T) {
		tooOld := fake("python3.9-sys", "3.9.6")
		ok := fake("python3.12-brew", "3.12.4")
		got, err := findHostPythonFrom(context.Background(), nil, []string{tooOld, ok})
		if err != nil {
			t.Fatalf("findHostPythonFrom: %v", err)
		}
		if got != ok {
			t.Fatalf("selected %q, want %q", got, ok)
		}
	})

	t.Run("only unsupported interpreters reports the version found", func(t *testing.T) {
		tooOld := fake("python3.8-only", "3.8.10")
		_, err := findHostPythonFrom(context.Background(), nil, []string{tooOld})
		if err == nil {
			t.Fatal("expected an error when no supported interpreter exists")
		}
		if !strings.Contains(err.Error(), "3.8") {
			t.Fatalf("error = %v, want it to name the version that was found", err)
		}
	})
}

func TestCleanCommandOutputNoHTML(t *testing.T) {
	in := "error: No matching distribution found for diffusers==0.41.0"
	got := cleanCommandOutput(in)
	if got != in {
		t.Fatalf("cleanCommandOutput = %q, want input unchanged", got)
	}
}

func TestCleanCommandOutputHTMLOnly(t *testing.T) {
	in := "<!DOCTYPE html>\n<html class=\"no-js ie6 oldie\" lang=\"en-US\">\n<head><meta charset=\"utf-8\"></head>\n<body>blocked</body>\n</html>"
	got := cleanCommandOutput(in)
	if strings.Contains(strings.ToLower(got), "<html") || strings.Contains(got, "<!DOCTYPE") {
		t.Fatalf("cleanCommandOutput still contains HTML: %q", got)
	}
	if !strings.Contains(got, "HTML page") {
		t.Fatalf("cleanCommandOutput = %q, want the network/mirror hint", got)
	}
}

func TestCleanCommandOutputHTMLMixed(t *testing.T) {
	in := "Looking in indexes: https://pypi.org/simple\n" +
		"error: Could not find a version that satisfies the requirement diffusers\n" +
		"<!DOCTYPE html>\n<html class=\"no-js ie6 oldie\" lang=\"en-US\">\n<head></head>\n<body>blocked</body>\n</html>"
	got := cleanCommandOutput(in)
	if strings.Contains(strings.ToLower(got), "<html") || strings.Contains(got, "<!DOCTYPE") {
		t.Fatalf("cleanCommandOutput still contains HTML: %q", got)
	}
	if !strings.Contains(got, "Could not find a version") {
		t.Fatalf("cleanCommandOutput = %q, want to keep the pip error line", got)
	}
	if strings.Contains(got, "blocked") {
		t.Fatalf("cleanCommandOutput = %q, want HTML body text removed", got)
	}
}

func TestRedactIndexURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://mirrors.aliyun.com/pypi/simple":             "https://mirrors.aliyun.com/pypi/simple",
		"https://user:token@mirror.example.com/pypi/simple":  "https://***@mirror.example.com/pypi/simple",
		"https://user:p@ss@mirror.example.com/pypi/simple":   "https://***@mirror.example.com/pypi/simple",
		"https://user@mirror.example.com/pypi/simple":        "https://***@mirror.example.com/pypi/simple",
		"https://mirror.example.com/simple?next=/path@other": "https://mirror.example.com/simple",
		"https://mirror.example.com/simple?token=secret":     "https://mirror.example.com/simple",
		"https://user:token@mirror.example.com":              "https://***@mirror.example.com",
		"not-a-url":                                          "not-a-url",
	} {
		if got := redactIndexURL(url); got != want {
			t.Fatalf("redactIndexURL(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestRedactSecrets(t *testing.T) {
	for in, want := range map[string]string{
		"Looking in indexes: https://user:token@mirror.example.com/simple": "Looking in indexes: https://***@mirror.example.com/simple",
		"Fetching https://user:p@ss@mirror.example.com/simple":             "Fetching https://***@mirror.example.com/simple",
		"Fetching https://mirror.example.com/simple?token=secret":          "Fetching https://mirror.example.com/simple",
		"https://mirror.example.com/simple?password=hunter2&other=x":       "https://mirror.example.com/simple",
		"https://mirror.example.com/simple?api_key=abc":                    "https://mirror.example.com/simple",
		"https://mirror.example.com/simple?X-Amz-Signature=abc123":         "https://mirror.example.com/simple",
		"authentication failed for token=secret":                           "authentication failed for token=***",
		"plain output without secrets":                                     "plain output without secrets",
	} {
		if got := redactSecrets(in); got != want {
			t.Fatalf("redactSecrets(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactingWriterFlushesLinesAndRemainder(t *testing.T) {
	var buf bytes.Buffer
	w := &redactingWriter{w: &buf}

	if _, err := w.Write([]byte("fetching https://user:token@mirror.example.com/simple")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("partial line flushed early: %q", buf.String())
	}
	if _, err := w.Write([]byte(" ok\n")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "fetching https://***@mirror.example.com/simple ok\n" {
		t.Fatalf("redactingWriter line = %q, want redacted line", got)
	}

	buf.Reset()
	if _, err := w.Write([]byte("progress 50%\r")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "progress 50%\r" {
		t.Fatalf("redactingWriter carriage return = %q, want the line flushed at \\r", got)
	}

	buf.Reset()
	if _, err := w.Write([]byte("partial https://mirror.example.com/simple?X-Amz-Signature=abc123")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "partial https://mirror.example.com/simple" {
		t.Fatalf("redactingWriter remainder = %q, want the query dropped", got)
	}
}

func TestRunCommandEnvRedactsCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake python")
	}
	secret := "https://user:token@mirror.example.com/simple?token=secret&X-Amz-Signature=abc123"
	python := fakePython(t, 1, secret)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	runErr := runCommandEnv(context.Background(), nil, python)
	os.Stderr = origStderr
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}

	if runErr == nil {
		t.Fatal("runCommandEnv should fail with exit code 1")
	}
	for name, text := range map[string]string{
		"stderr passthrough": string(out),
		"error detail":       runErr.Error(),
	} {
		for _, leak := range []string{"user:token", "token=secret", "X-Amz-Signature", "abc123"} {
			if strings.Contains(text, leak) {
				t.Fatalf("%s leaked credentials (%s): %q", name, leak, text)
			}
		}
	}
	if !strings.Contains(string(out), "***@mirror.example.com") {
		t.Fatalf("stderr passthrough missing redacted URL: %q", string(out))
	}
}

// os/exec copies stdout and stderr from separate goroutines once both streams
// are captured, so a command writing both streams exercises the shared capture
// buffer concurrently. The test only proves its point under -race, which is
// how CI runs the suite.
func TestRunCommandEnvCapturesConcurrentStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake python")
	}
	dir := t.TempDir()
	python := filepath.Join(dir, "python")
	writeFakePythonAt(t, python, `for i in 1 2 3 4 5 6 7 8; do
  echo "stdout line $i"
  echo "stderr line $i" >&2
done
exit 0
`)

	if err := runCommandEnv(context.Background(), nil, python); err != nil {
		t.Fatalf("runCommandEnv error = %v", err)
	}
}

// A credential-bearing URL split across two stdout writes with stderr output
// interleaved between the halves must not reassemble unredacted in the
// captured error detail: redaction has to complete within a stream before its
// bytes reach the shared capture. Pipe scheduling is not deterministic, so the
// fake python repeats the pattern to make at least one interleaving land
// between the two halves.
func TestRunCommandEnvRedactsInterleavedStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake python")
	}
	dir := t.TempDir()
	python := filepath.Join(dir, "python")
	writeFakePythonAt(t, python, `i=1
while [ "$i" -le 40 ]; do
  printf 'resolving https://user:sek'
  printf 'stderr noise %s\n' "$i" >&2
  printf 'ret@mirror.example.com/simple\n'
  i=$((i+1))
done
exit 1
`)

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout, origStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW
	runErr := runCommandEnv(context.Background(), nil, python)
	os.Stdout, os.Stderr = origStdout, origStderr
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	stdoutOut, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	stderrOut, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatal(err)
	}

	if runErr == nil {
		t.Fatal("runCommandEnv should fail with exit code 1")
	}
	for name, text := range map[string]string{
		"error detail":       runErr.Error(),
		"stdout passthrough": string(stdoutOut),
		"stderr passthrough": string(stderrOut),
	} {
		if strings.Contains(text, "user:sek") || strings.Contains(text, "sekret") {
			t.Fatalf("%s leaked credentials: %q", name, text)
		}
	}
	if !strings.Contains(runErr.Error(), "***@mirror.example.com") {
		t.Fatalf("error detail missing the redacted URL: %v", runErr)
	}
}

// The probe error output must be redacted before it is truncated: a
// truncation boundary falling inside a credential-bearing URL would leave a
// password fragment the redactor can no longer recognize.
func TestDiffusersHasPipelineRedactsBeforeTruncating(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake python")
	}
	dir := t.TempDir()
	m := NewRuntimeManagerAt(dir)
	venvPython := filepath.Join(dir, "venv", "bin", "python")

	// 100 filler bytes, then the URL, then 2015 filler bytes: the output is
	// 2166 bytes, so the 2048-byte tail starts inside the password.
	writeFakePythonAt(t, venvPython, "echo '"+
		strings.Repeat("a", 100)+
		"https://user:sup3rsekret@mirror.example.com/simple"+
		strings.Repeat("b", 2015)+"'\nexit 1\n")

	has, err := m.DiffusersHasPipeline(context.Background(), "QwenImage21Pipeline")
	if has || err == nil {
		t.Fatalf("failing probe = (%v, %v), want (false, error)", has, err)
	}
	if strings.Contains(err.Error(), "sup3rsekret") || strings.Contains(err.Error(), "ekret@") {
		t.Fatalf("truncated probe error leaked credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "mirror.example.com") {
		t.Fatalf("truncated probe error missing the output tail: %v", err)
	}
}

func TestLatestVersionWithFallback(t *testing.T) {
	pkg := "diffusers"
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<a href="https://mirror.test/diffusers-0.42.0-py3-none-any.whl">diffusers-0.42.0</a>`)
	}))
	defer mirror.Close()
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"info":{"version":"0.43.0"}}`)
	}))
	defer official.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	// A reachable mirror wins without consulting the official index.
	if got := latestVersionWithFallback(pkg, mirror.URL, official.URL); got != "0.42.0" {
		t.Fatalf("mirror lookup = %q, want 0.42.0", got)
	}
	// No mirror configured: the official index answers.
	if got := latestVersionWithFallback(pkg, "", official.URL); got != "0.43.0" {
		t.Fatalf("no-mirror lookup = %q, want 0.43.0", got)
	}
	// A failing mirror falls back to the official index.
	if got := latestVersionWithFallback(pkg, broken.URL, official.URL); got != "0.43.0" {
		t.Fatalf("fallback lookup = %q, want 0.43.0", got)
	}
	// Both failing leaves no version to report.
	if got := latestVersionWithFallback(pkg, broken.URL, broken.URL); got != "" {
		t.Fatalf("double failure = %q, want empty", got)
	}
}

func TestDiffusersHasPipeline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake python")
	}
	dir := t.TempDir()
	m := NewRuntimeManagerAt(dir)
	venvPython := filepath.Join(dir, "venv", "bin", "python")

	if has, err := m.DiffusersHasPipeline(context.Background(), ""); !has || err != nil {
		t.Fatalf("empty pipeline class = (%v, %v), want (true, nil)", has, err)
	}
	if has, err := m.DiffusersHasPipeline(context.Background(), "QwenImage21Pipeline"); has || err == nil {
		t.Fatalf("missing venv python = (%v, %v), want (false, error)", has, err)
	}
	if has, err := m.DiffusersHasPipeline(context.Background(), "not a class!"); has || err == nil {
		t.Fatalf("invalid class name = (%v, %v), want (false, error)", has, err)
	}

	// A failing import is not a missing class: the error must carry the
	// redacted Python output so the caller surfaces the real cause.
	writeFakePythonAt(t, venvPython, `echo "Looking in indexes: https://user:sekret@mirror.example.com/simple" >&2
echo "ModuleNotFoundError: No module named 'torch'" >&2
exit 1
`)
	has, err := m.DiffusersHasPipeline(context.Background(), "QwenImage21Pipeline")
	if has || err == nil {
		t.Fatalf("failing import = (%v, %v), want (false, error)", has, err)
	}
	if !strings.Contains(err.Error(), "No module named 'torch'") {
		t.Fatalf("failing import error missing the Python output: %v", err)
	}
	if strings.Contains(err.Error(), "user:sekret") {
		t.Fatalf("failing import error leaked credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "***@mirror.example.com") {
		t.Fatalf("failing import error missing the redacted URL: %v", err)
	}

	// An AttributeError for a different attribute, with the target class
	// name echoed in the traceback (its own source line or a "Did you mean"
	// suggestion), is a probe failure, not a missing class: text matching
	// on the traceback cannot tell the two apart.
	writeFakePythonAt(t, venvPython, `echo "  import diffusers; getattr(diffusers, 'QwenImage21Pipeline')" >&2
echo "AttributeError: module 'diffusers' has no attribute 'QwenImage2Pipeline'. Did you mean: 'QwenImage21Pipeline'?" >&2
exit 1
`)
	has, err = m.DiffusersHasPipeline(context.Background(), "QwenImage21Pipeline")
	if has || err == nil {
		t.Fatalf("unrelated AttributeError = (%v, %v), want (false, error)", has, err)
	}
	if !strings.Contains(err.Error(), "Did you mean") {
		t.Fatalf("unrelated AttributeError error missing the Python output: %v", err)
	}

	// An attribute that is not a DiffusionPipeline subclass (for example
	// __version__) must not pass as a pipeline class.
	writeFakePythonAt(t, venvPython, "exit 4\n")
	has, err = m.DiffusersHasPipeline(context.Background(), "QwenImage21Pipeline")
	if has || err == nil {
		t.Fatalf("non-pipeline attribute = (%v, %v), want (false, error)", has, err)
	}
	if !strings.Contains(err.Error(), "not a DiffusionPipeline class") {
		t.Fatalf("non-pipeline attribute error = %v", err)
	}

	writeFakePythonAt(t, venvPython, "exit 3\n")
	if has, err := m.DiffusersHasPipeline(context.Background(), "QwenImage21Pipeline"); has || err != nil {
		t.Fatalf("missing attribute = (%v, %v), want (false, nil)", has, err)
	}

	writeFakePythonAt(t, venvPython, "exit 0\n")
	if has, err := m.DiffusersHasPipeline(context.Background(), "QwenImage21Pipeline"); !has || err != nil {
		t.Fatalf("passing import check = (%v, %v), want (true, nil)", has, err)
	}
}

// TestDiffusersHasPipelineWithRealPython runs the probe script under a real
// Python interpreter against a stub diffusers module, so the exit-code
// contract the fake-python cases above only assume (0 subclass, 3 missing,
// 4 not a DiffusionPipeline subclass) is actually exercised.
func TestDiffusersHasPipelineWithRealPython(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake python")
	}
	real, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	m := NewRuntimeManagerAt(dir)
	venvPython := filepath.Join(dir, "venv", "bin", "python")

	stub := filepath.Join(dir, "stub", "diffusers")
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "class DiffusionPipeline:\n    pass\n\n\nclass QwenImage21Pipeline(DiffusionPipeline):\n    pass\n\n\n__version__ = \"9.9.9\"\n"
	if err := os.WriteFile(filepath.Join(stub, "__init__.py"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFakePythonAt(t, venvPython, fmt.Sprintf("export PYTHONPATH=%q\nexec %q \"$@\"\n", filepath.Join(dir, "stub"), real))

	if has, err := m.DiffusersHasPipeline(context.Background(), "QwenImage21Pipeline"); !has || err != nil {
		t.Fatalf("pipeline subclass = (%v, %v), want (true, nil)", has, err)
	}
	if has, err := m.DiffusersHasPipeline(context.Background(), "NoSuchPipelineExists"); has || err != nil {
		t.Fatalf("missing class = (%v, %v), want (false, nil)", has, err)
	}
	// __version__ exists on the module but is not a class: plain module
	// attributes must not pass as a pipeline class.
	has, err := m.DiffusersHasPipeline(context.Background(), "__version__")
	if has || err == nil {
		t.Fatalf("plain attribute = (%v, %v), want (false, error)", has, err)
	}
	if !strings.Contains(err.Error(), "not a DiffusionPipeline class") {
		t.Fatalf("plain attribute error = %v", err)
	}
}
