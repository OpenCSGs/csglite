package cli

import (
	"fmt"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/spf13/cobra"
)

var (
	stopBackgroundServiceForRestart = stopBackgroundServiceIfRunning
	startBackgroundServerForRestart = startBackgroundServer
	waitForServerForRestart         = waitForServer
)

func newRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "restart",
		Aliases: []string{"restart-service", "restart-server", "reload"},
		Short:   "Restart the background csghub-lite service",
		Long:    "Restart the background csghub-lite API service, starting it if it is not already running.",
		Args:    cobra.NoArgs,
		RunE:    runRestart,
	}
	return cmd
}

func runRestart(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	return restartBackgroundService(cfg)
}

func restartBackgroundService(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("missing config")
	}

	baseURL := serverBaseURL(cfg)
	fmt.Println("Restarting csghub-lite service...")
	if err := stopBackgroundServiceForRestart(); err != nil {
		return fmt.Errorf("stopping existing service: %w", err)
	}
	if err := startBackgroundServerForRestart(cfg); err != nil {
		return fmt.Errorf("starting service: %w", err)
	}
	if err := waitForServerForRestart(baseURL, 15*time.Second); err != nil {
		return err
	}
	fmt.Printf("Restarted csghub-lite service at %s\n", baseURL)
	return nil
}
