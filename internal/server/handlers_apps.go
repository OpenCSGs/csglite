package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/apps"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	aiAppRuntimeStatusRunning = "running"
	aiAppRuntimeStatusStopped = "stopped"
	aiAppRuntimeStatusTimeout = 2 * time.Second
)

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.appManager.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.enrichAIApps(r.Context(), apps)
	writeJSON(w, http.StatusOK, api.AIAppsResponse{Apps: apps})
}

func (s *Server) handleAppInstall(w http.ResponseWriter, r *http.Request) {
	var req api.AIAppInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.AppID) == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}
	log.Printf("AI APP %s: install requested", req.AppID)
	info, err := s.appManager.Install(req.AppID)
	if err != nil {
		log.Printf("AI APP %s: install request rejected: %v", req.AppID, err)
		if info.ID != "" && info.Disabled {
			writeJSON(w, http.StatusConflict, info)
			return
		}
		if strings.Contains(err.Error(), "unknown app") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.enrichAIApp(r.Context(), &info)
	writeJSON(w, http.StatusAccepted, info)
}

func (s *Server) handleAppUninstall(w http.ResponseWriter, r *http.Request) {
	var req api.AIAppUninstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.AppID) == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}
	log.Printf("AI APP %s: uninstall requested", req.AppID)
	info, err := s.appManager.Uninstall(req.AppID)
	if err != nil {
		log.Printf("AI APP %s: uninstall request rejected: %v", req.AppID, err)
		if info.ID != "" && info.Disabled {
			writeJSON(w, http.StatusConflict, info)
			return
		}
		if strings.Contains(err.Error(), "unknown app") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.enrichAIApp(r.Context(), &info)
	writeJSON(w, http.StatusAccepted, info)
}

