package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

const aiAppActionWaitTimeout = 25 * time.Minute

func newAppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "Manage AI Apps",
	}
	cmd.AddCommand(newAppsListCmd(), newAppsInstallCmd(), newAppsUninstallCmd())
	return cmd
}

func newAppsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List AI Apps",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppsList()
		},
	}
}

func newAppsInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install APP",
		Short: "Install an AI App",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppsAction(args[0], "install")
		},
	}
}

func newAppsUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall APP",
		Short: "Uninstall a managed AI App",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppsAction(args[0], "uninstall")
		},
	}
}

func appsServerURL() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}
	serverURL, err := ensureAIAppsServer(cfg)
	if err != nil {
		return "", fmt.Errorf("starting server: %w", err)
	}
	return serverURL, nil
}

func runAppsList() error {
	serverURL, err := appsServerURL()
	if err != nil {
		return err
	}
	apps, err := getAIApps(serverURL)
	if err != nil {
		return err
	}

	fmt.Printf("%-16s %-12s %-9s %-8s %s\n", "APP", "STATUS", "INSTALLED", "MANAGED", "VERSION")
	for _, app := range apps {
		version := strings.TrimSpace(app.Version)
		if version == "" {
			version = "-"
		}
		fmt.Printf("%-16s %-12s %-9t %-8t %s\n", app.ID, app.Status, app.Installed, app.Managed, version)
	}
	return nil
}

func runAppsAction(appID, action string) error {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return fmt.Errorf("APP is required")
	}
	serverURL, err := appsServerURL()
	if err != nil {
		return err
	}

	var app api.AIAppInfo
	switch action {
	case "install":
		app, err = requestAIAppInstall(serverURL, appID)
	case "uninstall":
		app, err = requestAIAppUninstall(serverURL, appID)
	default:
		return fmt.Errorf("unknown AI app action %q", action)
	}
	if err != nil {
		return err
	}
	if app.ID == "" {
		app.ID = appID
	}

	result, err := waitForAIAppAction(serverURL, app.ID, action)
	if err != nil {
		return err
	}
	switch action {
	case "install":
		fmt.Printf("%s installed.\n", result.ID)
	case "uninstall":
		fmt.Printf("%s uninstalled.\n", result.ID)
	}
	return nil
}

func requestAIAppUninstall(serverURL, appID string) (api.AIAppInfo, error) {
	return requestAIAppAction(serverURL, "/api/apps/uninstall", appID, "uninstall")
}

func requestAIAppAction(serverURL, path, appID, action string) (api.AIAppInfo, error) {
	body, _ := json.Marshal(api.AIAppActionRequest{AppID: appID})
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(serverURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return api.AIAppInfo{}, fmt.Errorf("starting %s for %s: %w", action, appID, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var info api.AIAppInfo
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &info)
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return api.AIAppInfo{}, fmt.Errorf("starting %s for %s: %s", action, appID, msg)
	}
	if len(respBody) == 0 {
		return api.AIAppInfo{}, fmt.Errorf("decoding %s response: empty response", action)
	}
	if err := json.Unmarshal(respBody, &info); err != nil {
		return api.AIAppInfo{}, fmt.Errorf("decoding %s response: %w", action, err)
	}
	return info, nil
}

func waitForAIAppAction(serverURL, appID, action string) (api.AIAppInfo, error) {
	deadline := time.Now().Add(aiAppActionWaitTimeout)
	lastLine := ""

	for time.Now().Before(deadline) {
		app, err := getAIAppInfo(serverURL, appID)
		if err != nil {
			return api.AIAppInfo{}, err
		}

		line := renderAIAppActionLine(action, app)
		if line != lastLine {
			fmt.Fprintf(os.Stderr, "\r\033[K%s", line)
			lastLine = line
		}

		switch action {
		case "install":
			if app.Status == "installed" {
				fmt.Fprintln(os.Stderr)
				return app, nil
			}
			if app.Status == "failed" {
				fmt.Fprintln(os.Stderr)
				return api.AIAppInfo{}, aiAppActionFailedError(app, "install")
			}
		case "uninstall":
			if !app.Installed || app.Status == "idle" {
				fmt.Fprintln(os.Stderr)
				return app, nil
			}
			if app.Status == "failed" {
				fmt.Fprintln(os.Stderr)
				return api.AIAppInfo{}, aiAppActionFailedError(app, "uninstall")
			}
		}

		time.Sleep(1 * time.Second)
	}

	fmt.Fprintln(os.Stderr)
	return api.AIAppInfo{}, fmt.Errorf("timed out waiting for %s %s after %s", appID, action, aiAppActionWaitTimeout)
}

func renderAIAppActionLine(action string, app api.AIAppInfo) string {
	label := "Installing"
	if action == "uninstall" {
		label = "Uninstalling"
	}
	if app.ProgressMode == "percent" && app.Progress > 0 {
		return fmt.Sprintf("%s %s: %s (%d%%)", label, app.ID, app.Phase, app.Progress)
	}
	if app.Phase != "" {
		return fmt.Sprintf("%s %s: %s", label, app.ID, app.Phase)
	}
	return fmt.Sprintf("%s %s...", label, app.ID)
}

func aiAppActionFailedError(app api.AIAppInfo, action string) error {
	if app.LastError != "" {
		if app.LogPath != "" {
			return fmt.Errorf("%s %s failed: %s (log: %s)", app.ID, action, app.LastError, app.LogPath)
		}
		return fmt.Errorf("%s %s failed: %s", app.ID, action, app.LastError)
	}
	return fmt.Errorf("%s %s failed", app.ID, action)
}
