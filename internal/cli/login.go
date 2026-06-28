package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var token string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Set the CSGHub access token",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			if token == "" {
				fmt.Print("Enter your CSGHub access token: ")
				reader := bufio.NewReader(os.Stdin)
				input, err := reader.ReadString('\n')
				if err != nil {
					return fmt.Errorf("reading token: %w", err)
				}
				token = strings.TrimSpace(input)
			}

			if token == "" {
				return fmt.Errorf("token cannot be empty")
			}

			cfg.Token = token
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			warnIfTokenSyncFailed(cfg)

			fmt.Println("Token saved successfully.")
			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "access token (interactive prompt if omitted)")
	return cmd
}