func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request) {
	var req api.AIAppActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.AppID) == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}
	if info, err := s.appManager.Get(r.Context(), req.AppID); err == nil && info.Disabled {
		writeJSON(w, http.StatusConflict, info)
		return
	}

	log.Printf("AI APP %s: start requested model=%q source=%q", req.AppID, req.ModelID, req.Source)
	info, err := s.startAIAppRuntime(r.Context(), req.AppID, req.ModelID, req.Source)
	if err != nil {
		log.Printf("AI APP %s: start failed: %v", req.AppID, err)
		if strings.Contains(err.Error(), "unknown app") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request) {
	var req api.AIAppActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.AppID) == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}

	log.Printf("AI APP %s: stop requested", req.AppID)
	info, err := s.stopAIAppRuntime(r.Context(), req.AppID)
	if err != nil {
		log.Printf("AI APP %s: stop failed: %v", req.AppID, err)
		if strings.Contains(err.Error(), "unknown app") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleAppModelSave(w http.ResponseWriter, r *http.Request) {
	var req api.AIAppActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	appID := strings.TrimSpace(req.AppID)
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}
	if appID == xiaozhiAppID {
		if err := s.saveXiaozhiModelBindings(r.Context(), req.ModelBindings); err != nil {
			log.Printf("AI APP xiaozhi: model binding save failed: %v", err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.syncXiaozhiConfig(); err != nil {
			log.Printf("AI APP xiaozhi: config sync failed: %v", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.TrimSpace(req.ModelID) == "" {
		writeError(w, http.StatusBadRequest, "app_id and model_id are required")
		return
	}
	if appID == "csgclaw" {
		if err := s.saveCSGClawModel(r.Context(), req.ModelID, req.Source); err != nil {
			log.Printf("AI APP csgclaw: model switch failed: %v", err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if status, supported, err := s.aiAppProviderStatus(appID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if supported && status.Mode == apps.ProviderModeOpenCSG {
		if err := s.switchAIAppProvider(r.Context(), appID, apps.ProviderModeOpenCSG, req.ModelID, req.Source); err != nil {
			log.Printf("AI APP %s: OpenCSG model update failed: %v", appID, err)
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.savePreferredAIAppModel(req.AppID, req.ModelID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAppSetPath(w http.ResponseWriter, r *http.Request) {
	var req api.AIAppPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	appID := strings.TrimSpace(req.AppID)
	path := strings.TrimSpace(req.Path)
	if appID == "" || path == "" {
		writeError(w, http.StatusBadRequest, "app_id and path are required")
		return
	}
	if appID != "codex-app" {
		writeError(w, http.StatusBadRequest, "manual install path is only supported for codex-app")
		return
	}

	log.Printf("AI APP %s: manual install path requested path=%q", appID, path)
	if err := apps.SetCodexAppLaunchTarget(path); err != nil {
		log.Printf("AI APP %s: manual install path rejected: %v", appID, err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	info, err := s.appManager.Get(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.enrichAIApp(r.Context(), &info)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleAppOpen(w http.ResponseWriter, r *http.Request) {
	var req api.AIAppOpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.AppID) == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}

	log.Printf("AI APP %s: open requested model=%q source=%q work_dir=%q", req.AppID, req.ModelID, req.Source, req.WorkDir)
	ctx := withLocalhostBrowserAccess(r.Context(), r)
	if isDesktopAIAppID(req.AppID) && !isLocalhostBrowserAccess(r) {
		writeError(w, http.StatusForbidden, "Desktop apps can only be opened from localhost")
		return
	}
	url, err := s.openAIAppURL(ctx, req.AppID, req.ModelID, req.Source, req.WorkDir, aiAppPublicBaseURL(r))
	if err != nil {
		log.Printf("AI APP %s: open failed: %v", req.AppID, err)
		if strings.Contains(err.Error(), "unknown app") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	mode := "url"
	if isDesktopAIAppID(req.AppID) {
		mode = "desktop"
		log.Printf("AI APP %s: desktop launch ready", req.AppID)
	} else {
		log.Printf("AI APP %s: open ready url=%s", req.AppID, redactURLToken(url))
	}
	writeJSON(w, http.StatusOK, api.AIAppOpenResponse{URL: url, Mode: mode})
}

func aiAppPublicBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	if idx := strings.Index(scheme, ","); idx >= 0 {
		scheme = strings.TrimSpace(scheme[:idx])
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if idx := strings.Index(host, ","); idx >= 0 {
		host = strings.TrimSpace(host[:idx])
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func (s *Server) enrichAIApps(ctx context.Context, apps []api.AIAppInfo) {
	for i := range apps {
		s.enrichAIAppListItem(ctx, &apps[i])
	}
}

func (s *Server) enrichAIAppListItem(ctx context.Context, info *api.AIAppInfo) {
	if info == nil {
		return
	}
	s.enrichAIAppRuntime(ctx, info)
	s.enrichAIAppProvider(info)
	if !info.Supported || info.Disabled {
		return
	}
	s.appManager.EnrichCachedLatestVersion(info)
	s.appManager.RefreshLatestVersionAsync(*info)
	if info.ProviderSwitchSupported && info.ProviderMode == apps.ProviderModeNative {
		info.ModelID = ""
		return
	}
	if info.ID == xiaozhiAppID {
		s.enrichXiaozhiModelSlots(ctx, info)
		return
	}
	info.ModelID = s.preferredAIAppModel(info.ID)
}

func (s *Server) enrichAIApp(ctx context.Context, info *api.AIAppInfo) {
	if info == nil {
		return
	}
	s.enrichAIAppRuntime(ctx, info)
	s.enrichAIAppProvider(info)
	if !info.Supported || info.Disabled {
		return
	}

	s.appManager.EnrichLatestVersion(ctx, info)
	if info.ProviderSwitchSupported && info.ProviderMode == apps.ProviderModeNative {
		info.ModelID = ""
		return
	}
	if info.ID == xiaozhiAppID {
		s.enrichXiaozhiModelSlots(ctx, info)
		return
	}

	var (
		modelID string
		err     error
	)

	switch info.ID {
	case "claude-code", "open-code", "codex", "pi", "zcode":
		modelID, _, err = s.resolveAIAppShellLaunchModels(ctx, info.ID, "", "")
	case "openclaw", "csgclaw":
		preferred := s.preferredAIAppModel(info.ID)
		modelID, _, err = s.resolveAIAppLaunchModels(ctx, preferred, "")
		if err != nil && preferred != "" {
			// Don't clear preference on lookup failure - the model might be from
			// a third-party provider whose API is temporarily unavailable.
			// Fall back to default model without clearing the preference.
			modelID, _, err = s.resolveAIAppLaunchModels(ctx, "", "")
		}
	default:
		return
	}

	if err == nil {
		info.ModelID = modelID
	}
}

func (s *Server) enrichAIAppRuntime(ctx context.Context, info *api.AIAppInfo) {
	if !aiAppSupportsRuntimeLifecycle(info.ID) {
		return
	}

	info.RuntimeSupported = true
	info.RuntimeStatus = aiAppRuntimeStatusStopped
	if !info.Supported || info.Disabled || !info.Installed {
		return
	}

	statusCtx, cancel := context.WithTimeout(ctx, aiAppRuntimeStatusTimeout)
	defer cancel()
	running, err := s.aiAppRuntimeRunning(statusCtx, info.ID)
	if err != nil {
		log.Printf("AI APP %s: runtime status check failed: %v", info.ID, err)
		return
	}
	info.RuntimeRunning = running
	if running {
		info.RuntimeStatus = aiAppRuntimeStatusRunning
	}
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}

	recent, err := s.appManager.RecentLogs(appID, 100)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	for _, line := range recent {
		fmt.Fprintf(w, "data: %s\n\n", trimNewline(line))
	}
	flusher.Flush()

	ch, err := s.appManager.SubscribeLogs(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer s.appManager.UnsubscribeLogs(appID, ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", trimNewline(line))
			flusher.Flush()
		}
	}
}
