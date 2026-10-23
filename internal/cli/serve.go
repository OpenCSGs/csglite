package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/server"
	"github.com/opencsgs/csglite/internal/upgrade"
	"github.com/spf13/cobra"
)

// alignmentShutdownGrace bounds how long stopping serve waits for the llama.cpp
// runtime alignment to unwind. Cancelling the alignment kills its installer, so
// this is a safety net rather than the normal path.
const alignmentShutdownGrace = 10 * time.Second

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
			stopAlignment := func() {}
			if !desktopMode {
				if err := writePIDFile(os.Getpid()); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not write PID file: %v\n", err)
				}
				defer func() { _ = removePIDFile() }()

				// Keep the llama.cpp runtime in lockstep with this build.
				// Upgrading only replaces the csghub-lite binary, so a runtime
				// installed by an older release keeps its old build and cannot
				// load newer architectures (the Laya decision model, for one).
				// Best-effort and off the request path: it logs and the next
				// start retries instead of failing start-up. Desktop mode skips
				// it: the app bundles its runtime under Contents/Resources,
				// signs it, and replaces both together on update.
				alignContext, cancelAlign := context.WithCancel(context.Background())
				aligned := make(chan struct{})
				go func() {
					defer close(aligned)
					if err := upgrade.AlignLlamaRuntime(alignContext, cfg.TempDir()); err != nil {
						log.Printf("LLAMA: runtime alignment failed: %v", err)
					}
				}()
				// Stopping serve cancels the alignment and waits for it to
				// unwind, so no installer and no temporary directory outlive the
				// server. Cancelling kills the installer, so the wait is short.
				stopAlignment = func() {
					cancelAlign()
					select {
					case <-aligned:
					case <-time.After(alignmentShutdownGrace):
					}
				}
			}
			srv := server.New(cfg, version)
			runContext := cmd.Context()
			if desktopMode && desktopParentPID > 0 {
				var cancel context.CancelFunc
				runContext, cancel = context.WithCancel(runContext)
				defer cancel()
				go cancelWhenParentExits(runContext, desktopParentPID, cancel)
			}
			err = srv.Run(runContext)
			stopAlignment()
			return err
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
