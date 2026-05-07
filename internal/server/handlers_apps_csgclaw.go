package server

import (
	"bufio"
	"context"
	"encoding/json"
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

	"github.com/opencsgs/csghub-lite/internal/config"
)

const (
	csgclawDefaultAddr    = "127.0.0.1:18080"
	csgclawOnboardTimeout = 2 * time.Minute
	csgclawServeWait      = 12 * time.Second
	csgclawLogName        = "csgclaw.log"
	csgclawPIDName        = "csgclaw.pid"
	csgclawProviderName   = "csghub-lite"
	csgclawManagerAgentID = "u-manager"
	csgclawManagerImage   = "opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/picoclaw:2026.4.26"
)

func (s *Server) openCSGClawURL(ctx context.Context, modelID, modelSource string) (string, error) {
	s.csgclawMu.Lock()
	defer s.csgclawMu.Unlock()

	binary, err := resolveAIAppLaunchBinary([]string{"csgclaw"})
	if err != nil {
		return "", fmt.Errorf("CSGClaw is installed, but its launch command was not found on PATH")
	}

	requestedModel := strings.TrimSpace(modelID)
	resolvedModel, modelIDs, err := s.resolveCSGClawLaunchModels(ctx, requestedModel, modelSource)
	if err != nil {
		return "", err
	}
	if requestedModel != "" {
		s.savePreferredAIAppModel("csgclaw", resolvedModel)
	}

	log.Printf("AI APP csgclaw: onboarding model=%q models=%d manager_image=%s", resolvedModel, len(modelIDs), csgclawManagerImage)
	if err := s.onboardCSGClaw(ctx, binary, resolvedModel, modelIDs, false); err != nil {
		return "", err
	}

	// Always restart to pick up model/config changes (like openclaw --force).
	stopCSGClaw(binary)
	log.Printf("AI APP csgclaw: starting serve daemon")
	if err := s.startCSGClawServe(binary); err != nil {
		return "", err
	}
	if err := waitForCSGClaw(csgclawServeWait); err != nil {
		return "", err
	}

	return "http://" + csgclawDefaultAddr + "/", nil
}

func (s *Server) saveCSGClawModel(ctx context.Context, modelID, modelSource string) error {
	s.csgclawMu.Lock()
	defer s.csgclawMu.Unlock()

	binary, err := resolveAIAppLaunchBinary([]string{"csgclaw"})
	if err != nil {
		return fmt.Errorf("CSGClaw is installed, but its launch command was not found on PATH")
	}

	resolvedModel, modelIDs, err := s.resolveCSGClawLaunchModels(ctx, modelID, modelSource)
	if err != nil {
		return err
	}
	s.savePreferredAIAppModel("csgclaw", resolvedModel)

	log.Printf("AI APP csgclaw: model switch requested model=%q resolved=%q", modelID, resolvedModel)
	stopCSGClawManager(ctx, binary)
	if err := s.onboardCSGClaw(ctx, binary, resolvedModel, modelIDs, true); err != nil {
		return err
	}

	stopCSGClaw(binary)
	log.Printf("AI APP csgclaw: restarting serve daemon after model switch")
	if err := s.startCSGClawServe(binary); err != nil {
		return err
	}
	if err := waitForCSGClaw(csgclawServeWait); err != nil {
		return err
	}
	return nil
}

func (s *Server) resolveCSGClawLaunchModels(ctx context.Context, requestedModel, requestedSource string) (string, []string, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel != "" {
		return s.resolveAIAppLaunchModels(ctx, requestedModel, requestedSource)
	}

	preferredModel := s.preferredAIAppModel("csgclaw")
	if preferredModel != "" {
		modelID, modelIDs, err := s.resolveAIAppLaunchModels(ctx, preferredModel, "")
		if err == nil {
			return modelID, modelIDs, nil
		}
		if strings.Contains(err.Error(), "is not available for AI Apps") {
			s.clearPreferredAIAppModel("csgclaw")
		} else {
			return "", nil, err
		}
	}

	return s.resolveAIAppLaunchModels(ctx, "", "")
}

