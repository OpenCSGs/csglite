package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/apps"
	"github.com/opencsgs/csglite/internal/claudeagent"
	"github.com/opencsgs/csglite/internal/codexagent"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	codexProviderGroup  = "codex"
	claudeProviderGroup = "claude-code"
)

func providerGroupForApp(appID string) (string, bool) {
	switch strings.TrimSpace(appID) {
	case "codex", "codex-app":
		return codexProviderGroup, true
	case "claude-code":
		return claudeProviderGroup, true
	default:
		return "", false
	}
}

func (s *Server) handleAppProviderSwitch(w http.ResponseWriter, r *http.Request) {
	if !isLocalhostBrowserAccess(r) {
		writeError(w, http.StatusForbidden, "AI app providers can only be switched from localhost")
		return
	}

	var req api.AIAppProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	appID := strings.TrimSpace(req.AppID)
	group, supported := providerGroupForApp(appID)
	if !supported {
		writeError(w, http.StatusBadRequest, "provider switching is not supported for this app")
		return
	}
	mode := strings.TrimSpace(req.ProviderMode)
	if mode != apps.ProviderModeNative && mode != apps.ProviderModeOpenCSG {
		writeError(w, http.StatusBadRequest, "provider_mode must be native or opencsg")
		return
	}
	if info, err := s.appManager.Get(r.Context(), appID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	} else if info.Disabled || !info.Supported {
		writeJSON(w, http.StatusConflict, info)
		return
	}

	log.Printf("AI APP %s: provider switch requested group=%s mode=%s", appID, group, mode)
	if err := s.switchAIAppProvider(r.Context(), appID, mode, req.ModelID, req.Source); err != nil {
		log.Printf("AI APP %s: provider switch failed: %v", appID, err)
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "refusing to overwrite") ||
			strings.Contains(err.Error(), "configuration changed") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
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

func (s *Server) switchAIAppProvider(ctx context.Context, appID, mode, requestedModelID, requestedSource string) error {
	group, supported := providerGroupForApp(appID)
	if !supported {
		return fmt.Errorf("provider switching is not supported for %s", appID)
	}
	targetPath, isManaged, restoreLegacy, err := sourceSwitchAdapter(group)
	if err != nil {
		return err
	}
	if mode == apps.ProviderModeNative {
		options := apps.RestoreNativeOptions{}
		if group == codexProviderGroup {
			options.ValidateManaged = s.sourceSwitchManagedValidator(group)
			options.RestoreManaged = codexagent.RestoreNativeConfigData
		}
		_, err := s.sourceSwitches.RestoreNative(group, targetPath, isManaged, restoreLegacy, options)
		return err
	}

	modelID, modelIDs, err := s.resolveAIAppShellLaunchModels(ctx, appID, requestedModelID, requestedSource)
	if err != nil {
		return err
	}
	modelSource, modelIDs, err := s.resolveAIAppModelSource(ctx, modelID, requestedSource)
	if err != nil {
		return err
	}
	serverURL, err := providerScopedBaseURL(s.localBaseURL(), modelSource)
	if err != nil {
		return err
	}
	apply := func() error {
		switch group {
		case codexProviderGroup:
			models := make([]api.ModelInfo, 0, len(modelIDs))
			for _, id := range modelIDs {
				models = append(models, api.ModelInfo{Model: id})
			}
			return codexagent.SyncConfig(
				serverURL,
				openClawProviderAPIKey(s.cfg.Token),
				modelID,
				models,
			)
		case claudeProviderGroup:
			return claudeagent.SyncConfig(serverURL, "csghub-lite", modelID)
		default:
			return fmt.Errorf("unknown provider group %s", group)
		}
	}
	if _, err := s.sourceSwitches.UseOpenCSG(
		group,
		targetPath,
		isManaged,
		apply,
		s.sourceSwitchManagedValidator(group),
	); err != nil {
		return err
	}
	s.savePreferredAIAppSelection(appID, modelID, modelSource)
	return nil
}

func sourceSwitchAdapter(group string) (
	targetPath string,
	isManaged func([]byte) bool,
	restoreLegacy func() error,
	err error,
) {
	switch group {
	case codexProviderGroup:
		targetPath, err = codexagent.ConfigPath()
		return targetPath, codexagent.IsManagedConfigData, codexagent.RemoveManagedConfig, err
	case claudeProviderGroup:
		targetPath, err = claudeagent.SettingsPath()
		return targetPath, claudeagent.IsManagedConfigData, claudeagent.RemoveManagedConfig, err
	default:
		return "", nil, nil, fmt.Errorf("unknown provider group %s", group)
	}
}

func (s *Server) aiAppProviderStatus(appID string) (apps.SourceSwitchStatus, bool, error) {
	group, supported := providerGroupForApp(appID)
	if !supported {
		return apps.SourceSwitchStatus{}, false, nil
	}
	targetPath, isManaged, _, err := sourceSwitchAdapter(group)
	if err != nil {
		return apps.SourceSwitchStatus{}, true, err
	}
	status, err := s.sourceSwitches.Status(group, targetPath, isManaged, s.sourceSwitchManagedValidator(group))
	return status, true, err
}

func (s *Server) sourceSwitchManagedValidator(group string) func([]byte) bool {
	if group != codexProviderGroup {
		return nil
	}
	serverURL := s.localBaseURL()
	allowedURLs := []string{serverURL}
	for _, source := range []string{"local", "cloud"} {
		if value, err := providerScopedBaseURL(serverURL, source); err == nil {
			allowedURLs = append(allowedURLs, value)
		}
	}
	for _, provider := range config.GetProviders() {
		if value, err := providerScopedBaseURL(serverURL, providerSource(provider.ID)); err == nil {
			allowedURLs = append(allowedURLs, value)
		}
	}
	for _, pool := range config.GetProviderPools() {
		if !pool.Enabled {
			continue
		}
		if value, err := providerScopedBaseURL(serverURL, poolSource(pool.ID)); err == nil {
			allowedURLs = append(allowedURLs, value)
		}
	}
	for _, appID := range []string{"codex", "codex-app"} {
		if source := s.preferredAIAppModelSource(appID); source != "" {
			if value, err := providerScopedBaseURL(serverURL, source); err == nil {
				allowedURLs = append(allowedURLs, value)
			}
		}
	}
	apiKey := openClawProviderAPIKey(s.cfg.Token)
	return func(data []byte) bool {
		for _, allowedURL := range allowedURLs {
			if codexagent.MatchesManagedProviderConfigData(data, allowedURL, apiKey) {
				return true
			}
		}
		return false
	}
}

func (s *Server) enrichAIAppProvider(info *api.AIAppInfo) {
	if info == nil {
		return
	}
	group, supported := providerGroupForApp(info.ID)
	if !supported {
		return
	}
	info.ProviderSwitchSupported = true
	info.ProviderGroup = group
	status, _, err := s.aiAppProviderStatus(info.ID)
	if err != nil {
		log.Printf("AI APP %s: provider status failed: %v", info.ID, err)
		info.ProviderMode = apps.ProviderModeNative
		return
	}
	info.ProviderMode = status.Mode
	info.ProviderDrifted = status.Drifted
}
