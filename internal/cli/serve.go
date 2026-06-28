package cli

import (
	"fmt"
	"os"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/server"
	"github.com/spf13/cobra"
)

func newServeCmd(version string) *cobra.Command {
	var listenAddr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the csghub-lite API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if listenAddr != "" {
				cfg.ListenAddr = listenAddr
			}
			if err := writePIDFile(os.Getpid()); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not write PID file: %v\n", err)
			}
			defer func() { _ = removePIDFile() }()
			srv := server.New(cfg, version)
			return srv.Run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", "", "address to listen on (default :11435)")
	return cmd
}
