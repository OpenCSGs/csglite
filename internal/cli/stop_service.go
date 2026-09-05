package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func newStopServiceCmd() *cobra.Command {
	return &cobra.Command{
		Use:        "stop-service",
		Short:      "Stop the background csghub-lite service",
		Long:       "Stop the background csghub-lite API service started by 'serve' or auto-started by client commands.\n\nDeprecated: use 'csghub-lite stop' instead.",
		Deprecated: "use \"csghub-lite stop\" instead",
		Args:       cobra.NoArgs,
		RunE:       runStopService,
	}
}

func runStopService(cmd *cobra.Command, args []string) error {
	return stopBackgroundService(false)
}

func stopBackgroundServiceIfRunning() error {
	return stopBackgroundService(true)
}

func stopBackgroundService(ignoreIfStopped bool) error {
	baseURL, hasBaseURL := currentServerBaseURL()
	if hasBaseURL && serverHealthy(baseURL) {
		fmt.Println("Stopping csghub-lite service...")
		if err := requestServerShutdown(baseURL); err != nil {
			return err
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !serverHealthy(baseURL) {
				_ = removePIDFile()
				fmt.Println("Stopped csghub-lite service")
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("service did not stop within 5s")
	}

	pid := ServerPID()
	if pid <= 0 {
		if ignoreIfStopped {
			return nil
		}
		return fmt.Errorf("no running csghub-lite service found")
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = removePIDFile()
		if ignoreIfStopped {
			return nil
		}
		return fmt.Errorf("finding server process %d: %w", pid, err)
	}

	if !processExists(proc) {
		_ = removePIDFile()
		if ignoreIfStopped {
			return nil
		}
		fmt.Printf("csghub-lite service is already stopped (pid %d)\n", pid)
		return nil
	}

	fmt.Printf("Stopping csghub-lite service (pid %d)...\n", pid)
	if err := stopProcess(proc); err != nil {
		if !processExists(proc) {
			_ = removePIDFile()
			fmt.Printf("csghub-lite service is already stopped (pid %d)\n", pid)
			return nil
		}
		return fmt.Errorf("stopping service pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		serverDown := true
		if hasBaseURL {
			serverDown = !serverHealthy(baseURL)
		}
		if serverDown && !processExists(proc) {
			_ = removePIDFile()
			fmt.Printf("Stopped csghub-lite service (pid %d)\n", pid)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	if hasBaseURL && !serverHealthy(baseURL) {
		_ = removePIDFile()
		fmt.Printf("Stopped csghub-lite service (pid %d)\n", pid)
		return nil
	}

	return fmt.Errorf("service pid %d did not stop within 5s", pid)
}

// currentServerBaseURL returns the configured server URL when config can be
// loaded. Service stop treats a missing URL as optional so it can still fall
// back to the PID file.
func currentServerBaseURL() (string, bool) {
	baseURL, err := currentServerURL()
	if err != nil {
		return "", false
	}
	return baseURL, true
}

func requestServerShutdown(baseURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	body, _ := json.Marshal(map[string]bool{"shutdown": true})
	resp, err := client.Post(baseURL+"/api/shutdown", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("requesting server shutdown: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shutdown request failed: HTTP %d", resp.StatusCode)
	}
	return nil
}
