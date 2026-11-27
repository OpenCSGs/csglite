package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/imagegen"
)

func TestRuntimeKindFromArg(t *testing.T) {
	for arg, want := range map[string]runtimeKind{
		"image":      runtimeImage,
		"IMAGE":      runtimeImage,
		" diffusers": runtimeImage,
		"asr":        runtimeASR,
		"TTS":        runtimeTTS,
	} {
		got, err := runtimeKindFromArg(arg)
		if err != nil {
			t.Fatalf("runtimeKindFromArg(%q): %v", arg, err)
		}
		if got != want {
			t.Fatalf("runtimeKindFromArg(%q) = %q, want %q", arg, got, want)
		}
	}
	if _, err := runtimeKindFromArg("sglang"); err == nil {
		t.Fatal("runtimeKindFromArg accepted an unknown runtime")
	}
}

func TestRuntimeKindsFromArgs(t *testing.T) {
	kinds, err := runtimeKindsFromArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 3 || kinds[0] != runtimeImage || kinds[1] != runtimeASR || kinds[2] != runtimeTTS {
		t.Fatalf("runtimeKindsFromArgs(nil) = %v, want all three runtimes", kinds)
	}
	kinds, err = runtimeKindsFromArgs([]string{"tts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 1 || kinds[0] != runtimeTTS {
		t.Fatalf("runtimeKindsFromArgs([tts]) = %v", kinds)
	}
	if _, err := runtimeKindsFromArgs([]string{"nope"}); err == nil {
		t.Fatal("runtimeKindsFromArgs accepted an unknown runtime")
	}
}

func TestFormatRuntimeStatus(t *testing.T) {
	ready := imagegen.RuntimeStatus{
		Ready:                  true,
		VenvDir:                "/home/ai-runtime/venv",
		Hardware:               imagegen.HardwareMPS,
		DiffusersVersion:       "0.36.0",
		DiffusersLatestVersion: "0.43.0",
	}
	out := formatRuntimeStatus(runtimeImage, ready)
	for _, want := range []string{
		"image (Diffusers):\n",
		"  ready: yes\n",
		"  venv: /home/ai-runtime/venv\n",
		"  hardware: mps\n",
		"  diffusers: 0.36.0 (latest: 0.43.0)\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatRuntimeStatus() missing %q:\n%s", want, out)
		}
	}

	notReady := imagegen.RuntimeStatus{
		Ready:           false,
		Error:           "ASR runtime is not installed",
		VenvDir:         "/home/asr-runtime/venv",
		Hardware:        imagegen.HardwareCPU,
		MissingPackages: []string{"torch", "fastapi"},
	}
	out = formatRuntimeStatus(runtimeASR, notReady)
	for _, want := range []string{
		"ASR:\n",
		"  ready: no\n",
		"  error: ASR runtime is not installed\n",
		"  missing packages: torch, fastapi\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatRuntimeStatus() missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "diffusers:") {
		t.Fatalf("formatRuntimeStatus() reported a Diffusers version for the ASR runtime:\n%s", out)
	}
}

