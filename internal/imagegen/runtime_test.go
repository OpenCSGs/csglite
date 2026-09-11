package imagegen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestTorchIndexURL(t *testing.T) {
	t.Setenv(mirrorModeEnv, "official")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")
	tests := []struct {
		hw   HardwareKind
		want string
	}{
		{HardwareCUDA, "https://download.pytorch.org/whl/cu128"},
		{HardwareROCm, "https://download.pytorch.org/whl/rocm7.1"},
		{HardwareMPS, ""},
		{HardwareCPU, "https://download.pytorch.org/whl/cpu"},
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

func TestResolvePackageIndexesDefaultsToAliyun(t *testing.T) {
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
	if got.Mirror != PackageMirrorAliyun {
		t.Fatalf("default package mirror = %q, want %q", got.Mirror, PackageMirrorAliyun)
	}
	if got.TorchFindLinksURL != "https://mirrors.aliyun.com/pytorch-wheels/cu128" {
		t.Fatalf("default CUDA torch find-links = %q", got.TorchFindLinksURL)
	}
	if got.TorchIndexURL != "" {
		t.Fatalf("default CUDA torch index = %q, want empty", got.TorchIndexURL)
	}
	if got.PyPIIndexURL != "https://mirrors.aliyun.com/pypi/simple" {
		t.Fatalf("default PyPI index = %q", got.PyPIIndexURL)
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
	embeddingRuntime, err := NewEmbeddingRuntimeManager()
	if err != nil {
		t.Fatal(err)
	}

	wantImage := filepath.Join(home, config.AppDir, runtimeDirName)
	wantASR := filepath.Join(home, config.AppDir, asrRuntimeDirName)
	wantEmbedding := filepath.Join(home, config.AppDir, embeddingRuntimeDirName)
	if imageRuntime.RootDir() != wantImage {
		t.Fatalf("image runtime root = %q, want %q", imageRuntime.RootDir(), wantImage)
	}
	if asrRuntime.RootDir() != wantASR {
		t.Fatalf("ASR runtime root = %q, want %q", asrRuntime.RootDir(), wantASR)
	}
	if embeddingRuntime.RootDir() != wantEmbedding {
		t.Fatalf("embedding runtime root = %q, want %q", embeddingRuntime.RootDir(), wantEmbedding)
	}
}

func TestRuntimeManagersShareUVCache(t *testing.T) {
	root := t.TempDir()
	imageRuntime := NewRuntimeManagerAt(filepath.Join(root, runtimeDirName))
	asrRuntime := NewRuntimeManagerAt(filepath.Join(root, asrRuntimeDirName))
	embeddingRuntime := NewRuntimeManagerAt(filepath.Join(root, embeddingRuntimeDirName))

	want := "UV_CACHE_DIR=" + filepath.Join(root, uvCacheDirName)
	if got := imageRuntime.uvInstallEnv(); len(got) != 1 || got[0] != want {
		t.Fatalf("image runtime uv env = %#v, want %#v", got, []string{want})
	}
	if got := asrRuntime.uvInstallEnv(); len(got) != 1 || got[0] != want {
		t.Fatalf("ASR runtime uv env = %#v, want %#v", got, []string{want})
	}
	if got := embeddingRuntime.uvInstallEnv(); len(got) != 1 || got[0] != want {
		t.Fatalf("embedding runtime uv env = %#v, want %#v", got, []string{want})
	}
}

func TestEmbeddingRuntimeInstallCommand(t *testing.T) {
	manager := NewRuntimeManagerAt(filepath.Join(t.TempDir(), embeddingRuntimeDirName))
	cmd := manager.EmbeddingInstallCommand(HardwareCPU)
	for _, want := range []string{"transformers>=5.0", "peft", "pillow", "numpy", "librosa", "soundfile"} {
		if !hasString(cmd, want) {
			t.Fatalf("embedding install command missing %q: %#v", want, cmd)
		}
	}
	for _, unwanted := range []string{"diffusers>=0.34.0", "funasr", "sentence-transformers", "vllm==0.20.1", "torchcodec"} {
		if hasString(cmd, unwanted) {
			t.Fatalf("embedding install command should not include %q by default: %#v", unwanted, cmd)
		}
	}
}

func TestEmbeddingTorchPackagesByOS(t *testing.T) {
	windowsPackages := embeddingTorchPackagesForGOOS("windows")
	for _, unwanted := range []string{"torchaudio"} {
		if hasString(windowsPackages, unwanted) {
			t.Fatalf("Windows embedding torch packages should not include %q: %#v", unwanted, windowsPackages)
		}
	}
	for _, want := range []string{"torch", "torchvision"} {
		if !hasString(windowsPackages, want) {
			t.Fatalf("Windows embedding torch packages missing %q: %#v", want, windowsPackages)
		}
	}

	linuxPackages := embeddingTorchPackagesForGOOS("linux")
	if len(linuxPackages) != len(torchPackages) {
		t.Fatalf("Linux embedding torch packages = %#v, want %#v", linuxPackages, torchPackages)
	}
	for i := range torchPackages {
		if linuxPackages[i] != torchPackages[i] {
			t.Fatalf("Linux embedding torch packages = %#v, want %#v", linuxPackages, torchPackages)
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

// The Windows readiness check must reproduce the worker's exact import block:
// a bare `import transformers` passes even with a broken torchaudio because
// transformers lazy-loads its submodules (issue #54).
func TestWindowsEmbeddingImportCheckScriptMatchesWorker(t *testing.T) {
	if !strings.Contains(windowsEmbeddingWorkerImportCheckScript, "from transformers import AutoModel, AutoProcessor, WhisperFeatureExtractor") {
		t.Fatalf("check script must expand transformers lazy modules the same way the worker does:\n%s", windowsEmbeddingWorkerImportCheckScript)
	}
	data, err := os.ReadFile(filepath.Join("..", "embedding", "worker", "embedding_worker.py"))
	if err != nil {
		t.Fatalf("reading embedding worker script: %v", err)
	}
	worker := string(data)
	for _, line := range strings.Split(strings.TrimSpace(windowsEmbeddingWorkerImportCheckScript), "\n") {
		if !strings.Contains(worker, line) {
			t.Fatalf("check script line %q not found in embedding_worker.py; keep both in sync", line)
		}
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

func TestVerifyEmbeddingWorkerImportsGatingCacheAndHint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh fake python")
	}
	ctx := context.Background()
	m := NewRuntimeManagerAt(t.TempDir())
	pythonPath := m.PythonPath()

	// Non-Windows hosts skip the check entirely, even without a venv.
	if err := m.verifyEmbeddingWorkerImports(ctx, "linux"); err != nil {
		t.Fatalf("non-windows verify returned error: %v", err)
	}

	writeFakePythonAt(t, pythonPath, "printf '%s' 'OSError: Could not load this library: libtorchaudio.pyd' >&2\nexit 1\n")
	err := m.verifyEmbeddingWorkerImports(ctx, "windows")
	if err == nil {
		t.Fatal("expected verify failure with broken python")
	}
	if !strings.Contains(err.Error(), "libtorchaudio") {
		t.Fatalf("verify error missing traceback tail: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Visual C++") {
		t.Fatalf("verify error missing actionable hint: %q", err.Error())
	}

	// A passing check is cached per runtime root...
	writeFakePythonAt(t, pythonPath, "exit 0\n")
	if err := m.verifyEmbeddingWorkerImports(ctx, "windows"); err != nil {
		t.Fatalf("verify with healthy python returned error: %v", err)
	}
	writeFakePythonAt(t, pythonPath, "exit 1\n")
	if err := m.verifyEmbeddingWorkerImports(ctx, "windows"); err != nil {
		t.Fatalf("cached verify should not re-run python: %v", err)
	}
	// ...until invalidated (install ran or the worker failed to start).
	m.InvalidateEmbeddingImportCheck()
	if err := m.verifyEmbeddingWorkerImports(ctx, "windows"); err == nil {
		t.Fatal("expected verify failure after invalidation")
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

func TestImportNamesFor(t *testing.T) {
	got := importNamesFor([]string{"kokoro", "misaki[zh]"})
	want := map[string]bool{"kokoro": true, "misaki": true, "ordered_set": true}
	for _, name := range got {
		delete(want, name)
	}
	if len(want) > 0 {
		t.Fatalf("importNamesFor() = %v, missing %v", got, want)
	}
}
