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