func TestDiffusersUpgradeReport(t *testing.T) {
	if got := diffusersUpgradeReport("", "0.36.0", "0.43.0"); got != "" {
		t.Fatalf("diffusersUpgradeReport with a failed version probe = %q, want empty", got)
	}
	if got := diffusersUpgradeReport("0.43.0", "", "0.43.0"); !strings.Contains(got, "Diffusers installed: 0.43.0") {
		t.Fatalf("diffusersUpgradeReport after a fresh install = %q", got)
	}
	if got := diffusersUpgradeReport("0.43.0", "0.36.0", "0.43.0"); !strings.Contains(got, "0.36.0 -> 0.43.0") {
		t.Fatalf("diffusersUpgradeReport after an upgrade = %q", got)
	}
	got := diffusersUpgradeReport("0.43.0", "0.43.0", "0.43.0")
	if !strings.Contains(got, "already at the latest version") {
		t.Fatalf("diffusersUpgradeReport without a version change = %q", got)
	}
	if !strings.Contains(got, "CSGHUB_LITE_PYPI_INDEX_URL") {
		t.Fatalf("diffusersUpgradeReport without a version change is missing the mirror hint: %q", got)
	}
	got = diffusersUpgradeReport("0.39.0", "0.36.0", "0.43.0")
	if !strings.Contains(got, "The latest available Diffusers version is 0.43.0.") {
		t.Fatalf("diffusersUpgradeReport is missing the version note: %q", got)
	}

	// An unchanged version with no probe result must not claim to be the
	// latest: the index may simply be unreachable.
	got = diffusersUpgradeReport("0.39.0", "0.39.0", "")
	if !strings.Contains(got, "Diffusers version unchanged after upgrade: 0.39.0") {
		t.Fatalf("diffusersUpgradeReport with an unreachable index = %q", got)
	}
	if strings.Contains(got, "already at the latest version") {
		t.Fatalf("diffusersUpgradeReport with an unreachable index claims the latest version: %q", got)
	}

	// The #350 scenario: the upgrade did not move Diffusers while the index
	// offers a newer version. The report must not call the installed version
	// the latest and must point at the two possible causes.
	got = diffusersUpgradeReport("0.39.0", "0.39.0", "0.43.0")
	if !strings.Contains(got, "Diffusers version unchanged after upgrade: 0.39.0") {
		t.Fatalf("diffusersUpgradeReport with a lagging upgrade = %q", got)
	}
	if strings.Contains(got, "already at the latest version") {
		t.Fatalf("diffusersUpgradeReport contradicts itself about the latest version: %q", got)
	}
	if !strings.Contains(got, "The latest available Diffusers version is 0.43.0; check for dependency conflicts or a lagging mirror") {
		t.Fatalf("diffusersUpgradeReport with a lagging upgrade is missing the conflict hint: %q", got)
	}

	// An index offering an older version than the installed one is simply
	// lagging behind: reporting it would read like advice to downgrade.
	got = diffusersUpgradeReport("0.43.0", "0.43.0", "0.39.0")
	if !strings.Contains(got, "Diffusers version unchanged after upgrade: 0.43.0") {
		t.Fatalf("diffusersUpgradeReport with a lagging index = %q", got)
	}
	if strings.Contains(got, "latest available") {
		t.Fatalf("diffusersUpgradeReport reports an older index version: %q", got)
	}
	got = diffusersUpgradeReport("0.43.0", "0.39.0", "0.39.0")
	if !strings.Contains(got, "0.39.0 -> 0.43.0") {
		t.Fatalf("diffusersUpgradeReport after an upgrade past the index = %q", got)
	}
	if strings.Contains(got, "latest available") {
		t.Fatalf("diffusersUpgradeReport reports an older index version after an upgrade: %q", got)
	}
}

func TestRuntimeStatusCommand(t *testing.T) {
	setupCLIConfigHome(t)
	output := captureCLIStdout(t, func() {
		if err := runRuntimeStatus(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"image (Diffusers):",
		"ASR:",
		"text-to-speech:",
		"ready: no",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("runtime status output missing %q:\n%s", want, output)
		}
	}
}

func TestRuntimeStatusRejectsUnknownRuntime(t *testing.T) {
	if err := runRuntimeStatus(context.Background(), []string{"sglang"}); err == nil {
		t.Fatal("runRuntimeStatus accepted an unknown runtime")
	}
}

func TestRuntimeUpgradeRejectsUnknownRuntime(t *testing.T) {
	if err := runRuntimeUpgrade(context.Background(), []string{"sglang"}, ""); err == nil {
		t.Fatal("runRuntimeUpgrade accepted an unknown runtime")
	}
}

func TestRuntimeUpgradeRejectsPipelineForNonImageRuntime(t *testing.T) {
	if err := runRuntimeUpgrade(context.Background(), []string{"asr"}, "QwenImage21Pipeline"); err == nil {
		t.Fatal("runRuntimeUpgrade accepted --pipeline for a non-image runtime")
	}
}

func TestRuntimeUpgradeRejectsInvalidPipelineClass(t *testing.T) {
	for _, name := range []string{"not a class!", "pkg.Class", "QwenImage21Pipeline()"} {
		if err := runRuntimeUpgrade(context.Background(), nil, name); err == nil {
			t.Fatalf("runRuntimeUpgrade accepted an invalid pipeline class %q", name)
		}
	}
}
