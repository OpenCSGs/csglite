package cli

import (
	"fmt"

	clusterpublic "github.com/opencsgs/csglite/ee/cluster/public"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

func newClusterLoginTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use: "login-token", Short: "Generate a public management login token (revokes the previous token and all sessions)", Args: cobra.NoArgs,
		Long: "Generate a management token on this Lite instance. Paste it into the public HTTPS login page. The token is printed once; only its hash is stored. Running this command again revokes the previous token and all browser sessions. No server restart is required. This is separate from node join tokens and inference API keys.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			token, err := clusterpublic.GenerateAdminToken(clusterpublic.AdminTokenPath(cfg.StorageDir()))
			if err != nil {
				return fmt.Errorf("generate management login token: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), token)
			return err
		},
	}
}
