package cli

import (
	"github.com/spf13/cobra"
)

func NewRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "csghub-lite",
		Short: "Run large language models locally with CSGHub",
		Long: `csghub-lite is a lightweight tool for running large language models locally.
It downloads models from the CSGHub platform and provides local inference,
an interactive chat interface, and an OpenAI-compatible REST API.

Visit https://opencsg.com for advanced features, enterprise solutions,
and the full CSGHub platform.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(
		newServeCmd(version),
		newAppsCmd(),
		newLaunchCmd(),
		newRunCmd(),
		newChatCmd(),
		newPullCmd(),
		newListCmd(),
		newShowCmd(),
		newPsCmd(),
		newStopCmd(),
		newStopServiceCmd(),
		newRestartCmd(),
		newRmCmd(),
		newLoginCmd(),
		newSearchCmd(),
		newConfigCmd(),
		newUninstallCmd(),
		newUpgradeCmd(),
	)

	return cmd
}
