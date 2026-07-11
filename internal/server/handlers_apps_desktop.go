package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/apps"
	"github.com/opencsgs/csglite/internal/codexagent"
	"github.com/opencsgs/csglite/internal/zcodeagent"
	"github.com/opencsgs/csglite/pkg/api"
)

var stopZCodeForConfigReloadFunc = stopZCodeForConfigReload

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

	target, err := apps.CodexAppLaunchTarget()
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

func isDesktopAIAppID(appID string) bool {
	return appID == "codex-app" || appID == "zcode"
}

func (s *Server) ensureZCodeLaunchConfig(ctx context.Context, requestedModelID, requestedSource string) (string, error) {
	modelID, modelIDs, err := s.resolveAIAppShellLaunchModels(ctx, "zcode", requestedModelID, requestedSource)
	if err != nil {
		return "", err
	}
	// ZCode persists in-memory provider state while exiting. Stop it completely
	// before editing config.json so the old state cannot overwrite our merge.
	if err := stopZCodeForConfigReloadFunc(); err != nil {
		return "", err
	}
	if err := zcodeagent.SyncConfig(s.localBaseURL(), openClawProviderAPIKey(s.cfg.Token), modelID, modelIDs); err != nil {
		return "", fmt.Errorf("syncing ZCode config: %w", err)
	}
	configPath, err := zcodeagent.ConfigPath()
	if err != nil {
		return "", err
	}
	log.Printf("AI APP zcode: synced local model provider selected_model=%q models=%d path=%s", modelID, len(modelIDs), configPath)
	s.savePreferredAIAppModel("zcode", modelID)
	return modelID, nil
}

func (s *Server) launchZCodeDesktopApp(ctx context.Context) error {
	if !isLocalhostBrowserAccessFromContext(ctx) {
		return fmt.Errorf("ZCode can only be opened from localhost")
	}
	target, err := apps.ZCodeLaunchTarget()
	if err != nil {
		return err
	}

	log.Printf("AI APP zcode: launching desktop target=%s", target)
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.CommandContext(ctx, "open", target).CombinedOutput(); err != nil {
			return desktopLaunchError("ZCode", out, err)
		}
	case "windows":
		if out, err := exec.CommandContext(ctx, "cmd", "/c", "start", "", target).CombinedOutput(); err != nil {
			return desktopLaunchError("ZCode", out, err)
		}
	case "linux":
		cmd := exec.Command(target)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("launching ZCode: %w", err)
		}
		if err := cmd.Process.Release(); err != nil {
			return fmt.Errorf("releasing ZCode process: %w", err)
		}
	default:
		return fmt.Errorf("ZCode is not available on %s", runtime.GOOS)
	}
	return nil
}

func stopZCodeForConfigReload() error {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("osascript", "-e", `if application "ZCode" is running then tell application "ZCode" to quit`).Run()
	case "windows":
		script := `Get-Process ZCode -ErrorAction SilentlyContinue | ForEach-Object { $_.CloseMainWindow() | Out-Null }`
		_ = exec.Command("powershell", "-NoProfile", "-Command", script).Run()
	case "linux":
		_ = exec.Command("pkill", "-TERM", "-x", "ZCode").Run()
		_ = exec.Command("pkill", "-TERM", "-x", "zcode").Run()
	}
	deadline := time.Now().Add(5 * time.Second)
	for zcodeProcessRunning() {
		if time.Now().After(deadline) {
			return fmt.Errorf("ZCode is still running; close it and try Launch again")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func zcodeProcessRunning() bool {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("pgrep", "-x", "ZCode").Run() == nil
	case "windows":
		script := `if (Get-Process ZCode -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }`
		return exec.Command("powershell", "-NoProfile", "-Command", script).Run() == nil
	case "linux":
		return exec.Command("pgrep", "-x", "ZCode").Run() == nil ||
			exec.Command("pgrep", "-x", "zcode").Run() == nil
	default:
		return false
	}
}

func desktopLaunchError(appName string, output []byte, launchErr error) error {
	msg := strings.TrimSpace(string(output))
	if msg == "" {
		msg = launchErr.Error()
	}
	return fmt.Errorf("launching %s: %s", appName, msg)
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
