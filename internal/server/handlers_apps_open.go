package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const (
	openClawWebProfile      = "csghub-lite"
	openClawProviderID      = "opencsg"
	openClawProviderAPI     = "openai-completions"
	openClawOpenTimeout     = 2 * time.Minute
	openClawGatewayWait     = 2 * time.Minute
	openClawGatewayLogName  = "openclaw-gateway.log"
	openClawGatewayPIDName  = "openclaw-gateway.pid"
	openClawDashboardPrefix = "Dashboard URL:"
	openClawContextWindow   = 16000
	openClawMaxTokens       = 4096
	openClawDefaultRegistry = "https://registry.npmmirror.com"
)

func (s *Server) openAIAppURL(ctx context.Context, appID, modelID, modelSource, workDir, publicBaseURL string) (string, error) {
	info, err := s.appManager.Get(ctx, appID)
	if err != nil {
		return "", err
	}
	if info.Disabled || !info.Supported {
		return "", fmt.Errorf("%s is currently disabled in AI Apps", appID)
	}
	if !info.Installed {
		return "", fmt.Errorf("%s is not installed yet", appID)
	}

	switch appID {
	case "openclaw":
		url, err := s.openClawChatURL(ctx, modelID, modelSource)
		if err != nil {
			return "", err
		}
		return rewriteLoopbackURLHost(url, publicBaseURL), nil
	case "csgclaw":
		url, err := s.openCSGClawURL(ctx, modelID, modelSource)
		if err != nil {
			return "", err
		}
		return rewriteLoopbackURLHost(url, publicBaseURL), nil
	case "claude-code", "open-code", "codex", "pi", "antigravity":
		return s.openAIAppShellURL(ctx, appID, modelID, modelSource, workDir, publicBaseURL)
	default:
		return "", fmt.Errorf("%s does not provide a direct chat entry yet", appID)
	}
}

func (s *Server) openClawChatURL(ctx context.Context, modelID, modelSource string) (string, error) {
	s.openclawMu.Lock()
	defer s.openclawMu.Unlock()

	binary, err := resolveAIAppLaunchBinary([]string{"openclaw"})
	if err != nil {
		return "", fmt.Errorf("OpenClaw is installed, but its launch command was not found on PATH")
	}

	log.Printf("AI APP openclaw: opening chat requested_model=%q", modelID)
	s.refreshOpenClawModelCatalog(ctx)

	if err := s.ensureOpenClawProfile(ctx, binary, modelID, modelSource); err != nil {
		return "", err
	}
	if strings.TrimSpace(modelID) != "" {
		s.savePreferredAIAppModel("openclaw", modelID)
	}

	url, err := openClawDashboardURL(ctx, binary)
	if err != nil {
		return "", err
	}
	if dashboardReachable(url) {
		log.Printf("AI APP openclaw: reusing reachable gateway url=%s", redactURLToken(url))
	} else {
		cmd, err := s.startOpenClawGateway(binary)
		if err != nil {
			return "", err
		}
		if err := waitForDashboard(url, openClawGatewayWait); err != nil {
			stopStartedOpenClawGateway(cmd)
			return "", err
		}
		releaseStartedOpenClawGateway(cmd)
	}
	log.Printf("AI APP openclaw: gateway ready url=%s", redactURLToken(url))
	return openClawDirectChatURL(url, "main")
}

func (s *Server) refreshOpenClawModelCatalog(ctx context.Context) {
	if s.cloud == nil || !s.hasCloudCredential() {
		return
	}

	if _, err := s.refreshCloudChatModels(ctx); err != nil {
		log.Printf("refreshing cloud models before opening OpenClaw failed: %v", err)
	}
}