func (s *Server) onboardCSGClaw(ctx context.Context, binary, modelID string, modelIDs []string, forceRecreateManager bool) error {
	listenAddr := ""
	if s != nil && s.cfg != nil {
		listenAddr = s.cfg.ListenAddr
	}
	serverURL := csgclawReachableBaseURL(listenAddr, csgclawInterfaceAddrs())
	token := ""
	if s != nil && s.cfg != nil {
		token = strings.TrimSpace(s.cfg.Token)
	}
	apiKey := token
	if apiKey == "" {
		apiKey = "csghub-lite"
	}
	modelBaseURL := strings.TrimRight(serverURL, "/") + "/v1"
	models := csgclawOrderedModels(modelID, modelIDs)
	if forceRecreateManager || csgclawConfigNeedsManagerRecreate(modelBaseURL, apiKey, modelID, csgclawManagerImage) {
		log.Printf("AI APP csgclaw: manager/config drift detected; recreate manager with synced config")
		stopCSGClawManager(ctx, binary)
	}
	if err := ensureCSGClawManagedConfig(modelBaseURL, apiKey, modelID, models, csgclawManagerImage); err != nil {
		return fmt.Errorf("writing CSGClaw config: %w", err)
	}

	onboardCtx, cancel := context.WithTimeout(ctx, csgclawOnboardTimeout)
	defer cancel()

	args := []string{"onboard"}

	cmd := exec.CommandContext(onboardCtx, binary, args...)
	output, err := cmd.CombinedOutput()
	if onboardCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("configuring CSGClaw timed out after %s", csgclawOnboardTimeout)
	}
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("configuring CSGClaw: %s", msg)
	}

	log.Printf("AI APP csgclaw: onboard complete base_url=%s models=%s", modelBaseURL, strings.Join(models, ","))
	return nil
}

func appendForceRecreateManagerArg(args []string) []string {
	for _, arg := range args {
		if arg == "--force-recreate-manager" || arg == "-force-recreate-manager" {
			return args
		}
	}
	return append(args, "--force-recreate-manager")
}

func ensureCSGClawManagedConfig(baseURL, apiKey, modelID string, models []string, managerImage string) error {
	path, err := csgclawConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	input := string(data)
	if strings.TrimSpace(input) == "" {
		input = defaultCSGClawConfig(managerImage)
	}

	updated := setCSGClawManagedModelConfig(input, baseURL, apiKey, modelID, models, managerImage)
	return os.WriteFile(path, []byte(updated), 0o600)
}

func defaultCSGClawConfig(managerImage string) string {
	return `# Generated by csgclaw onboard.

[server]
listen_addr = "0.0.0.0:18080"
advertise_base_url = ""
access_token = "your_access_token"
no_auth = false

[bootstrap]
manager_image = ` + strconv.Quote(managerImage) + `

[sandbox]
debian_registries = ["harbor.opencsg.com", "docker.io"]
provider = "boxlite-cli"
home_dir_name = "boxlite"
`
}

func setCSGClawManagedModelConfig(input, baseURL, apiKey, modelID string, models []string, managerImage string) string {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines)+12)
	inModelsSection := false
	inBootstrap := false
	bootstrapFound := false
	managerImageSet := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inBootstrap && !managerImageSet {
				out = append(out, "manager_image = "+strconv.Quote(managerImage))
				managerImageSet = true
			}
			section := strings.Trim(trimmed, "[]")
			inBootstrap = section == "bootstrap"
			if inBootstrap {
				bootstrapFound = true
			}
			inModelsSection = section == "models" || strings.HasPrefix(section, "models.providers.")
		}
		if inModelsSection {
			continue
		}
		if inBootstrap {
			key, _, ok := parseCSGClawConfigKV(trimmed)
			if ok && key == "manager_image" {
				if !managerImageSet {
					out = append(out, "manager_image = "+strconv.Quote(managerImage))
					managerImageSet = true
				}
				continue
			}
		}
		out = append(out, line)
	}
	if inBootstrap && !managerImageSet {
		out = append(out, "manager_image = "+strconv.Quote(managerImage))
	}
	if !bootstrapFound {
		out = append(out, "", "[bootstrap]", "manager_image = "+strconv.Quote(managerImage))
	}

	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	out = append(out, "", csgclawManagedModelConfigBlock(baseURL, apiKey, modelID, models))
	return strings.Join(out, "\n") + "\n"
}

func csgclawManagedModelConfigBlock(baseURL, apiKey, modelID string, models []string) string {
	quotedModels := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		quotedModels = append(quotedModels, strconv.Quote(model))
	}
	return strings.Join([]string{
		"[models]",
		"default = " + strconv.Quote(csgclawModelSelector(csgclawProviderName, modelID)),
		"",
		"[models.providers." + csgclawProviderName + "]",
		"base_url = " + strconv.Quote(strings.TrimRight(baseURL, "/")),
		"api_key = " + strconv.Quote(apiKey),
		"models = [" + strings.Join(quotedModels, ", ") + "]",
	}, "\n")
}

