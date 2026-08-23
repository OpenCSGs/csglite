package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "stop",
		Aliases: []string{"stop-server", "down"},
		Short:   "Stop the background csghub-lite service",
		Long:    "Stop the background csghub-lite API service started by 'serve' or auto-started by client commands.\n\nUse 'csghub-lite stop model' to unload a running model without stopping the service.",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runStop,
	}
	cmd.AddCommand(newStopModelCmd())
	return cmd
}

func newStopModelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "model [MODEL]",
		Short: "Stop a running model",
		Long:  "Unload a running model from the server to free resources. If MODEL is omitted, stop all currently running models.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runStopModel,
	}
}

func runStop(cmd *cobra.Command, args []string) error {
	if len(args) == 1 {
		return stopRunningModelByName(args[0])
	}
	return stopBackgroundService(false)
}

func runStopModel(cmd *cobra.Command, args []string) error {
	if len(args) == 1 {
		return stopRunningModelByName(args[0])
	}
	return stopAllRunningModels()
}

func stopRunningModelByName(modelID string) error {
	serverURL, err := currentServerURL()
	if err != nil {
		return err
	}
	return stopRunningModel(serverURL, modelID)
}

func stopAllRunningModels() error {
	serverURL, err := currentServerURL()
	if err != nil {
		return err
	}
	return stopAllRunningModelsAt(serverURL)
}

func stopAllRunningModelsAt(serverURL string) error {
	models, err := listRunningModels(serverURL)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		fmt.Println("No models currently running.")
		return nil
	}

	var lastErr error
	stopped := 0
	for _, model := range models {
		id := runningModelID(model)
		if id == "" {
			fmt.Fprintln(os.Stderr, "warning: skipping model with empty id")
			continue
		}
		if err := stopRunningModel(serverURL, id); err != nil {
			lastErr = err
			continue
		}
		stopped++
	}
	if lastErr != nil {
		if stopped == 0 {
			return lastErr
		}
		return fmt.Errorf("stopped %d model(s), but another stop failed: %w", stopped, lastErr)
	}
	return nil
}

// currentServerURL loads the configured local server URL. Callers that stop
// models require this URL and must surface config errors.
func currentServerURL() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}
	return serverBaseURL(cfg), nil
}

func listRunningModels(serverURL string) ([]api.RunningModel, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(strings.TrimRight(serverURL, "/") + "/api/ps")
	if err != nil {
		return nil, fmt.Errorf("cannot connect to csghub-lite server at %s. Is it running?", serverURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing running models failed: HTTP %d", resp.StatusCode)
	}

	var psResp api.PsResponse
	if err := json.NewDecoder(resp.Body).Decode(&psResp); err != nil {
		return nil, fmt.Errorf("decoding running models: %w", err)
	}
	return psResp.Models, nil
}

func runningModelID(model api.RunningModel) string {
	if id := strings.TrimSpace(model.Model); id != "" {
		return id
	}
	return strings.TrimSpace(model.Name)
}

func stopRunningModel(serverURL, modelID string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	body, err := json.Marshal(api.StopRequest{Model: modelID})
	if err != nil {
		return fmt.Errorf("encoding stop request: %w", err)
	}
	resp, err := client.Post(strings.TrimRight(serverURL, "/")+"/api/stop", "application/json", bytes.NewReader(body))
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
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp.Error == "" {
		return fmt.Errorf("stop failed: HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("stop failed: %s", errResp.Error)
}
