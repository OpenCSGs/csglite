package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop MODEL",
		Short: "Stop a running model",
		Long:  "Unload a running model from the server to free resources.",
		Args:  cobra.ExactArgs(1),
		RunE:  runStop,
	}
	return cmd
}

func runStop(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	modelID := args[0]
	serverURL := fmt.Sprintf("http://localhost%s", cfg.ListenAddr)
	client := &http.Client{Timeout: 10 * time.Second}

	body, _ := json.Marshal(map[string]string{"model": modelID})
	resp, err := client.Post(serverURL+"/api/stop", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cannot connect to csghub-lite server at %s. Is it running?", serverURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("Stopped model %s\n", modelID)
		return nil
	}

	var errResp struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&errResp)
	return fmt.Errorf("stop failed: %s", errResp.Error)
}
