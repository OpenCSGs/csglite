package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLlamaCppVersionLockstepWithInstallScripts(t *testing.T) {
	t.Helper()

	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	wantShell := `LLAMA_CPP_DEFAULT_TAG="${CSGHUB_LITE_LLAMA_CPP_TAG:-` + BundledConverterLLamacppRef + `}"`
	wantPowerShell := `$LlamaCppDefaultTag = if ($env:CSGHUB_LITE_LLAMA_CPP_TAG) { $env:CSGHUB_LITE_LLAMA_CPP_TAG } else { "` + BundledConverterLLamacppRef + `" }`

	cases := []struct {
		path string
		want string
	}{
		{path: filepath.Join(repoRoot, "scripts", "install.sh"), want: wantShell},
		{path: filepath.Join(repoRoot, "scripts", "install.ps1"), want: wantPowerShell},
		{path: filepath.Join(repoRoot, "scripts", "llama-build", "common.sh"), want: `${1:-` + BundledConverterLLamacppRef + `}`},
		{path: filepath.Join(repoRoot, "scripts", "llama-build", "build-ubuntu22-cuda-x64.sh"), want: `LLAMA_TAG:-` + BundledConverterLLamacppRef},
		{path: filepath.Join(repoRoot, "scripts", "llama-build", "build-ubuntu22-cuda-arm64.sh"), want: `LLAMA_TAG:-` + BundledConverterLLamacppRef},
		{path: filepath.Join(repoRoot, "scripts", "llama-build", "build-ubuntu22-rocm-x64.sh"), want: `LLAMA_TAG:-` + BundledConverterLLamacppRef},
		{path: filepath.Join(repoRoot, "Makefile"), want: `LLAMA_TAG ?= ` + BundledConverterLLamacppRef},
	}

	for _, tc := range cases {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if !strings.Contains(string(data), tc.want) {
			t.Fatalf("%s is not pinned to BundledConverterLLamacppRef=%s", tc.path, BundledConverterLLamacppRef)
		}
	}
}

func TestLlamaInstallerCandidateCollectionCrossPlatform(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	shell := readRepoFile(t, repoRoot, "scripts", "install.sh")
	powerShell := readRepoFile(t, repoRoot, "scripts", "install.ps1")

	shellMustContain := []string{
		`add_candidate()`,
		`_candidates="${_candidates:+${_candidates} }${_candidate}"`,
		`llama-${_llama_tag}-bin-macos-x64.tar.gz`,
		`llama-${_llama_tag}-bin-macos-arm64.tar.gz`,
		`add_candidate "llama-${_llama_tag}-bin-ubuntu-cuda-${_arch_token}.tar.gz"`,
		// Legacy alias of the pre-b9158 local mirrors. Current tags publish no
		// such asset, but an old pinned tag still resolves through it.
		`llama-${_llama_tag}-bin-ubuntu-cuda-12.4-${_arch_token}.tar.gz`,
		`llama-${_llama_tag}-bin-ubuntu-${_arch_token}.tar.gz`,
		`ensure_cuda_runtime_for_llama "$_llama_dir" "$_llama_asset"`,
		`CSGHUB_LITE_AUTO_INSTALL_CUDA_LIBS`,
		`cuda-libraries-${_cuda_pkg_suffix}`,
	}
	for _, want := range shellMustContain {
		if !strings.Contains(shell, want) {
			t.Fatalf("scripts/install.sh missing llama.cpp candidate logic %q", want)
		}
	}

	// The CUDA candidate scan must not pick up upstream's versioned Linux CUDA
	// names (b11541+). Upstream builds that job on Ubuntu 24.04, so the binary
	// needs GLIBC_2.38 and cannot start on the 22.04 hosts this installer
	// targets, and nothing re-checks it afterwards. Matching the versioned scan
	// pattern keeps that regression out; the legacy 12.4 alias above is a fixed
	// name, not a metadata scan.
	if strings.Contains(shell, `-bin-ubuntu-cuda-[0-9]+`) {
		t.Fatal("scripts/install.sh must not scan release metadata for versioned Linux CUDA names")
	}

	// ROCm keeps its series scan because the series is part of our asset name and
	// no fixed-name candidate can cover it, but that scan must read only our own
	// GitLab release metadata. Upstream builds its Linux ROCm assets on Ubuntu
	// 24.04 as well, so letting the scan see GitHub metadata would offer those
	// binaries to 22.04 hosts.
	if !strings.Contains(shell, `_llama_json="$(try_download_text "$_gl_url"`) {
		t.Fatal("scripts/install.sh must feed the ROCm series scan from GitLab release metadata")
	}
	if strings.Contains(shell, `_llama_json="$(region_download_text`) {
		t.Fatal("scripts/install.sh must not feed llama.cpp release metadata from GitHub")
	}
	if !strings.Contains(shell, `add_release_candidates "llama-${_llama_tag}-bin-ubuntu-rocm-[0-9]+`) {
		t.Fatal("scripts/install.sh must keep the ROCm series metadata scan")
	}

	powerShellMustContain := []string{
		`$candidates = [System.Collections.Generic.List[object]]::new()`,
		`[void]$candidates.Add(@{ Asset = $Asset; Cudart = $Cudart })`,
		`llama-${llamaTag}-bin-win-cuda-12.4-${archToken}.zip`,
		`llama-${escapedTag}-bin-win-cuda-[0-9.]+-${archToken}\.zip`,
		`llama-${llamaTag}-bin-win-vulkan-${archToken}.zip`,
		`llama-${llamaTag}-bin-win-cpu-${archToken}.zip`,
	}
	for _, want := range powerShellMustContain {
		if !strings.Contains(powerShell, want) {
			t.Fatalf("scripts/install.ps1 missing llama.cpp candidate logic %q", want)
		}
	}
	if strings.Contains(powerShell, `$candidates += @{ Asset = $Asset; Cudart = $Cudart }`) {
		t.Fatal("scripts/install.ps1 must not append candidates with += inside Add-Candidate; PowerShell scopes that assignment locally")
	}
}

func readRepoFile(t *testing.T, repoRoot string, path ...string) string {
	t.Helper()

	parts := append([]string{repoRoot}, path...)
	data, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(path...), err)
	}
	return string(data)
}
