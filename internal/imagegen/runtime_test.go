package imagegen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
		{HardwareCUDA, "https://mirrors.aliyun.com/pytorch-wheels/cu124", "https://mirrors.aliyun.com/pypi/simple"},
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
	if got.TorchFindLinksURL != "https://mirrors.aliyun.com/pytorch-wheels/cu124" {
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

func TestAliyunCUDAPinsTorchPackages(t *testing.T) {
	t.Setenv(mirrorModeEnv, "aliyun")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")

	manager := NewRuntimeManagerAt(t.TempDir())
	cmd := manager.InstallCommand(HardwareCUDA)

	for _, want := range aliyunCUDATorchPackages {
		if !hasString(cmd, want) {
			t.Fatalf("InstallCommand(CUDA) missing %q in %#v", want, cmd)
		}
	}
	if !hasString(cmd, "--find-links") {
		t.Fatalf("InstallCommand(CUDA) should use Aliyun wheel links: %#v", cmd)
	}
}

func TestOfficialCUDADoesNotPinAliyunTorchPackages(t *testing.T) {
	t.Setenv(mirrorModeEnv, "official")
	t.Setenv(torchIndexOverrideEnv, "")
	t.Setenv(pypiIndexOverrideEnv, "")

	got := torchPackageSpecs(HardwareCUDA, ResolvePackageIndexes(HardwareCUDA))
	if len(got) != len(defaultTorchPackages) {
		t.Fatalf("official CUDA packages = %#v, want %#v", got, defaultTorchPackages)
	}
	for i := range got {
		if got[i] != defaultTorchPackages[i] {
			t.Fatalf("official CUDA packages = %#v, want %#v", got, defaultTorchPackages)
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

func TestRequiredPythonPackagesUseImportNames(t *testing.T) {
	for _, name := range requiredPythonPackages {
		if name == "protobuf" {
			t.Fatalf("protobuf package must be checked via google.protobuf import name")
		}
	}
}
