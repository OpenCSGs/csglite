package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/server"
	"github.com/spf13/cobra"
)

func newServeCmd(version string) *cobra.Command {
	var listenAddr string
	var openAIStreamDefault bool
	var desktopMode bool
	var desktopParentPID int

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
			if desktopMode {
				cfg.DesktopMode = true
				cfg.ListenAddrOverride = "127.0.0.1:0"
				cfg.DesktopAPIAddr = config.DefaultDesktopAPIAddr
				cfg.DesktopAPIBindAddr = config.DefaultDesktopAPIBindAddr
				cfg.DesktopToken, err = randomIdentifier(32)
				if err != nil {
					return fmt.Errorf("generating desktop bootstrap token: %w", err)
				}
				cfg.DesktopSessionToken, err = randomIdentifier(32)
				if err != nil {
					return fmt.Errorf("generating desktop session token: %w", err)
				}
				cfg.DesktopControlToken, err = randomIdentifier(32)
				if err != nil {
					return fmt.Errorf("generating desktop control token: %w", err)
				}
				cfg.DesktopInstanceID, err = randomIdentifier(16)
				if err != nil {
					return fmt.Errorf("generating desktop instance ID: %w", err)
				}
			}
			if cmd.Flags().Changed("openai-stream-default") {
				cfg.OpenAIStreamDefault = openAIStreamDefault
			}
			if !desktopMode {
				if err := writePIDFile(os.Getpid()); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not write PID file: %v\n", err)
				}
				defer func() { _ = removePIDFile() }()
			}
			srv := server.New(cfg, version)
			runContext := cmd.Context()
			if desktopMode && desktopParentPID > 0 {
				var cancel context.CancelFunc
				runContext, cancel = context.WithCancel(runContext)
				defer cancel()
				go cancelWhenParentExits(runContext, desktopParentPID, cancel)
			}
			return srv.Run(runContext)
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", "", "address to listen on (default :11435)")
	cmd.Flags().BoolVar(&openAIStreamDefault, "openai-stream-default", false, "stream OpenAI chat completions by default when stream is omitted")
	cmd.Flags().BoolVar(&desktopMode, "desktop", false, "run as a desktop-managed loopback sidecar")
	cmd.Flags().IntVar(&desktopParentPID, "desktop-parent-pid", 0, "exit when the managing desktop process exits")
	return cmd
}

func randomIdentifier(byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func cancelWhenParentExits(ctx context.Context, parentPID int, cancel context.CancelFunc) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !processAlive(parentPID) {
				cancel()
				return
			}
		}
	}
}
