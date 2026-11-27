package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/spf13/cobra"
)

// pipelineClassNameRe matches a Python class name, so --pipeline rejects
// values that could never name a Diffusers pipeline class before any install
// work starts.
var pipelineClassNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// runtimeKind names one of the Python runtimes csghub-lite manages: the image
// (Diffusers) runtime plus the ASR and text-to-speech runtimes.
type runtimeKind string

const (
	runtimeImage runtimeKind = "image"
	runtimeASR   runtimeKind = "asr"
	runtimeTTS   runtimeKind = "tts"
)

// label names a runtime in user-facing messages.
func (k runtimeKind) label() string {
	switch k {
	case runtimeImage:
		return "image (Diffusers)"
	case runtimeASR:
		return "ASR"
	case runtimeTTS:
		return "text-to-speech"
	}
	return string(k)
}

func newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect or upgrade the local Python runtimes",
		Long: `Inspect or upgrade the local Python runtimes.

The image runtime serves Diffusers image models, the ASR runtime serves
speech recognition models, and the text-to-speech runtime serves TTS models.
Each is a Python environment under the csghub-lite storage root.

status reports whether a runtime is ready and, for the image runtime, the
installed and latest Diffusers versions. upgrade reinstalls a runtime's
packages at their latest versions, which is how to pick up a newly released
Diffusers that a model needs (for example Qwen-Image-2.1).`,
	}
	cmd.AddCommand(newRuntimeStatusCmd(), newRuntimeUpgradeCmd())
	return cmd
}

func newRuntimeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [image|asr|tts]",
		Short: "Show Python runtime status",
		Long: `Show whether the local Python runtimes are ready to serve models.

Without an argument it reports all three runtimes: image (Diffusers), ASR,
and text-to-speech. The image runtime also reports the installed Diffusers
version and the latest version available from the configured package index.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRuntimeStatus(cmd.Context(), args)
		},
	}
}

func newRuntimeUpgradeCmd() *cobra.Command {
	var pipelineClass string
	cmd := &cobra.Command{
		Use:   "upgrade [image|asr|tts]",
		Short: "Upgrade Python runtime packages",
		Long: `Upgrade a local Python runtime's packages to their latest versions.

Without an argument it upgrades the image (Diffusers) runtime; this also
installs the runtime when it is missing. Use this when a model fails with an
error such as "module diffusers has no attribute QwenImage21Pipeline": the
installed Diffusers predates the pipeline class the model needs. Pass the
class with --pipeline to verify after upgrading that the installed Diffusers
provides it.

Upgrading the ASR or text-to-speech runtime also reinstalls PyTorch at its
latest version, which can be a large download.

If the server is running with models loaded, unload them first (for example
csghub-lite stop model MODEL); a running worker keeps the packages it started
with.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRuntimeUpgrade(cmd.Context(), args, pipelineClass)
		},
	}
	cmd.Flags().StringVar(&pipelineClass, "pipeline", "", "verify this Diffusers pipeline class (for example QwenImage21Pipeline) exists after upgrading")
	return cmd
}

func runRuntimeStatus(ctx context.Context, args []string) error {
	kinds, err := runtimeKindsFromArgs(args)
	if err != nil {
		return err
	}
	for i, kind := range kinds {
		status, err := runtimeStatusFor(ctx, kind)
		if err != nil {
			return err
		}
		if i > 0 {
			fmt.Println()
		}
		fmt.Print(formatRuntimeStatus(kind, status))
	}
	return nil
}

func runRuntimeUpgrade(ctx context.Context, args []string, pipelineClass string) error {
	kind := runtimeImage
	if len(args) > 0 {
		k, err := runtimeKindFromArg(args[0])
		if err != nil {
			return err
		}
		kind = k
	}
	pipelineClass = strings.TrimSpace(pipelineClass)
	if pipelineClass != "" && kind != runtimeImage {
		return fmt.Errorf("--pipeline only applies to the image runtime")
	}
	if pipelineClass != "" && !pipelineClassNameRe.MatchString(pipelineClass) {
		return fmt.Errorf("--pipeline must be a Python class name such as QwenImage21Pipeline")
	}
	manager, err := runtimeManagerFor(kind)
	if err != nil {
		return err
	}
	var prevDiffusers string
	if kind == runtimeImage {
		prevDiffusers = manager.DiffusersVersion(ctx)
	}
	fmt.Printf("Upgrading the %s runtime...\n", kind.label())
	progress := func(step string, current, total int) {
		fmt.Printf("  [%d/%d] %s\n", current, total, step)
	}
	var status imagegen.RuntimeStatus
	switch kind {
	case runtimeImage:
		status, err = manager.InstallWithProgressOptions(ctx, progress, true)
	case runtimeASR:
		status, err = manager.InstallASRWithProgressOptions(ctx, progress, true)
	case runtimeTTS:
		status, err = manager.InstallTTSWithProgressOptions(ctx, progress, true)
	}
	if err != nil {
		return fmt.Errorf("upgrading the %s runtime: %w", kind.label(), err)
	}
	if !status.Ready {
		if status.Error != "" {
			return fmt.Errorf("the %s runtime is still not ready: %s", kind.label(), status.Error)
		}
		return fmt.Errorf("the %s runtime is still not ready", kind.label())
	}
	if kind == runtimeImage {
		if report := diffusersUpgradeReport(status.DiffusersVersion, prevDiffusers, status.DiffusersLatestVersion); report != "" {
			fmt.Println(report)
		}
		if pipelineClass != "" {
			has, err := manager.DiffusersHasPipeline(ctx, pipelineClass)
			if err != nil {
				return err
			}
			if !has {
				return fmt.Errorf("Diffusers %s still lacks the %s pipeline class; the configured package index may lag, check CSGHUB_LITE_PACKAGE_MIRROR or CSGHUB_LITE_PYPI_INDEX_URL", status.DiffusersVersion, pipelineClass)
			}
			fmt.Printf("Diffusers %s provides %s.\n", status.DiffusersVersion, pipelineClass)
		}
	}
	fmt.Printf("The %s runtime is ready.\n", kind.label())
	return nil
}

