package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/dshagent"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	dshWebPort          = 3080
	dshWebHost          = "127.0.0.1"
	dshWebProfile       = "web"
	dshGatewayWait      = 2 * time.Minute
	dshGatewayLogName   = "dsh-web.log"
	dshGatewayPIDName   = "dsh-web.pid"
)

func aiAppSupportsRuntimeLifecycle(appID string) bool {
	switch strings.TrimSpace(appID) {
	case "openclaw", "xiaozhi", "dsh":
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
	case "xiaozhi":
		if err := s.prepareXiaozhiLaunch(ctx); err != nil {
			return api.AIAppInfo{}, err
		}
		if err := s.appManager.StartXiaozhi(ctx); err != nil {
			return api.AIAppInfo{}, err
		}
	case "dsh":
		if err := s.startDshRuntime(ctx, modelID, modelSource); err != nil {
			return api.AIAppInfo{}, err
		}
	default:
		return api.AIAppInfo{}, fmt.Errorf("%s does not support start/stop actions", appID)
	}

	info, err = s.appManager.Get(ctx, appID)
	if err != nil {
		return api.AIAppInfo{}, err
	}
	s.invalidateAIAppRuntimeCache(appID)
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
		binary, err := resolveAIAppLaunchBinary("openclaw", []string{"openclaw"})
		if err != nil {
			return api.AIAppInfo{}, fmt.Errorf("OpenClaw is installed, but its launch command was not found on PATH")
		}
		if err := stopOpenClawGateway(ctx, binary); err != nil {
			return api.AIAppInfo{}, err
		}
	case "csgclaw":
		binary, err := resolveAIAppLaunchBinary("csgclaw", []string{"csgclaw"})
		if err != nil {
			return api.AIAppInfo{}, fmt.Errorf("CSGClaw is installed, but its launch command was not found on PATH")
		}
		if err := stopCSGClawServe(binary); err != nil {
			return api.AIAppInfo{}, err
		}
	case "xiaozhi":
		if err := s.appManager.StopXiaozhi(ctx); err != nil {
			return api.AIAppInfo{}, err
		}
	case "dsh":
		if err := stopDshRuntime(ctx); err != nil {
			return api.AIAppInfo{}, err
		}
	default:
		return api.AIAppInfo{}, fmt.Errorf("%s does not support start/stop actions", appID)
	}

	info, err = s.appManager.Get(ctx, appID)
	if err != nil {
		return api.AIAppInfo{}, err
	}
	s.invalidateAIAppRuntimeCache(appID)
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
		binary, err := resolveAIAppLaunchBinary("openclaw", []string{"openclaw"})
		if err != nil {
			return false, nil
		}
		return openClawGatewayRunning(ctx, binary), nil
	case "csgclaw":
		return csgclawReachable(), nil
	case "xiaozhi":
		return s.appManager.XiaozhiRunning(ctx)
	case "dsh":
		return dshWebReachable(), nil
	default:
		return false, fmt.Errorf("%s does not support start/stop actions", appID)
	}
}

// --- dsh runtime lifecycle ---

func (s *Server) startDshRuntime(ctx context.Context, modelID, modelSource string) error {
	if dshWebReachable() {
		log.Printf("AI APP dsh: web server already running on %s:%d", dshWebHost, dshWebPort)
		return nil
	}

	binary, err := resolveAIAppLaunchBinary("dsh", []string{"dsh"})
	if err != nil {
		return fmt.Errorf("DeepSeek Harness is installed, but the dsh command was not found on PATH")
	}

	// Sync config so dsh knows about csghub-lite as the model provider.
	if err := s.syncDshConfig(ctx, modelID, modelSource); err != nil {
		return err
	}

	logPath, pidPath, err := dshGatewayPaths()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening dsh web log: %w", err)
	}

	cmd := exec.Command(binary, "--profile", dshWebProfile, "--no-open", "--port", strconv.Itoa(dshWebPort))
	cmd.Env = envWithOverrides(nil)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("starting dsh web server: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		log.Printf("AI APP dsh: writing web pid failed: %v", err)
	}
	log.Printf("AI APP dsh: web process started pid=%d log=%s", cmd.Process.Pid, logPath)
	_ = logFile.Close()

	// Release the process so it survives this request.
	_ = cmd.Process.Release()

	addr := net.JoinHostPort(dshWebHost, strconv.Itoa(dshWebPort))
	if err := waitForPort(addr, dshGatewayWait); err != nil {
		return fmt.Errorf("dsh web server did not become ready: %w", err)
	}
	log.Printf("AI APP dsh: web server ready on %s", dshWebURL())
	return nil
}