func (s *Server) startCSGClawServe(binary string) error {
	logPath, pidPath, err := csgclawServePaths()
	if err != nil {
		return err
	}
	cmd := exec.Command(binary, "serve", "--daemon", "--log", logPath, "--pid", pidPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("starting CSGClaw serve: %s", msg)
	}
	log.Printf("started csgclaw serve daemon (log %s, pid %s)", logPath, pidPath)
	return nil
}

// stopCSGClaw terminates any running csgclaw serve process so a fresh
// instance can be started with updated configuration.
func stopCSGClaw(binary string) {
	if !csgclawReachable() {
		return
	}
	if _, pidPath, err := csgclawServePaths(); err == nil {
		_ = exec.Command(binary, "stop", "--pid", pidPath).Run()
	}
	if waitForCSGClawStop(3*time.Second) == nil {
		return
	}
	if runtime.GOOS != "windows" {
		_ = exec.Command("pkill", "-f", "csgclaw (serve|_serve)").Run()
	}
	_ = waitForCSGClawStop(3 * time.Second)
}

func stopCSGClawManager(ctx context.Context, binary string) {
	boxIDs := csgclawManagerBoxIDs(ctx, binary)

	stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(stopCtx, binary, "agent", "stop", csgclawManagerAgentID)
	output, err := cmd.CombinedOutput()
	if stopCtx.Err() == context.DeadlineExceeded {
		log.Printf("AI APP csgclaw: manager stop timed out")
	} else if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		log.Printf("AI APP csgclaw: manager stop failed: %s", msg)
	} else {
		log.Printf("AI APP csgclaw: manager stop requested")
	}

	// The agent API can mark the manager stopped while an old boxlite-shim keeps
	// running with stale model environment variables. Remove that stale process
	// before force-recreating the manager.
	killCSGClawManagerShims(boxIDs)
}

func csgclawManagerBoxIDs(ctx context.Context, binary string) []string {
	statusCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(statusCtx, binary, "agent", "status").CombinedOutput()
	if err != nil {
		log.Printf("AI APP csgclaw: manager status failed before recreate: %v", err)
		return nil
	}

	agents, err := parseCSGClawAgentStatus(output)
	if err != nil {
		log.Printf("AI APP csgclaw: parsing manager status failed before recreate: %v", err)
		return nil
	}
	boxIDs := make([]string, 0, len(agents))
	seen := map[string]struct{}{}
	for _, agent := range agents {
		if !agent.IsManager() {
			continue
		}
		boxID := strings.TrimSpace(agent.BoxID)
		if boxID == "" {
			continue
		}
		if _, ok := seen[boxID]; ok {
			continue
		}
		seen[boxID] = struct{}{}
		boxIDs = append(boxIDs, boxID)
	}
	return boxIDs
}

type csgclawAgentStatus struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	BoxID string `json:"box_id"`
}

func (a csgclawAgentStatus) IsManager() bool {
	return strings.TrimSpace(a.ID) == csgclawManagerAgentID ||
		strings.TrimSpace(a.Role) == "manager" ||
		strings.TrimSpace(a.Name) == "manager"
}

func parseCSGClawAgentStatus(output []byte) ([]csgclawAgentStatus, error) {
	var agents []csgclawAgentStatus
	if err := json.Unmarshal(output, &agents); err == nil {
		return agents, nil
	}
	var agent csgclawAgentStatus
	if err := json.Unmarshal(output, &agent); err != nil {
		return nil, err
	}
	return []csgclawAgentStatus{agent}, nil
}

func killCSGClawManagerShims(boxIDs []string) {
	if len(boxIDs) == 0 || runtime.GOOS == "windows" {
		return
	}
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		log.Printf("AI APP csgclaw: listing manager processes failed: %v", err)
		return
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "boxlite-shim") {
			continue
		}
		for _, boxID := range boxIDs {
			if !strings.Contains(line, boxID) {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil || pid <= 0 || pid == os.Getpid() {
				continue
			}
			process, err := os.FindProcess(pid)
			if err != nil {
				continue
			}
			if err := process.Kill(); err != nil {
				log.Printf("AI APP csgclaw: killing stale manager process pid=%d box_id=%s failed: %v", pid, boxID, err)
				continue
			}
			log.Printf("AI APP csgclaw: killed stale manager process pid=%d box_id=%s", pid, boxID)
			break
		}
	}
}

