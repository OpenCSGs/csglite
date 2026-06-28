package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/opencsgs/csglite/internal/codexagent"
	"github.com/opencsgs/csglite/pkg/api"
)

const codexAppLaunchTargetFile = "launch-target"

func isLocalhostBrowserAccess(r *http.Request) bool {
	if r == nil {
		return false
	}
	host := browserAccessHost(r)
	return isLoopbackHost(host)
}

func browserAccessHost(r *http.Request) string {
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if idx := strings.Index(host, ","); idx >= 0 {
		host = strings.TrimSpace(host[:idx])
	}
	hostname, _, err := net.SplitHostPort(host)
	if err != nil {
		return strings.TrimSpace(host)
	}
	return strings.TrimSpace(hostname)
}

func (s *Server) ensureCodexAppLaunchConfig(ctx context.Context, requestedModelID, requestedSource string) (string, error) {
	modelID, modelIDs, err := s.resolveAIAppShellLaunchModels(ctx, "codex-app", requestedModelID, requestedSource)
	if err != nil {
		return "", err
	}

	models := make([]api.ModelInfo, 0, len(modelIDs))
	for _, id := range modelIDs {
		models = append(models, api.ModelInfo{Model: id})
	}
	serverURL := s.localBaseURL()
	if err := codexagent.SyncConfig(serverURL, openClawProviderAPIKey(s.cfg.Token), modelID, models); err != nil {
		return "", fmt.Errorf("syncing Codex config: %w", err)
	}

	configPath, err := codexagent.ConfigPath()
	if err != nil {
		return "", err
	}
	log.Printf("AI APP codex-app: synced shared config model=%q path=%s", modelID, configPath)
	s.savePreferredAIAppModel("codex-app", modelID)
	return modelID, nil
}

func (s *Server) launchCodexDesktopApp(ctx context.Context) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return fmt.Errorf("Codex App is only available on macOS and Windows")
	}
	if !isLocalhostBrowserAccessFromContext(ctx) {
		return fmt.Errorf("Codex App can only be opened from localhost")
	}

	target, err := codexAppLaunchTarget()
	if err != nil {
		return err
	}

	log.Printf("AI APP codex-app: launching desktop target=%s", target)
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.CommandContext(ctx, "open", target)
		if out, err := cmd.CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("launching Codex App: %s", msg)
		}
	case "windows":
		cmd := exec.CommandContext(ctx, "cmd", "/c", "start", "", target)
		if out, err := cmd.CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("launching Codex App: %s", msg)
		}
	default:
		return fmt.Errorf("Codex App is only available on macOS and Windows")
	}
	return nil
}

type localhostAccessContextKey struct{}

func withLocalhostBrowserAccess(ctx context.Context, r *http.Request) context.Context {
	if r == nil {
		return ctx
	}
	if !isLocalhostBrowserAccess(r) {
		return ctx
	}
	return context.WithValue(ctx, localhostAccessContextKey{}, true)
}

func isLocalhostBrowserAccessFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, ok := ctx.Value(localhostAccessContextKey{}).(bool)
	return ok && allowed
}

func codexAppLaunchTarget() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("Codex App is installed, but the user home directory was not found")
	}

	targetPath := filepath.Join(home, ".local", "share", "codex-app", codexAppLaunchTargetFile)
	data, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("Codex App is installed, but its launch target was not found at %s", targetPath)
		}
		return "", fmt.Errorf("reading Codex App launch target: %w", err)
	}
	target := strings.TrimSpace(string(data))
	if target == "" {
		return "", fmt.Errorf("Codex App launch target is empty")
	}
	if _, err := os.Stat(target); err != nil {
		return "", fmt.Errorf("Codex App launch target is missing: %s", target)
	}
	return target, nil
}