func (s *Server) syncDshConfig(ctx context.Context, modelID, modelSource string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		modelID = strings.TrimSpace(s.preferredAIAppModel("dsh"))
	}
	resolvedModel, _, err := s.resolveAIAppLaunchModels(ctx, modelID, modelSource)
	if err != nil {
		return err
	}
	resolvedSource, modelIDs, err := s.resolveAIAppModelSource(ctx, resolvedModel, modelSource)
	if err != nil {
		return err
	}
	serverURL, err := providerScopedBaseURL(s.localBaseURL(), resolvedSource)
	if err != nil {
		return err
	}
	models := make([]api.ModelInfo, 0, len(modelIDs))
	for _, id := range modelIDs {
		models = append(models, api.ModelInfo{Model: id})
	}
	if err := dshagent.SyncConfig(serverURL, openClawProviderAPIKey(config.Get().Token), resolvedModel, models); err != nil {
		return fmt.Errorf("syncing dsh config: %w", err)
	}
	return nil
}

func stopDshRuntime(ctx context.Context) error {
	addr := net.JoinHostPort(dshWebHost, strconv.Itoa(dshWebPort))
	if !portReachable(addr) {
		removeDshPID()
		return nil
	}

	killDshPID()
	if waitForPortStop(addr, 3*time.Second) == nil {
		removeDshPID()
		return nil
	}
	killDshListeners()
	if waitForPortStop(addr, 5*time.Second) == nil {
		removeDshPID()
		return nil
	}
	return fmt.Errorf("dsh web server did not stop in time")
}

func dshWebURL() string {
	return fmt.Sprintf("http://%s/", net.JoinHostPort(dshWebHost, strconv.Itoa(dshWebPort)))
}

func dshWebReachable() bool {
	addr := net.JoinHostPort(dshWebHost, strconv.Itoa(dshWebPort))
	return portReachable(addr)
}

func portReachable(hostPort string) bool {
	conn, err := net.DialTimeout("tcp", hostPort, 750*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForPort(hostPort string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if portReachable(hostPort) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("port %s did not become ready in time", hostPort)
}

func waitForPortStop(hostPort string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !portReachable(hostPort) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("port %s did not stop in time", hostPort)
}

func dshGatewayPaths() (logPath, pidPath string, err error) {
	appHome, err := config.AppHome()
	if err != nil {
		return "", "", err
	}
	logDir := filepath.Join(appHome, "apps", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", "", fmt.Errorf("creating dsh log dir: %w", err)
	}
	return filepath.Join(logDir, dshGatewayLogName), filepath.Join(logDir, dshGatewayPIDName), nil
}

func killDshPID() {
	_, pidPath, err := dshGatewayPaths()
	if err != nil {
		return
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := process.Kill(); err != nil {
		log.Printf("AI APP dsh: killing web process pid=%d failed: %v", pid, err)
	}
	_ = process.Release()
}

func killDshListeners() {
	if runtime.GOOS == "windows" {
		return
	}
	addr := net.JoinHostPort(dshWebHost, strconv.Itoa(dshWebPort))
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return
	}
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return
	}
	output, err := exec.Command(lsof, "-tiTCP:"+port, "-sTCP:LISTEN").Output()
	if err != nil {
		return
	}
	for _, rawPID := range strings.Fields(string(output)) {
		pid, err := strconv.Atoi(strings.TrimSpace(rawPID))
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := process.Kill(); err != nil {
			log.Printf("AI APP dsh: killing listener pid=%d failed: %v", pid, err)
		}
		_ = process.Release()
	}
}

func removeDshPID() {
	_, pidPath, err := dshGatewayPaths()
	if err == nil {
		_ = os.Remove(pidPath)
	}
}
