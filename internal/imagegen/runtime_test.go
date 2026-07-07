package imagegen

import (
	"context"
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

func TestVerifyImportsWithFakePython(t *testing.T) {
	ctx := context.Background()
	if err := verifyImports(ctx, fakePython(t, 0, ""), []string{"torch"}); err != nil {
		t.Fatalf("verifyImports success returned error: %v", err)
	}

	err := verifyImports(ctx, fakePython(t, 1, strings.Repeat("x", 2200)+"libtorchaudio.pyd failed"), []string{"torch"})
	if err == nil {
		t.Fatal("verifyImports failure returned nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "libtorchaudio.pyd failed") {
		t.Fatalf("verifyImports error missing output tail: %q", msg)
	}
	if len(msg) > len("python import check failed: ")+2048 {
		t.Fatalf("verifyImports error was not truncated: length=%d", len(msg))
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
