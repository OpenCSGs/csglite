package cli

import (
	"fmt"
	"time"

	clusterpublic "github.com/opencsgs/csglite/ee/cluster/public"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

func newClusterLoginTokenCmd() *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use: "login-token", Short: "Generate a public management login token (revokes the previous token and all sessions)", Args: cobra.NoArgs,
		Long: "Generate a management token on this Lite instance. Paste it into the public HTTPS login page. The token is printed once; only its hash is stored. Running this command again revokes the previous token and all browser sessions. No server restart is required. Use --ttl 168h for a token valid for one week; zero (the default) means no expiry. Existing sessions cannot outlive the token. This is separate from node join tokens and inference API keys.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			token, err := clusterpublic.GenerateAdminTokenWithTTL(clusterpublic.AdminTokenPath(cfg.StorageDir()), ttl)
			if err != nil {
				return fmt.Errorf("generate management login token: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), token)
			return err
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "Token validity (e.g. 168h for one week); 0 means no expiry")
	return cmd
}
