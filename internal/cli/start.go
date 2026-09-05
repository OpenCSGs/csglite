package cli

import (
	"fmt"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

var (
	serverHealthyForStart         = serverHealthy
	startBackgroundServerForStart = startBackgroundServer
	waitForServerForStart         = waitForServer
)

func newStartCmd() *cobra.Command {
	var listenAddr string
	cmd := &cobra.Command{
		Use:     "start",
		Aliases: []string{"start-service", "start-server", "up"},
		Short:   "Start the background csghub-lite service",
		Long:    "Start the csghub-lite API service in the background. If it is already running, print its address and exit.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if listenAddr != "" {
				cfg.ListenAddr = listenAddr
			}
			return startBackgroundService(cfg)
		},
	}
	cmd.Flags().StringVar(&listenAddr, "listen", "", "address to listen on (default :11435)")
	return cmd
}

func startBackgroundService(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("missing config")
	}

	baseURL := serverBaseURL(cfg)
	if serverHealthyForStart(baseURL) {
		fmt.Printf("csghub-lite service is already running at %s\n", baseURL)
		return nil
	}

	fmt.Println("Starting csghub-lite service...")
	if err := startBackgroundServerForStart(cfg); err != nil {
		return fmt.Errorf("starting service: %w", err)
	}
	if err := waitForServerForStart(baseURL, 15*time.Second); err != nil {
		return err
	}
	fmt.Printf("Started csghub-lite service at %s\n", baseURL)
	return nil
}