func (s *Server) ensureOpenClawProfile(ctx context.Context, binary, requestedModelID, requestedSource string) error {
	modelID, modelIDs, err := s.resolveAIAppLaunchModels(ctx, requestedModelID, requestedSource)
	if err != nil {
		return err
	}
	serverURL := s.localBaseURL()

	ok, err := openClawProfileMatches(serverURL, modelID)
	if err != nil || !ok {
		log.Printf("AI APP openclaw: configuring profile model=%q base_url=%s", modelID, openClawProviderBaseURL(serverURL))
		configureCtx, cancel := context.WithTimeout(ctx, openClawOpenTimeout)
		defer cancel()

		args := []string{
			"--profile", openClawWebProfile,
			"onboard",
			"--non-interactive",
			"--auth-choice", "custom-api-key",
			"--custom-provider-id", openClawProviderID,
			"--custom-compatibility", "openai",
			"--custom-base-url", openClawProviderBaseURL(serverURL),
			"--custom-model-id", modelID,
			"--custom-api-key", openClawProviderAPIKey(s.cfg.Token),
			"--accept-risk",
			"--skip-channels",
			"--skip-search",
			"--skip-ui",
			"--skip-skills",
			"--skip-daemon",
			"--skip-health",
		}
		cmd := exec.CommandContext(configureCtx, binary, args...)
		cmd.Env = envWithOverrides(map[string]string{
			"NPM_CONFIG_REGISTRY": openClawNPMRegistry(),
		})
		output, err := cmd.CombinedOutput()
		if configureCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("configuring OpenClaw timed out after %s", openClawOpenTimeout)
		}
		if err != nil {
			msg := strings.TrimSpace(string(output))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("configuring OpenClaw: %s", msg)
		}
		log.Printf("AI APP openclaw: profile configured")
	}

	models := buildOpenClawProfileModels(modelIDs, nil)
	if availableModels, err := s.listAvailableModels(ctx); err == nil {
		models = buildOpenClawProfileModels(modelIDs, availableModels)
	}
	if err := syncOpenClawProfile(serverURL, openClawProviderAPIKey(s.cfg.Token), modelID, models); err != nil {
		return fmt.Errorf("syncing OpenClaw profile models: %w", err)
	}
	log.Printf("AI APP openclaw: model catalog synced models=%d selected=%q", len(models), modelID)
	return nil
}

func openClawNPMRegistry() string {
	if registry := strings.TrimSpace(os.Getenv("NPM_CONFIG_REGISTRY")); registry != "" {
		return registry
	}
	return openClawDefaultRegistry
}

func scoreOpenClawModel(m *model.LocalModel) int64 {
	name := strings.ToLower(m.FullName())
	score := m.Size / 1_000_000
	if strings.Contains(name, "coder") {
		score += 10_000_000
	}
	if strings.Contains(name, "code") {
		score += 5_000_000
	}
	if strings.Contains(name, "gpt-oss") {
		score += 6_000_000
	}
	if strings.Contains(name, "qwen") {
		score += 2_000_000
	}
	return score
}

func openClawProviderBaseURL(serverURL string) string {
	return strings.TrimRight(serverURL, "/") + "/v1"
}

func (s *Server) localBaseURL() string {
	addr := strings.TrimSpace(s.cfg.ListenAddr)
	if addr == "" {
		return "http://127.0.0.1" + config.DefaultListenAddr
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "http://127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	if strings.HasPrefix(addr, "localhost:") || strings.HasPrefix(addr, "127.0.0.1:") {
		return "http://" + addr
	}
	if strings.Contains(addr, "://") {
		return addr
	}
	return "http://" + addr
}

func openClawDashboardURL(ctx context.Context, binary string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, "--profile", openClawWebProfile, "dashboard", "--no-open")
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("fetching OpenClaw dashboard URL: %s", msg)
	}
	url, err := extractDashboardURL(output)
	if err != nil {
		return "", err
	}
	return openClawURLWithGatewayToken(url)
}

func extractDashboardURL(output []byte) (string, error) {
	for _, rawLine := range bytes.Split(output, []byte{'\n'}) {
		line := strings.TrimSpace(string(rawLine))
		if !strings.HasPrefix(line, openClawDashboardPrefix) {
			continue
		}
		url := strings.TrimSpace(strings.TrimPrefix(line, openClawDashboardPrefix))
		if url == "" {
			continue
		}
		if _, err := neturl.Parse(url); err == nil {
			return url, nil
		}
	}
	return "", fmt.Errorf("OpenClaw did not return a usable dashboard URL")
}

