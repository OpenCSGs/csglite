package cli

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/opencsgs/csglite/internal/upgrade"
	"github.com/spf13/cobra"
)

func newUpgradeCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade csghub-lite to the latest version",
		Long: `Check for updates and upgrade csghub-lite to the latest version.

This command checks the latest release from CSGHub and prompts to download
and install the update if a newer version is available.

Use --yes to skip confirmation prompts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(yes)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompts")
	return cmd
}

func runUpgrade(skipConfirm bool) error {
	fmt.Println("Checking for updates...")

	result, err := upgrade.Check()
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	if !result.Available {
		fmt.Printf("csghub-lite is already up to date (version %s)\n", result.CurrentVersion)
		return nil
	}

	fmt.Printf("A new version is available: %s (current: %s)\n", result.LatestVersion, result.CurrentVersion)
	fmt.Println()

	if !skipConfirm {
		fmt.Print("Do you want to upgrade? [Y/n] ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return nil
		}
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer == "n" || answer == "no" {
			fmt.Println("Upgrade cancelled.")
			return nil
		}
	}

	fmt.Printf("Upgrading to version %s...\n", result.LatestVersion)

	progress := func(p upgrade.Progress) {
		if p.Total > 0 {
			pct := float64(p.Downloaded) / float64(p.Total) * 100
			fmt.Printf("\r  Downloading... %.1f%% (%s / %s)", pct, formatBytes(p.Downloaded), formatBytes(p.Total))
		} else {
			fmt.Printf("\r  Downloading... %s", formatBytes(p.Downloaded))
		}
	}

	if err := upgrade.Apply(result, progress); err != nil {
		fmt.Println()
		return fmt.Errorf("upgrade failed: %w", err)
	}

	fmt.Println()
	fmt.Println()
	fmt.Printf("Successfully upgraded to version %s!\n", result.LatestVersion)
	if runtime.GOOS == "windows" {
		fmt.Println("The Windows upgrade helper will finish replacement automatically.")
	} else {
		fmt.Println("Please restart any running csghub-lite services to use the new version.")
	}

	return nil
}