func csgclawServePaths() (logPath, pidPath string, err error) {
	appHome, err := config.AppHome()
	if err != nil {
		return "", "", err
	}
	logDir := filepath.Join(appHome, "apps", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", "", fmt.Errorf("creating CSGClaw log dir: %w", err)
	}
	return filepath.Join(logDir, csgclawLogName), filepath.Join(logDir, csgclawPIDName), nil
}

func waitForCSGClawStop(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !csgclawReachable() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("CSGClaw server did not stop in time")
}

func csgclawReachable() bool {
	conn, err := net.DialTimeout("tcp", csgclawDefaultAddr, 750*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForCSGClaw(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", csgclawDefaultAddr, 750*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("CSGClaw server did not become ready in time")
}

func csgclawReachableBaseURL(listenAddr string, addrs []net.Addr) string {
	host, port := csgclawListenHostPort(listenAddr)
	if csgclawNeedsReachableHost(host) {
		if reachableHost := csgclawReachableHost(addrs); reachableHost != "" {
			host = reachableHost
		} else {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, port)
}

func csgclawListenHostPort(listenAddr string) (host, port string) {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		addr = config.DefaultListenAddr
	}
	if strings.HasPrefix(addr, ":") {
		return "", strings.TrimPrefix(addr, ":")
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		return strings.Trim(host, "[]"), port
	}
	if strings.Count(addr, ":") == 1 {
		parts := strings.SplitN(addr, ":", 2)
		return parts[0], parts[1]
	}
	return "127.0.0.1", strings.TrimPrefix(config.DefaultListenAddr, ":")
}

func csgclawNeedsReachableHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsUnspecified()
}

func csgclawReachableHost(addrs []net.Addr) string {
	privateHosts := make([]string, 0, len(addrs))
	publicHosts := make([]string, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() {
			continue
		}
		ip = ip.To4()
		if ip == nil {
			continue
		}
		host := ip.String()
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		if ip.IsPrivate() {
			privateHosts = append(privateHosts, host)
			continue
		}
		if ip.IsGlobalUnicast() {
			publicHosts = append(publicHosts, host)
		}
	}
	if len(privateHosts) > 0 {
		return privateHosts[0]
	}
	if len(publicHosts) > 0 {
		return publicHosts[0]
	}
	return ""
}

func csgclawInterfaceAddrs() []net.Addr {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	return addrs
}

func csgclawConfigNeedsManagerRecreate(baseURL, apiKey, modelID, managerImage string) bool {
	cfg, err := readCSGClawModelConfig()
	if err != nil {
		return true
	}
	providerName := csgclawProviderName
	provider, ok := cfg.Providers[providerName]
	if !ok {
		return true
	}
	wantSelector := csgclawModelSelector(providerName, modelID)
	return strings.TrimSpace(cfg.DefaultSelector) != wantSelector ||
		strings.TrimSpace(cfg.ManagerImage) != strings.TrimSpace(managerImage) ||
		strings.TrimRight(provider.BaseURL, "/") != strings.TrimRight(baseURL, "/") ||
		strings.TrimSpace(provider.APIKey) != strings.TrimSpace(apiKey) ||
		!csgclawContainsModel(provider.Models, modelID)
}

type csgclawModelConfig struct {
	DefaultSelector string
	ManagerImage    string
	Providers       map[string]csgclawModelProviderConfig
}

type csgclawModelProviderConfig struct {
	BaseURL string
	APIKey  string
	Models  []string
}

func (c csgclawModelConfig) EffectiveProviderName() string {
	selector := strings.TrimSpace(c.DefaultSelector)
	if providerName, _, ok := strings.Cut(selector, "."); ok {
		providerName = strings.TrimSpace(providerName)
		if providerName != "" {
			return providerName
		}
	}
	if len(c.Providers) == 1 {
		for name := range c.Providers {
			name = strings.TrimSpace(name)
			if name != "" {
				return name
			}
		}
	}
	if _, ok := c.Providers[csgclawProviderName]; ok {
		return csgclawProviderName
	}
	return ""
}

func readCSGClawModelConfig() (csgclawModelConfig, error) {
	path, err := csgclawConfigPath()
	if err != nil {
		return csgclawModelConfig{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return csgclawModelConfig{}, err
	}
	defer file.Close()

	cfg := csgclawModelConfig{
		Providers: make(map[string]csgclawModelProviderConfig),
	}
	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		key, value, ok := parseCSGClawConfigKV(line)
		if !ok {
			continue
		}
		switch {
		case section == "bootstrap":
			if key == "manager_image" {
				cfg.ManagerImage = value
			}
		case section == "models":
			if key == "default" {
				cfg.DefaultSelector = value
			}
		case strings.HasPrefix(section, "models.providers."):
			providerName := strings.TrimSpace(strings.TrimPrefix(section, "models.providers."))
			if providerName == "" {
				continue
			}
			provider := cfg.Providers[providerName]
			switch key {
			case "base_url":
				provider.BaseURL = value
			case "api_key":
				provider.APIKey = value
			case "models":
				models, err := parseCSGClawConfigStringArray(value)
				if err != nil {
					return csgclawModelConfig{}, err
				}
				provider.Models = models
			}
			cfg.Providers[providerName] = provider
		}
	}
	if err := scanner.Err(); err != nil {
		return csgclawModelConfig{}, err
	}
	providerName := cfg.EffectiveProviderName()
	provider, ok := cfg.Providers[providerName]
	if providerName == "" || !ok || strings.TrimSpace(cfg.DefaultSelector) == "" || strings.TrimSpace(provider.BaseURL) == "" || len(provider.Models) == 0 {
		return csgclawModelConfig{}, fmt.Errorf("csgclaw models config is incomplete")
	}
	return cfg, nil
}

func ensureCSGClawConfigDefault(selector string) (bool, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return false, nil
	}
	path, err := csgclawConfigPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	updated, changed := setCSGClawConfigDefault(string(data), selector)
	if !changed {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func setCSGClawConfigDefault(input, selector string) (string, bool) {
	lines := strings.Split(input, "\n")
	inModels := false
	defaultFound := false
	changed := false
	defaultLine := "default = " + strconv.Quote(selector)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inModels && !defaultFound {
				lines = append(lines[:i], append([]string{defaultLine}, lines[i:]...)...)
				return strings.Join(lines, "\n"), true
			}
			inModels = trimmed == "[models]"
			continue
		}
		if !inModels || !strings.HasPrefix(trimmed, "default") {
			continue
		}
		key, value, ok := parseCSGClawConfigKV(trimmed)
		if !ok || key != "default" {
			continue
		}
		defaultFound = true
		if strings.TrimSpace(value) == selector {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + defaultLine
		changed = true
	}
	if inModels && !defaultFound {
		lines = append(lines, defaultLine)
		changed = true
	}
	return strings.Join(lines, "\n"), changed
}

func csgclawConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".csgclaw", "config.toml"), nil
}