func openClawDirectChatURL(rawURL, session string) (string, error) {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing OpenClaw dashboard URL: %w", err)
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	parsed.Path = basePath + "/chat"
	query := parsed.Query()
	query.Set("session", session)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func rewriteLoopbackURLHost(rawURL, publicBaseURL string) string {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if !isLoopbackHost(parsed.Hostname()) {
		return rawURL
	}
	publicParsed, err := neturl.Parse(strings.TrimSpace(publicBaseURL))
	if err != nil || publicParsed.Hostname() == "" || isLoopbackHost(publicParsed.Hostname()) {
		return rawURL
	}
	host := publicParsed.Hostname()
	if port := parsed.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	parsed.Host = host
	return parsed.String()
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func openClawURLWithGatewayToken(rawURL string) (string, error) {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing OpenClaw dashboard URL: %w", err)
	}
	if fragmentHasToken(parsed.Fragment) {
		return parsed.String(), nil
	}

	token, err := openClawGatewayToken()
	if err != nil {
		log.Printf("AI APP openclaw: reading gateway token failed: %v", err)
		return parsed.String(), nil
	}
	if token == "" {
		return parsed.String(), nil
	}

	if parsed.Fragment == "" {
		parsed.Fragment = "token=" + neturl.QueryEscape(token)
		return parsed.String(), nil
	}
	parsed.Fragment += "&token=" + neturl.QueryEscape(token)
	return parsed.String(), nil
}

func fragmentHasToken(fragment string) bool {
	values, err := neturl.ParseQuery(fragment)
	return err == nil && strings.TrimSpace(values.Get("token")) != ""
}

func openClawGatewayToken() (string, error) {
	path, err := openClawProfileConfigPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var cfg struct {
		Gateway struct {
			Auth struct {
				Mode  string `json:"mode"`
				Token string `json:"token"`
			} `json:"auth"`
		} `json:"gateway"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	if cfg.Gateway.Auth.Mode != "token" {
		return "", nil
	}
	return strings.TrimSpace(cfg.Gateway.Auth.Token), nil
}

func redactURLToken(rawURL string) string {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	redacted := false
	query := parsed.Query()
	if query.Get("token") != "" {
		query.Set("token", "redacted")
		parsed.RawQuery = query.Encode()
		redacted = true
	}
	if fragmentHasToken(parsed.Fragment) {
		values, err := neturl.ParseQuery(parsed.Fragment)
		if err == nil {
			values.Set("token", "redacted")
			parsed.Fragment = values.Encode()
			redacted = true
		}
	}
	if !redacted {
		return rawURL
	}
	return parsed.String()
}

func dashboardReachable(rawURL string) bool {
	hostPort, err := dashboardHostPort(rawURL)
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("tcp", hostPort, 750*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForDashboard(rawURL string, timeout time.Duration) error {
	hostPort, err := dashboardHostPort(rawURL)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", hostPort, 750*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("OpenClaw gateway did not become ready in time")
}

func dashboardHostPort(rawURL string) (string, error) {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing OpenClaw dashboard URL: %w", err)
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" || port == "" {
		return "", fmt.Errorf("OpenClaw dashboard URL is missing a host or port")
	}
	return net.JoinHostPort(host, port), nil
}

func (s *Server) startOpenClawGateway(binary string) (*exec.Cmd, error) {
	logPath, pidPath, err := openClawGatewayPaths()
	if err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening OpenClaw gateway log: %w", err)
	}

	cmd := exec.Command(binary, "--profile", openClawWebProfile, "gateway", "run", "--force")
	cmd.Env = envWithOverrides(map[string]string{
		"NPM_CONFIG_REGISTRY":      openClawNPMRegistry(),
		"OPENCLAW_DISABLE_BONJOUR": "1",
	})
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("starting OpenClaw gateway: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		log.Printf("AI APP openclaw: writing gateway pid failed: %v", err)
	}
	log.Printf("AI APP openclaw: gateway process started pid=%d log=%s", cmd.Process.Pid, logPath)
	_ = logFile.Close()
	return cmd, nil
}

func openClawGatewayRunning(ctx context.Context, binary string) bool {
	url, err := openClawDashboardURL(ctx, binary)
	if err != nil {
		return false
	}
	return dashboardReachable(url)
}

func stopOpenClawGateway(ctx context.Context, binary string) error {
	url, _ := openClawDashboardURL(ctx, binary)
	if !dashboardReachableOrDefault(url, openClawDefaultGatewayAddr()) {
		removeOpenClawGatewayPID()
		return nil
	}

	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(stopCtx, binary, "--profile", openClawWebProfile, "gateway", "stop").CombinedOutput()
	if stopCtx.Err() == context.DeadlineExceeded {
		log.Printf("AI APP openclaw: gateway stop command timed out")
	} else if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		log.Printf("AI APP openclaw: gateway stop command failed: %s", msg)
	}

	if waitForDashboardStopOrDefault(url, 3*time.Second) == nil {
		removeOpenClawGatewayPID()
		return nil
	}
	killOpenClawGatewayPID()
	if waitForDashboardStopOrDefault(url, 5*time.Second) == nil {
		removeOpenClawGatewayPID()
		return nil
	}
	killOpenClawGatewayListeners(url)
	if waitForDashboardStopOrDefault(url, 5*time.Second) == nil {
		removeOpenClawGatewayPID()
		return nil
	}
	if runtime.GOOS != "windows" {
		_ = exec.Command("pkill", "-f", "openclaw .*gateway run").Run()
	}
	if err := waitForDashboardStopOrDefault(url, 5*time.Second); err != nil {
		return fmt.Errorf("OpenClaw gateway did not stop in time")
	}
	removeOpenClawGatewayPID()
	return nil
}

func openClawGatewayPaths() (logPath, pidPath string, err error) {
	appHome, err := config.AppHome()
	if err != nil {
		return "", "", err
	}
	logDir := filepath.Join(appHome, "apps", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", "", fmt.Errorf("creating OpenClaw gateway log dir: %w", err)
	}
	return filepath.Join(logDir, openClawGatewayLogName), filepath.Join(logDir, openClawGatewayPIDName), nil
}

func openClawDefaultGatewayAddr() string {
	return "127.0.0.1:18789"
}

func dashboardReachableOrDefault(rawURL, defaultHostPort string) bool {
	hostPort, err := dashboardHostPort(rawURL)
	if err != nil {
		hostPort = defaultHostPort
	}
	conn, err := net.DialTimeout("tcp", hostPort, 750*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForDashboardStopOrDefault(rawURL string, timeout time.Duration) error {
	hostPort, err := dashboardHostPort(rawURL)
	if err != nil {
		hostPort = openClawDefaultGatewayAddr()
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", hostPort, 750*time.Millisecond)
		if err != nil {
			return nil
		}
		_ = conn.Close()
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("OpenClaw gateway did not stop in time")
}

func killOpenClawGatewayPID() {
	_, pidPath, err := openClawGatewayPaths()
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
		log.Printf("AI APP openclaw: killing gateway process pid=%d failed: %v", pid, err)
	}
	_ = process.Release()
}

func killOpenClawGatewayListeners(rawURL string) {
	if runtime.GOOS == "windows" {
		return
	}
	hostPort, err := dashboardHostPort(rawURL)
	if err != nil {
		hostPort = openClawDefaultGatewayAddr()
	}
	_, port, err := net.SplitHostPort(hostPort)
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
			log.Printf("AI APP openclaw: killing gateway listener pid=%d failed: %v", pid, err)
		}
		_ = process.Release()
	}
}

func removeOpenClawGatewayPID() {
	_, pidPath, err := openClawGatewayPaths()
	if err == nil {
		_ = os.Remove(pidPath)
	}
}

func releaseStartedOpenClawGateway(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
		return
	}
	_ = cmd.Process.Release()
}

func stopStartedOpenClawGateway(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func openClawProfileMatches(serverURL, modelID string) (bool, error) {
	path, err := openClawProfileConfigPath()
	if err != nil {
		return false, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	var cfg struct {
		Models struct {
			Providers map[string]struct {
				BaseURL string `json:"baseUrl"`
			} `json:"providers"`
		} `json:"models"`
		Agents struct {
			Defaults struct {
				Model struct {
					Primary string `json:"primary"`
				} `json:"model"`
			} `json:"defaults"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, err
	}

	provider, ok := cfg.Models.Providers[openClawProviderID]
	if !ok {
		return false, nil
	}
	wantModel := openClawProviderID + "/" + modelID
	return strings.TrimRight(provider.BaseURL, "/") == strings.TrimRight(openClawProviderBaseURL(serverURL), "/") &&
		cfg.Agents.Defaults.Model.Primary == wantModel, nil
}

func openClawProfileConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := ".openclaw-" + openClawWebProfile
	if openClawWebProfile == "" {
		base = ".openclaw"
	}
	return filepath.Join(home, base, "openclaw.json"), nil
}

func openClawAgentModelsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := ".openclaw-" + openClawWebProfile
	if openClawWebProfile == "" {
		base = ".openclaw"
	}
	return filepath.Join(home, base, "agents", "main", "agent", "models.json"), nil
}

func buildOpenClawProfileModels(modelIDs []string, available []api.ModelInfo) []api.ModelInfo {
	byID := make(map[string]api.ModelInfo, len(available))
	for _, item := range available {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			continue
		}
		byID[modelID] = item
	}

	models := make([]api.ModelInfo, 0, len(modelIDs))
	seen := make(map[string]struct{}, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		if item, ok := byID[modelID]; ok {
			models = append(models, item)
			continue
		}
		models = append(models, api.ModelInfo{
			Name:        modelID,
			Model:       modelID,
			DisplayName: modelID,
		})
	}
	return models
}

func syncOpenClawProfile(serverURL, apiKey, selectedModelID string, models []api.ModelInfo) error {
	provider := openClawProviderConfig(serverURL, apiKey, models)
	primaryModel := openClawProviderID + "/" + strings.TrimSpace(selectedModelID)
	agentModels := openClawAgentModelEntries(models)

	profilePath, err := openClawProfileConfigPath()
	if err != nil {
		return err
	}
	if err := syncOpenClawJSONFile(profilePath, func(doc map[string]interface{}) {
		modelsSection := ensureOpenClawObject(doc, "models")
		if strings.TrimSpace(fmt.Sprint(modelsSection["mode"])) == "" {
			modelsSection["mode"] = "merge"
		}
		modelsSection["providers"] = map[string]interface{}{
			openClawProviderID: provider,
		}

		agentsSection := ensureOpenClawObject(doc, "agents")
		defaultsSection := ensureOpenClawObject(agentsSection, "defaults")
		modelSection := ensureOpenClawObject(defaultsSection, "model")
		modelSection["primary"] = primaryModel
		defaultsSection["models"] = agentModels
	}); err != nil {
		return err
	}

	modelsPath, err := openClawAgentModelsPath()
	if err != nil {
		return err
	}
	return syncOpenClawJSONFile(modelsPath, func(doc map[string]interface{}) {
		doc["providers"] = map[string]interface{}{
			openClawProviderID: provider,
		}
	})
}

