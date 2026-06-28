package server

import (
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/upgrade"
	"github.com/opencsgs/csglite/pkg/api"
)

// GET /api/upgrade/check - Check for updates
func (s *Server) handleUpgradeCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	result, err := upgrade.NewUpdater(s.version).CheckForUpdate(ctx)
	if err != nil {
		log.Printf("upgrade check: failed to check latest release: %v", err)
		// Return current version info even if check fails
		writeJSON(w, http.StatusOK, api.UpgradeCheckResponse{CurrentVersion: s.version})
		return
	}

	writeJSON(w, http.StatusOK, api.UpgradeCheckResponse{
		CurrentVersion:  result.CurrentVersion,
		LatestVersion:   strings.TrimPrefix(result.LatestVersion, "v"),
		UpdateAvailable: result.Available,
		ReleaseNotes:    result.ReleaseNotes,
		ReleaseURL:      result.DownloadURL,
	})
}

// POST /api/upgrade - Perform upgrade with SSE progress
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sendProgress := func(status, message string, progress int, version string) {
		resp := api.UpgradeProgressResponse{
			Status:   status,
			Progress: progress,
			Message:  message,
			Version:  version,
		}
		writeSSE(w, resp)
	}

	sendError := func(message string) {
		sendProgress("error", message, 0, "")
	}

	sendProgress("checking", "Checking for updates...", 0, "")
	updater := upgrade.NewUpdater(s.version)
	result, err := updater.CheckForUpdate(ctx)
	if err != nil {
		log.Printf("upgrade: failed to check latest release: %v", err)
		sendError("Failed to check for updates: " + err.Error())
		return
	}

	latestVersion := strings.TrimPrefix(result.LatestVersion, "v")
	if !result.Available {
		sendProgress("completed", "Already running the latest version", 100, s.version)
		return
	}

	sendProgress("checking", fmt.Sprintf("Update available: %s", latestVersion), 10, latestVersion)
	sendProgress("downloading", "Downloading update...", 20, latestVersion)

	err = updater.PerformUpgradeWithProgress(ctx, result, func(p upgrade.Progress) {
		if p.Total <= 0 {
			sendProgress("downloading", "Downloading update...", 20, latestVersion)
			return
		}
		percent := int(p.Downloaded * 100 / p.Total)
		sendProgress("downloading", "Downloading update...", 20+percent*60/100, latestVersion)
	})
	if err != nil {
		log.Printf("upgrade: installation failed: %v", err)
		sendError("Failed to install update: " + err.Error())
		return
	}

	message := fmt.Sprintf("Successfully upgraded to %s. Restarting the application...", latestVersion)
	if runtime.GOOS == "windows" {
		message = fmt.Sprintf("Successfully downloaded %s. Applying update and restarting automatically.", latestVersion)
	} else if err := upgrade.RestartAfter(1500 * time.Millisecond); err != nil {
		log.Printf("upgrade: failed to schedule restart: %v", err)
		sendError("Upgrade installed, but failed to restart automatically: " + err.Error())
		return
	}
	sendProgress("completed", message, 100, latestVersion)
}
