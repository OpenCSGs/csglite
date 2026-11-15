package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	clusterpublic "github.com/opencsgs/csglite/ee/cluster/public"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

func newClusterLoginTokenCmd() *cobra.Command {
	var ttl time.Duration
	var name string
	cmd := &cobra.Command{
		Use: "login-token", Short: "Generate an additional public management login token", Args: cobra.NoArgs,
		Long: "Add an independent management token on this Lite instance. Paste it into the public HTTPS login page. The token is printed once; only its hash is stored. Existing tokens and browser sessions remain valid. No server restart is required. Use --name to label a token and --ttl 168h for one week; zero (the default) means no expiry. Sessions cannot outlive their own token. Use login-token list and login-token revoke <id> to manage tokens. These administrator credentials are separate from node join tokens and inference API keys.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			_, token, err := clusterpublic.CreateAdminToken(clusterpublic.AdminTokenPath(cfg.StorageDir()), name, ttl)
			if err != nil {
				return fmt.Errorf("generate management login token: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), token)
			return err
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "Token validity (e.g. 168h for one week); 0 means no expiry")
	cmd.Flags().StringVar(&name, "name", "", "Optional label to identify this token")
	cmd.AddCommand(newClusterLoginTokenListCmd(), newClusterLoginTokenRevokeCmd())
	return cmd
}

func newClusterLoginTokenListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "list", Short: "List management token IDs and expiry without revealing secrets", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			tokens, err := clusterpublic.ListAdminTokens(clusterpublic.AdminTokenPath(cfg.StorageDir()))
			if err != nil {
				return fmt.Errorf("list management login tokens: %w", err)
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(tokens)
			}
			out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(out, "ID\tNAME\tCREATED\tEXPIRES\tSTATUS"); err != nil {
				return err
			}
			for _, token := range tokens {
				name, expires, status := token.Name, "never", "active"
				if name == "" {
					name = "—"
				}
				if token.ExpiresAt != nil {
					expires = token.ExpiresAt.Format(time.RFC3339)
					if !time.Now().Before(*token.ExpiresAt) {
						status = "expired"
					}
				}
				if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", token.ID, name, token.CreatedAt.Format(time.RFC3339), expires, status); err != nil {
					return err
				}
			}
			return out.Flush()
		}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print token metadata as JSON")
	return cmd
}

func newClusterLoginTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{Use: "revoke <id>", Short: "Revoke one management token and only its browser sessions", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			deleted, err := clusterpublic.RevokeAdminToken(clusterpublic.AdminTokenPath(cfg.StorageDir()), args[0])
			if err != nil {
				return fmt.Errorf("revoke management login token: %w", err)
			}
			if !deleted {
				return fmt.Errorf("management login token not found")
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Management login token revoked.")
			return err
		}}
}
