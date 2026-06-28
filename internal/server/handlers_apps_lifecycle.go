package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
)

func aiAppSupportsRuntimeLifecycle(appID string) bool {
	switch strings.TrimSpace(appID) {
	case "openclaw", "csgclaw":
		return true
	default:
		return false
	}
}

func (s *Server) startAIAppRuntime(ctx context.Context, appID, modelID, modelSource string) (api.AIAppInfo, error) {
	info, err := s.appManager.Get(ctx, appID)
	if err != nil {
		return api.AIAppInfo{}, err
	}
	if err := validateAIAppRuntimeAction(info); err != nil {
		return api.AIAppInfo{}, err
	}

	switch appID {
	case "openclaw":
		if _, err := s.openClawChatURL(ctx, modelID, modelSource); err != nil {
			return api.AIAppInfo{}, err
		}
	case "csgclaw":
		if _, err := s.openCSGClawURL(ctx, modelID, modelSource); err != nil {
			return api.AIAppInfo{}, err
		}
	default:
		return api.AIAppInfo{}, fmt.Errorf("%s does not support start/stop actions", appID)
	}

	info, err = s.appManager.Get(ctx, appID)
	if err != nil {
		return api.AIAppInfo{}, err
	}
	s.enrichAIApp(ctx, &info)
	return info, nil
}

func (s *Server) stopAIAppRuntime(ctx context.Context, appID string) (api.AIAppInfo, error) {
	info, err := s.appManager.Get(ctx, appID)
	if err != nil {
		return api.AIAppInfo{}, err
	}
	if err := validateAIAppRuntimeAction(info); err != nil {
		return api.AIAppInfo{}, err
	}

	switch appID {
	case "openclaw":
		binary, err := resolveAIAppLaunchBinary([]string{"openclaw"})
		if err != nil {
			return api.AIAppInfo{}, fmt.Errorf("OpenClaw is installed, but its launch command was not found on PATH")
		}
		if err := stopOpenClawGateway(ctx, binary); err != nil {
			return api.AIAppInfo{}, err
		}
	case "csgclaw":
		binary, err := resolveAIAppLaunchBinary([]string{"csgclaw"})
		if err != nil {
			return api.AIAppInfo{}, fmt.Errorf("CSGClaw is installed, but its launch command was not found on PATH")
		}
		if err := stopCSGClawServe(binary); err != nil {
			return api.AIAppInfo{}, err
		}
	default:
		return api.AIAppInfo{}, fmt.Errorf("%s does not support start/stop actions", appID)
	}

	info, err = s.appManager.Get(ctx, appID)
	if err != nil {
		return api.AIAppInfo{}, err
	}
	s.enrichAIApp(ctx, &info)
	return info, nil
}

func validateAIAppRuntimeAction(info api.AIAppInfo) error {
	if info.Disabled || !info.Supported {
		return fmt.Errorf("%s is currently disabled in AI Apps", info.ID)
	}
	if !info.Installed {
		return fmt.Errorf("%s is not installed yet", info.ID)
	}
	if !aiAppSupportsRuntimeLifecycle(info.ID) {
		return fmt.Errorf("%s does not support start/stop actions", info.ID)
	}
	return nil
}

func (s *Server) aiAppRuntimeRunning(ctx context.Context, appID string) (bool, error) {
	switch appID {
	case "openclaw":
		binary, err := resolveAIAppLaunchBinary([]string{"openclaw"})
		if err != nil {
			return false, nil
		}
		return openClawGatewayRunning(ctx, binary), nil
	case "csgclaw":
		return csgclawReachable(), nil
	default:
		return false, fmt.Errorf("%s does not support start/stop actions", appID)
	}
}