func syncOpenClawJSONFile(path string, mutate func(map[string]interface{})) error {
	doc := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &doc); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	mutate(doc)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func ensureOpenClawObject(parent map[string]interface{}, key string) map[string]interface{} {
	if existing, ok := parent[key].(map[string]interface{}); ok {
		return existing
	}
	child := map[string]interface{}{}
	parent[key] = child
	return child
}

func openClawProviderConfig(serverURL, apiKey string, models []api.ModelInfo) map[string]interface{} {
	return map[string]interface{}{
		"baseUrl": openClawProviderBaseURL(serverURL),
		"apiKey":  openClawProviderAPIKey(apiKey),
		"api":     openClawProviderAPI,
		"models":  openClawProviderModels(models),
	}
}

func openClawProviderAPIKey(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "csghub-lite"
	}
	return token
}

func openClawProviderModels(models []api.ModelInfo) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(models))
	for _, item := range models {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			continue
		}

		displayName := strings.TrimSpace(item.DisplayName)
		if displayName == "" {
			displayName = modelID
		}
		source := strings.TrimSpace(item.Source)
		if source == "cloud" {
			displayName += " (OpenCSG)"
		} else if source == "local" {
			displayName += " (Local)"
		}

		items = append(items, map[string]interface{}{
			"id":            modelID,
			"name":          displayName,
			"api":           openClawProviderAPI,
			"reasoning":     false,
			"input":         []string{"text"},
			"cost":          openClawZeroCost(),
			"contextWindow": openClawContextWindow,
			"maxTokens":     openClawMaxTokens,
		})
	}
	return items
}

func openClawAgentModelEntries(models []api.ModelInfo) map[string]interface{} {
	entries := make(map[string]interface{}, len(models))
	for _, item := range models {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			continue
		}
		entries[openClawProviderID+"/"+modelID] = map[string]interface{}{}
	}
	return entries
}

func openClawZeroCost() map[string]float64 {
	return map[string]float64{
		"input":      0,
		"output":     0,
		"cacheRead":  0,
		"cacheWrite": 0,
	}
}

func envWithOverrides(overrides map[string]string) []string {
	return envWithOverridesAndUnset(overrides)
}

func envWithOverridesAndUnset(overrides map[string]string, unsetKeys ...string) []string {
	skip := make(map[string]struct{}, len(unsetKeys))
	for _, key := range unsetKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		skip[key] = struct{}{}
	}

	base := os.Environ()
	env := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		name := item
		if idx := strings.IndexByte(item, '='); idx >= 0 {
			name = item[:idx]
		}
		if _, ok := skip[name]; ok {
			continue
		}
		env = append(env, item)
	}

	for key, value := range overrides {
		prefix := key + "="
		replaced := false
		for i, item := range env {
			if strings.HasPrefix(item, prefix) {
				env[i] = prefix + value
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, prefix+value)
		}
	}
	return env
}