func parseCSGClawConfigKV(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, "\"")
		}
	}
	return key, value, true
}

func parseCSGClawConfigStringArray(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("invalid csgclaw array value %q", value)
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if inner == "" {
		return nil, nil
	}
	items := strings.Split(inner, ",")
	models := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if len(item) >= 2 && strings.HasPrefix(item, "\"") && strings.HasSuffix(item, "\"") {
			unquoted, err := strconv.Unquote(item)
			if err != nil {
				return nil, err
			}
			item = unquoted
		}
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		models = append(models, item)
	}
	return models, nil
}

func csgclawModelSelector(providerName, modelID string) string {
	providerName = strings.TrimSpace(providerName)
	modelID = strings.TrimSpace(modelID)
	if providerName == "" || modelID == "" {
		return ""
	}
	return providerName + "." + modelID
}

func csgclawContainsModel(models []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, model := range models {
		if strings.TrimSpace(model) == want {
			return true
		}
	}
	return false
}

func csgclawOrderedModels(selected string, modelIDs []string) []string {
	selected = strings.TrimSpace(selected)
	ordered := make([]string, 0, len(modelIDs)+1)
	seen := make(map[string]struct{}, len(modelIDs)+1)
	appendModel := func(modelID string) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			return
		}
		if _, ok := seen[modelID]; ok {
			return
		}
		seen[modelID] = struct{}{}
		ordered = append(ordered, modelID)
	}

	appendModel(selected)
	for _, modelID := range modelIDs {
		appendModel(modelID)
	}
	return ordered
}