func runtimeKindsFromArgs(args []string) ([]runtimeKind, error) {
	if len(args) == 0 {
		return []runtimeKind{runtimeImage, runtimeASR, runtimeTTS}, nil
	}
	kind, err := runtimeKindFromArg(args[0])
	if err != nil {
		return nil, err
	}
	return []runtimeKind{kind}, nil
}

func runtimeKindFromArg(arg string) (runtimeKind, error) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "image", "diffusers":
		return runtimeImage, nil
	case "asr":
		return runtimeASR, nil
	case "tts":
		return runtimeTTS, nil
	}
	return "", fmt.Errorf("unknown runtime %q; expected image, asr, or tts", arg)
}

func runtimeManagerFor(kind runtimeKind) (*imagegen.RuntimeManager, error) {
	switch kind {
	case runtimeImage:
		return imagegen.NewRuntimeManager()
	case runtimeASR:
		return imagegen.NewASRRuntimeManager()
	case runtimeTTS:
		return imagegen.NewTTSRuntimeManager()
	}
	return nil, fmt.Errorf("unknown runtime %q", kind)
}

func runtimeStatusFor(ctx context.Context, kind runtimeKind) (imagegen.RuntimeStatus, error) {
	manager, err := runtimeManagerFor(kind)
	if err != nil {
		return imagegen.RuntimeStatus{}, err
	}
	switch kind {
	case runtimeImage:
		return manager.StatusWithVersions(ctx), nil
	case runtimeASR:
		return manager.ASRStatus(ctx), nil
	case runtimeTTS:
		return manager.TTSStatus(ctx), nil
	}
	return imagegen.RuntimeStatus{}, fmt.Errorf("unknown runtime %q", kind)
}

func formatRuntimeStatus(kind runtimeKind, status imagegen.RuntimeStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", kind.label())
	if status.Ready {
		b.WriteString("  ready: yes\n")
	} else {
		b.WriteString("  ready: no\n")
		if status.Error != "" {
			fmt.Fprintf(&b, "  error: %s\n", status.Error)
		}
		if len(status.MissingPackages) > 0 {
			fmt.Fprintf(&b, "  missing packages: %s\n", strings.Join(status.MissingPackages, ", "))
		}
	}
	fmt.Fprintf(&b, "  venv: %s\n", status.VenvDir)
	fmt.Fprintf(&b, "  hardware: %s\n", status.Hardware)
	if status.DiffusersVersion != "" {
		if status.DiffusersLatestVersion != "" {
			fmt.Fprintf(&b, "  diffusers: %s (latest: %s)\n", status.DiffusersVersion, status.DiffusersLatestVersion)
		} else {
			fmt.Fprintf(&b, "  diffusers: %s\n", status.DiffusersVersion)
		}
	}
	return b.String()
}

// diffusersUpgradeReport compares the Diffusers version before and after an
// upgrade; empty means there is nothing to report (the version probe failed).
// latest is the newest version the configured package index offered, empty
// when it could not be determined. An unchanged version is reported as
// unchanged: only a matching probe result can support calling it the latest.
// A latest version older than the installed one is not reported at all: the
// index is simply lagging behind, and suggesting it would read like advice to
// downgrade.
func diffusersUpgradeReport(current, previous, latest string) string {
	if current == "" {
		return ""
	}
	var report string
	switch {
	case previous == "":
		report = "Diffusers installed: " + current
	case previous != current:
		report = fmt.Sprintf("Diffusers upgraded: %s -> %s", previous, current)
	case latest == current:
		report = "Diffusers is already at the latest version: " + current +
			". If a model still fails, the configured package index may lag; check CSGHUB_LITE_PACKAGE_MIRROR or CSGHUB_LITE_PYPI_INDEX_URL."
	default:
		report = "Diffusers version unchanged after upgrade: " + current
	}
	if latest != "" && imagegen.CompareVersions(latest, current) > 0 {
		if previous == current {
			report += fmt.Sprintf(" The latest available Diffusers version is %s; check for dependency conflicts or a lagging mirror (CSGHUB_LITE_PACKAGE_MIRROR or CSGHUB_LITE_PYPI_INDEX_URL).", latest)
		} else {
			report += fmt.Sprintf(" The latest available Diffusers version is %s.", latest)
		}
	}
	return report
}
