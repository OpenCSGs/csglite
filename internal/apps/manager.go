package apps

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/xpty"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const (
	progressModePercent        = "percent"
	progressModeIndeterminate  = "indeterminate"
	mirrorBaseURL              = "https://git-devops.opencsg.com/opensource/apps/-/raw/main"
	repoRawBaseURL             = "https://git-devops.opencsg.com/opensource/csghub-lite/-/raw/main"
	installTimeout             = 20 * time.Minute
	latestVersionCacheTTL      = 10 * time.Minute
	latestVersionTimeout       = 3 * time.Second
	latestVersionResponseLimit = 64 * 1024
	installerPTYCols           = 120
	installerPTYRows           = 36
)

var versionTokenPattern = regexp.MustCompile(`(?i)v?\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.-]+)?`)

//go:embed scripts/*
var embeddedScripts embed.FS

type scriptSource struct {
	mirrorURL    string
	embeddedPath string
	args         []string
	requiresPTY  bool
}

type latestVersionSource struct {
	baseURL string
	envVar  string
	format  string
}

type appSpec struct {
	id             string
	binaryName     string
	installMode    string
	progressMode   string
	supported      bool
	disabledReason string
	versionArgs    []string
	latest         *latestVersionSource
	unix           *scriptSource
	windows        *scriptSource
	uninstallUnix  *scriptSource
	uninstallWin   *scriptSource
}

type appState struct {
	info    api.AIAppInfo
	logBuf  *LogBuffer
	cancel  context.CancelFunc
	running bool
}

type latestVersionCacheEntry struct {
	version   string
	expiresAt time.Time
}

type Manager struct {
	cfg        *config.Config
	httpClient *http.Client

	mu          sync.RWMutex
	specs       []appSpec
	states      map[string]*appState
	latestMu    sync.Mutex
	latestCache map[string]latestVersionCacheEntry
}

type installDetectionState struct {
	installPath string
	version     string
	installed   bool
	managed     bool
}

func NewManager(cfg *config.Config) *Manager {
	m := &Manager{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
		specs:       appSpecs(),
		states:      make(map[string]*appState),
		latestCache: make(map[string]latestVersionCacheEntry),
	}

	for _, spec := range m.specs {
		logPath := m.logPath(spec.id)
		status := "idle"
		phase := "ready"
		if !spec.supported {
			status = "disabled"
			phase = spec.disabledReason
		}
		m.states[spec.id] = &appState{
			info: api.AIAppInfo{
				ID:             spec.id,
				Supported:      spec.supported,
				Disabled:       !spec.supported,
				Status:         status,
				Phase:          phase,
				ProgressMode:   spec.progressMode,
				LogPath:        logPath,
				DisabledReason: spec.disabledReason,
				UpdatedAt:      time.Now(),
			},
			logBuf: NewLogBuffer(500),
		}
	}

	_ = m.RefreshAll(context.Background())
	return m
}

func csgclawDisabledReason() string {
	if runtime.GOOS == "windows" {
		return "windows_unsupported"
	}
	return ""
}

func appSpecs() []appSpec {
	return []appSpec{
		{
			id:           "claude-code",
			binaryName:   "claude",
			installMode:  "script",
			progressMode: progressModeIndeterminate,
			supported:    true,
			versionArgs:  []string{"--version"},
			latest: &latestVersionSource{
				baseURL: "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/claude-code-releases",
				envVar:  "CSGHUB_LITE_CLAUDE_DIST_BASE_URL",
			},
			unix: &scriptSource{
				mirrorURL:    repoRawBaseURL + "/internal/apps/scripts/claude-code-install.sh",
				embeddedPath: "scripts/claude-code-install.sh",
				args:         []string{"latest"},
				requiresPTY:  true,
			},
			windows: &scriptSource{
				mirrorURL:    repoRawBaseURL + "/internal/apps/scripts/claude-code-install.ps1",
				embeddedPath: "scripts/claude-code-install.ps1",
				args:         []string{"-Target", "latest"},
				requiresPTY:  true,
			},
			uninstallUnix: &scriptSource{
				embeddedPath: "scripts/claude-code-uninstall.sh",
			},
			uninstallWin: &scriptSource{
				embeddedPath: "scripts/claude-code-uninstall.ps1",
			},
		},
		{
			id:           "open-code",
			binaryName:   "opencode",
			installMode:  "script",
			progressMode: progressModePercent,
			supported:    true,
			versionArgs:  []string{"--version"},
			latest: &latestVersionSource{
				baseURL: "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/open-code-releases",
				envVar:  "CSGHUB_LITE_OPEN_CODE_DIST_BASE_URL",
			},
			unix: &scriptSource{
				mirrorURL:    mirrorBaseURL + "/open-code/install.sh",
				embeddedPath: "scripts/open-code-install.sh",
			},
			windows: &scriptSource{
				mirrorURL:    mirrorBaseURL + "/open-code/install.ps1",
				embeddedPath: "scripts/open-code-install.ps1",
			},
			uninstallUnix: &scriptSource{
				embeddedPath: "scripts/open-code-uninstall.sh",
			},
			uninstallWin: &scriptSource{
				embeddedPath: "scripts/open-code-uninstall.ps1",
			},
		},
		{
			id:           "openclaw",
			binaryName:   "openclaw",
			installMode:  "script",
			progressMode: progressModePercent,
			supported:    true,
			versionArgs:  []string{"--version"},
			unix: &scriptSource{
				mirrorURL:    mirrorBaseURL + "/openclaw/install.sh",
				embeddedPath: "scripts/openclaw-install.sh",
			},
			windows: &scriptSource{
				mirrorURL:    mirrorBaseURL + "/openclaw/install.ps1",
				embeddedPath: "scripts/openclaw-install.ps1",
			},
			uninstallUnix: &scriptSource{
				embeddedPath: "scripts/openclaw-uninstall.sh",
			},
			uninstallWin: &scriptSource{
				embeddedPath: "scripts/openclaw-uninstall.ps1",
			},
		},
		{
			id:             "csgclaw",
			binaryName:     "csgclaw",
			installMode:    "script",
			progressMode:   progressModePercent,
			supported:      runtime.GOOS != "windows",
			disabledReason: csgclawDisabledReason(),
			versionArgs:    []string{"--version"},
			latest: &latestVersionSource{
				baseURL: "https://csgclaw.opencsg.com/releases/latest",
				envVar:  "CSGHUB_LITE_CSGCLAW_LATEST_URL",
				format:  "github-release",
			},
			unix: &scriptSource{
				mirrorURL:    "https://csgclaw.opencsg.com/install.sh",
				embeddedPath: "scripts/csgclaw-install.sh",
			},
			uninstallUnix: &scriptSource{
				embeddedPath: "scripts/csgclaw-uninstall.sh",
			},
		},
		{
			id:           "codex",
			binaryName:   "codex",
			installMode:  "script",
			progressMode: progressModePercent,
			supported:    true,
			versionArgs:  []string{"--version"},
			latest: &latestVersionSource{
				baseURL: "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/codex-releases",
				envVar:  "CSGHUB_LITE_CODEX_DIST_BASE_URL",
			},
			unix: &scriptSource{
				mirrorURL:    mirrorBaseURL + "/codex/install.sh",
				embeddedPath: "scripts/codex-install.sh",
			},
			windows: &scriptSource{
				mirrorURL:    mirrorBaseURL + "/codex/install.ps1",
				embeddedPath: "scripts/codex-install.ps1",
			},
			uninstallUnix: &scriptSource{
				embeddedPath: "scripts/codex-uninstall.sh",
			},
			uninstallWin: &scriptSource{
				embeddedPath: "scripts/codex-uninstall.ps1",
			},
		},
		{
			id:           "pi",
			binaryName:   "pi",
			installMode:  "script",
			progressMode: progressModeIndeterminate,
			supported:    true,
			versionArgs:  []string{"--version"},
			unix: &scriptSource{
				embeddedPath: "scripts/pi-install.sh",
			},
			windows: &scriptSource{
				embeddedPath: "scripts/pi-install.ps1",
			},
			uninstallUnix: &scriptSource{
				embeddedPath: "scripts/pi-uninstall.sh",
			},
			uninstallWin: &scriptSource{
				embeddedPath: "scripts/pi-uninstall.ps1",
			},
		},
		{
			id:             "dify",
			installMode:    "docker",
			progressMode:   progressModeIndeterminate,
			supported:      false,
			disabledReason: "docker_disabled",
		},
		{
			id:             "anythingllm",
			installMode:    "docker",
			progressMode:   progressModeIndeterminate,
			supported:      false,
			disabledReason: "docker_disabled",
		},
	}
}

func (m *Manager) List(ctx context.Context) ([]api.AIAppInfo, error) {
	if err := m.RefreshAll(ctx); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	items := make([]api.AIAppInfo, 0, len(m.specs))
	for _, spec := range m.specs {
		st := m.states[spec.id]
		items = append(items, cloneInfo(st.info))
	}
	return items, nil
}

func (m *Manager) Get(ctx context.Context, appID string) (api.AIAppInfo, error) {
	if err := m.RefreshAll(ctx); err != nil {
		return api.AIAppInfo{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.states[appID]
	if !ok {
		return api.AIAppInfo{}, fmt.Errorf("unknown app %q", appID)
	}
	return cloneInfo(st.info), nil
}

func (m *Manager) RefreshAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, spec := range m.specs {
		st := m.states[spec.id]
		if st == nil || st.running || !spec.supported {
			continue
		}

		m.refreshStateLocked(ctx, spec, st)
	}
	return nil
}

func (m *Manager) Install(appID string) (api.AIAppInfo, error) {
	return m.startAction(appID, "install")
}

func (m *Manager) Uninstall(appID string) (api.AIAppInfo, error) {
	return m.startAction(appID, "uninstall")
}

func (m *Manager) startAction(appID, action string) (api.AIAppInfo, error) {
	m.mu.Lock()
	spec, st, err := m.specStateLocked(appID)
	if err != nil {
		m.mu.Unlock()
		return api.AIAppInfo{}, err
	}
	if !spec.supported {
		info := cloneInfo(st.info)
		m.mu.Unlock()
		return info, errors.New("app is disabled")
	}
	m.refreshStateLocked(context.Background(), spec, st)
	if st.running {
		info := cloneInfo(st.info)
		m.mu.Unlock()
		return info, nil
	}
	if action == "install" && st.info.Installed && !st.info.Managed {
		info := cloneInfo(st.info)
		m.mu.Unlock()
		return info, nil
	}
	if action == "uninstall" && (!st.info.Installed || !st.info.Managed) {
		info := cloneInfo(st.info)
		m.mu.Unlock()
		return info, nil
	}

	st.logBuf.Reset()
	if err := os.MkdirAll(filepath.Dir(st.info.LogPath), 0o755); err != nil {
		m.mu.Unlock()
		return api.AIAppInfo{}, err
	}
	if err := os.WriteFile(st.info.LogPath, nil, 0o644); err != nil {
		m.mu.Unlock()
		return api.AIAppInfo{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	st.cancel = cancel
	st.running = true
	st.info.Status = actionStatus(action)
	st.info.Phase = "starting"
	st.info.Progress = 0
	st.info.LastError = ""
	st.info.UpdatedAt = time.Now()
	info := cloneInfo(st.info)
	m.mu.Unlock()

	go m.runAction(ctx, spec, action)
	return info, nil
}

func (m *Manager) RecentLogs(appID string, n int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.states[appID]
	if !ok {
		return nil, fmt.Errorf("unknown app %q", appID)
	}
	return st.logBuf.Recent(n), nil
}

func (m *Manager) SubscribeLogs(appID string) (chan string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.states[appID]
	if !ok {
		return nil, fmt.Errorf("unknown app %q", appID)
	}
	return st.logBuf.Subscribe(), nil
}

func (m *Manager) UnsubscribeLogs(appID string, ch chan string) {
	m.mu.RLock()
	st := m.states[appID]
	m.mu.RUnlock()
	if st != nil {
		st.logBuf.Unsubscribe(ch)
	}
}

func (m *Manager) EnrichLatestVersion(ctx context.Context, info *api.AIAppInfo) {
	if info == nil || !info.Supported || info.Disabled || !info.Installed || !info.Managed {
		return
	}
	spec, ok := m.specByID(info.ID)
	if !ok || spec.latest == nil {
		return
	}
	latest, ok := m.resolveLatestVersion(ctx, spec)
	if !ok {
		return
	}
	info.LatestVersion = latest
	info.UpdateAvailable = appUpdateAvailable(info.Version, latest)
}

func (m *Manager) specByID(appID string) (appSpec, bool) {
	for _, spec := range m.specs {
		if spec.id == appID {
			return spec, true
		}
	}
	return appSpec{}, false
}

func (m *Manager) resolveLatestVersion(ctx context.Context, spec appSpec) (string, bool) {
	cacheKey := spec.id
	now := time.Now()
	m.latestMu.Lock()
	if cached, ok := m.latestCache[cacheKey]; ok && now.Before(cached.expiresAt) {
		m.latestMu.Unlock()
		return cached.version, cached.version != ""
	}
	m.latestMu.Unlock()

	latest, err := m.fetchLatestVersion(ctx, spec)
	if err != nil {
		log.Printf("AI APP %s: latest version lookup skipped: %v", spec.id, err)
	}

	m.latestMu.Lock()
	m.latestCache[cacheKey] = latestVersionCacheEntry{
		version:   latest,
		expiresAt: now.Add(latestVersionCacheTTL),
	}
	m.latestMu.Unlock()
	return latest, latest != ""
}

func (m *Manager) fetchLatestVersion(ctx context.Context, spec appSpec) (string, error) {
	if spec.latest == nil {
		return "", nil
	}
	baseURL := strings.TrimSpace(spec.latest.baseURL)
	if override := strings.TrimSpace(os.Getenv(spec.latest.envVar)); override != "" {
		baseURL = override
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return "", nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, latestVersionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/latest", nil)
	if spec.latest.format == "github-release" || spec.latest.format == "version-json" {
		req, err = http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL, nil)
	}
	if err != nil {
		return "", err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("latest endpoint returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, latestVersionResponseLimit))
	if err != nil {
		return "", err
	}
	if spec.latest.format == "github-release" {
		var payload struct {
			TagName string `json:"tag_name"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return "", err
		}
		return strings.TrimSpace(payload.TagName), nil
	}
	if spec.latest.format == "version-json" {
		var payload struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return "", err
		}
		return strings.TrimSpace(payload.Version), nil
	}
	return strings.TrimSpace(string(data)), nil
}

func appUpdateAvailable(installedVersion, latestVersion string) bool {
	installed := comparableVersion(installedVersion)
	latest := comparableVersion(latestVersion)
	if installed == "" || latest == "" {
		return false
	}
	if cmp, ok := compareVersionOrder(installed, latest); ok {
		return cmp < 0
	}
	return installed != latest
}

func comparableVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if token := versionTokenPattern.FindString(version); token != "" {
		return strings.TrimPrefix(strings.ToLower(token), "v")
	}
	return strings.TrimPrefix(strings.ToLower(version), "v")
}

func compareVersionOrder(installed, latest string) (int, bool) {
	installedParts, ok := versionNumberParts(installed)
	if !ok {
		return 0, false
	}
	latestParts, ok := versionNumberParts(latest)
	if !ok {
		return 0, false
	}

	maxLen := max(len(installedParts), len(latestParts))
	for i := 0; i < maxLen; i++ {
		installedPart := 0
		if i < len(installedParts) {
			installedPart = installedParts[i]
		}
		latestPart := 0
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if installedPart < latestPart {
			return -1, true
		}
		if installedPart > latestPart {
			return 1, true
		}
	}
	return 0, true
}

func versionNumberParts(version string) ([]int, bool) {
	version = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(version)), "v")
	version = strings.FieldsFunc(version, func(r rune) bool {
		return r == '-' || r == '+'
	})[0]
	rawParts := strings.Split(version, ".")
	if len(rawParts) == 0 {
		return nil, false
	}
	parts := make([]int, 0, len(rawParts))
	for _, raw := range rawParts {
		if raw == "" {
			return nil, false
		}
		part, err := strconv.Atoi(raw)
		if err != nil {
			return nil, false
		}
		parts = append(parts, part)
	}
	return parts, true
}

func actionStatus(action string) string {
	if action == "uninstall" {
		return "uninstalling"
	}
	return "installing"
}

func actionRunnerName(action string) string {
	if action == "uninstall" {
		return "uninstaller"
	}
	return "installer"
}

func (m *Manager) runAction(ctx context.Context, spec appSpec, action string) {
	log.Printf("AI APP %s: %s started", spec.id, action)
	logFile, err := os.OpenFile(m.logPath(spec.id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o644)
	if err != nil {
		m.failAction(spec, action, fmt.Sprintf("open log file: %v", err))
		return
	}
	defer logFile.Close()

	runnerName := actionRunnerName(action)
	m.appendLog(spec.id, logFile, fmt.Sprintf("INFO: preparing %s", runnerName))
	source, err := m.currentScriptSource(spec, action)
	if err != nil {
		m.failAction(spec, action, err.Error())
		return
	}

	content, resolvedFrom, err := m.resolveScript(spec.id, source)
	if err != nil {
		m.failAction(spec, action, err.Error())
		return
	}

	m.appendLog(spec.id, logFile, fmt.Sprintf("INFO: %s source %s", runnerName, resolvedFrom))
	log.Printf("AI APP %s: %s source resolved from %s", spec.id, runnerName, resolvedFrom)
	m.updateProgress(spec.id, 5, "preflight")

	tmpPath, err := m.writeTempScript(spec.id, source, content)
	if err != nil {
		m.failAction(spec, action, err.Error())
		return
	}
	defer os.Remove(tmpPath)

	cmd, err := buildScriptCommand(tmpPath, source)
	if err != nil {
		m.failAction(spec, action, err.Error())
		return
	}
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmd.Env = append(os.Environ(), cmd.Env...)
	if err := m.runScriptCommand(ctx, spec.id, source, cmd, logFile); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			m.failAction(spec, action, fmt.Sprintf("%s timed out after %s", runnerName, installTimeout))
			return
		}
		m.failAction(spec, action, m.actionErrorMessage(spec.id, err.Error()))
		return
	}

	verifyPhase := "verifying"
	verifyErr := "installer completed but binary was not found on PATH"
	if action == "uninstall" {
		verifyPhase = "verifying_uninstall"
		verifyErr = "uninstaller completed but binary is still found on PATH"
	}
	m.updateProgress(spec.id, 95, verifyPhase)
	installPath, version, installed := detectInstalled(context.Background(), spec)
	if action == "uninstall" {
		if installed {
			m.failAction(spec, action, verifyErr)
			return
		}
		m.completeUninstall(spec)
		m.appendLog(spec.id, logFile, "INFO: uninstallation complete")
		log.Printf("AI APP %s: uninstall complete", spec.id)
		return
	}

	if !installed {
		m.failAction(spec, action, verifyErr)
		return
	}

	m.completeInstall(spec, installPath, version)
	m.appendLog(spec.id, logFile, "INFO: installation complete")
	log.Printf("AI APP %s: install complete path=%q version=%q", spec.id, installPath, version)
}

func installerPTYEnv() []string {
	return []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"FORCE_COLOR=1",
		"CLICOLOR=1",
		"TERM_PROGRAM=csghub-lite",
	}
}

func (m *Manager) runScriptCommand(ctx context.Context, appID string, source *scriptSource, cmd *exec.Cmd, logFile *os.File) error {
	if source != nil && source.requiresPTY {
		cmd.Env = append(cmd.Env, installerPTYEnv()...)
		return m.runScriptCommandWithPTY(ctx, appID, cmd, logFile)
	}
	return m.runScriptCommandWithPipes(appID, cmd, logFile)
}

func (m *Manager) runScriptCommandWithPipes(appID string, cmd *exec.Cmd, logFile *os.File) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	m.appendLog(appID, logFile, fmt.Sprintf("INFO: running %s", strings.Join(cmd.Args, " ")))
	log.Printf("AI APP %s: running command %s", appID, strings.Join(cmd.Args, " "))

	var wg sync.WaitGroup
	wg.Add(2)
	go m.consumeOutput(&wg, appID, stdout, logFile)
	go m.consumeOutput(&wg, appID, stderr, logFile)
	waitErr := cmd.Wait()
	wg.Wait()
	return waitErr
}

func (m *Manager) runScriptCommandWithPTY(ctx context.Context, appID string, cmd *exec.Cmd, logFile *os.File) error {
	pty, err := xpty.NewPty(installerPTYCols, installerPTYRows)
	if err != nil {
		return fmt.Errorf("creating pseudo-terminal: %w", err)
	}

	if err := pty.Start(cmd); err != nil {
		_ = pty.Close()
		return fmt.Errorf("starting command in pseudo-terminal: %w", err)
	}
	m.appendLog(appID, logFile, fmt.Sprintf("INFO: running %s", strings.Join(cmd.Args, " ")))
	log.Printf("AI APP %s: running command %s", appID, strings.Join(cmd.Args, " "))

	var wg sync.WaitGroup
	wg.Add(1)
	go m.consumeOutput(&wg, appID, pty, logFile)

	waitErr := xpty.WaitProcess(ctx, cmd)
	_ = pty.Close()
	wg.Wait()
	return waitErr
}

func (m *Manager) consumeOutput(wg *sync.WaitGroup, appID string, r io.Reader, logFile *os.File) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if handled := m.handleControlLine(appID, line); handled {
			continue
		}
		m.appendLog(appID, logFile, line)
	}
	if err := scanner.Err(); err != nil {
		m.appendLog(appID, logFile, fmt.Sprintf("WARN: stream read error: %v", err))
	}
}

func (m *Manager) handleControlLine(appID, line string) bool {
	if !strings.HasPrefix(line, "CSGHUB_PROGRESS|") {
		return false
	}
	parts := strings.SplitN(line, "|", 3)
	if len(parts) != 3 {
		return true
	}
	value, err := strconv.Atoi(parts[1])
	if err != nil {
		return true
	}
	phase := parts[2]
	m.updateProgress(appID, value, phase)
	log.Printf("AI APP %s: progress %d%% phase=%s", appID, value, phase)
	return true
}

func (m *Manager) updateProgress(appID string, progress int, phase string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[appID]
	if st == nil {
		return
	}
	if st.info.Status != "installing" && st.info.Status != "uninstalling" {
		st.info.Status = "installing"
	}
	st.info.Phase = phase
	if st.info.ProgressMode == progressModePercent {
		st.info.Progress = progress
	}
	st.info.UpdatedAt = time.Now()
}

func (m *Manager) completeInstall(spec appSpec, installPath, version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[spec.id]
	if st == nil {
		return
	}
	st.running = false
	st.cancel = nil
	_ = m.markManagedInstall(spec.id)
	st.info.Status = "installed"
	st.info.Phase = "installed"
	st.info.Progress = 100
	st.info.Installed = true
	st.info.Managed = true
	st.info.InstallPath = installPath
	st.info.Version = version
	st.info.LastError = ""
	st.info.UpdatedAt = time.Now()
}

func (m *Manager) completeUninstall(spec appSpec) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[spec.id]
	if st == nil {
		return
	}
	st.running = false
	st.cancel = nil
	_ = m.clearManagedInstallMarker(spec.id)
	st.info.Status = "idle"
	st.info.Phase = "ready"
	st.info.Progress = 0
	st.info.Installed = false
	st.info.Managed = false
	st.info.InstallPath = ""
	st.info.Version = ""
	st.info.LastError = ""
	st.info.UpdatedAt = time.Now()
}

func (m *Manager) failAction(spec appSpec, action, errMsg string) {
	detected := m.detectInstallState(context.Background(), spec)
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[spec.id]
	if st == nil {
		return
	}
	st.running = false
	st.cancel = nil
	if action == "uninstall" {
		if detected.installed {
			st.info.Status = "installed"
			st.info.Phase = "uninstall_failed"
			st.info.Progress = 100
			st.info.Installed = true
			st.info.Managed = detected.managed
			st.info.InstallPath = detected.installPath
			st.info.Version = detected.version
		} else {
			st.info.Status = "failed"
			st.info.Phase = "uninstall_failed"
			st.info.Progress = 0
			st.info.Installed = false
			st.info.Managed = false
			st.info.InstallPath = ""
			st.info.Version = ""
		}
	} else if detected.installed {
		st.info.Status = "installed"
		st.info.Phase = "installed"
		st.info.Progress = 100
		st.info.Installed = true
		st.info.Managed = detected.managed
		st.info.InstallPath = detected.installPath
		st.info.Version = detected.version
	} else {
		st.info.Status = "failed"
		st.info.Phase = "failed"
		st.info.Progress = 0
		st.info.Installed = false
		st.info.Managed = false
		st.info.InstallPath = ""
		st.info.Version = ""
	}
	st.info.LastError = errMsg
	st.info.UpdatedAt = time.Now()
	log.Printf("AI APP %s: %s failed: %s", spec.id, action, errMsg)
}

func (m *Manager) actionErrorMessage(appID, fallback string) string {
	m.mu.RLock()
	st := m.states[appID]
	m.mu.RUnlock()
	if st == nil {
		return fallback
	}
	if msg := summarizeFailureLogs(st.logBuf.Recent(50)); msg != "" {
		return msg
	}
	return fallback
}

func summarizeFailureLogs(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := stripLogTimestamp(lines[i])
		if strings.HasPrefix(line, "ERROR:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "ERROR:"))
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := stripLogTimestamp(lines[i])
		if actionableNPMError(line) {
			return line
		}
	}
	return ""
}

func actionableNPMError(line string) bool {
	prefixes := []string{"npm error ", "npm ERR! "}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		skipPrefixes := []string{
			prefix + "code ",
			prefix + "syscall ",
			prefix + "path ",
			prefix + "dest ",
			prefix + "errno ",
			prefix + "A complete log ",
		}
		for _, skip := range skipPrefixes {
			if strings.HasPrefix(line, skip) {
				return false
			}
		}
		return true
	}
	return false
}

func stripLogTimestamp(line string) string {
	line = strings.TrimSpace(line)
	if len(line) <= 20 || line[19] != ' ' {
		return line
	}
	if _, err := time.Parse("2006-01-02 15:04:05", line[:19]); err == nil {
		return strings.TrimSpace(line[20:])
	}
	return line
}

func (m *Manager) appendLog(appID string, logFile *os.File, line string) {
	m.mu.RLock()
	st := m.states[appID]
	m.mu.RUnlock()
	if st == nil {
		return
	}
	formatted := fmt.Sprintf("%s %s", time.Now().Format("2006-01-02 15:04:05"), trimLine(line))
	st.logBuf.Append(formatted)
	if logFile != nil {
		_, _ = logFile.WriteString(formatted + "\n")
	}
}

func (m *Manager) resolveScript(appID string, source *scriptSource) ([]byte, string, error) {
	if source == nil {
		return nil, "", fmt.Errorf("no script configured for %s on %s", appID, runtime.GOOS)
	}
	data, err := embeddedScripts.ReadFile(source.embeddedPath)
	if err == nil {
		return data, "embedded:" + source.embeddedPath, nil
	}

	if source.mirrorURL != "" {
		req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, source.mirrorURL, nil)
		if reqErr == nil {
			resp, httpErr := m.httpClient.Do(req)
			if httpErr == nil {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					remoteData, readErr := io.ReadAll(resp.Body)
					if readErr == nil {
						return remoteData, source.mirrorURL, nil
					}
				}
			}
		}
	}

	return nil, "", fmt.Errorf("read embedded script: %w", err)
}

func (m *Manager) tempScriptDir() (string, error) {
	candidates := []string{}
	if dir := os.TempDir(); dir != "" {
		candidates = append(candidates, dir)
	}
	if home, err := config.AppHome(); err == nil && home != "" {
		fallback := filepath.Join(home, "apps", "tmp")
		if len(candidates) == 0 || !samePath(candidates[0], fallback) {
			candidates = append(candidates, fallback)
		}
	}

	var errs []string
	for _, dir := range candidates {
		// Recreate a missing TMPDIR before we try to create the temp script.
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return dir, nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", dir, err))
		}
	}
	if len(errs) == 0 {
		return "", fmt.Errorf("no temp directory available")
	}
	return "", fmt.Errorf("prepare temp directory: %s", strings.Join(errs, "; "))
}

func (m *Manager) writeTempScript(appID string, source *scriptSource, content []byte) (string, error) {
	ext := ".sh"
	if runtime.GOOS == "windows" {
		ext = ".ps1"
	}
	tempDir, err := m.tempScriptDir()
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(tempDir, appID+"-*"+ext)
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, bytes.NewReader(content)); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			return "", err
		}
	}
	return tmp.Name(), nil
}

func buildScriptCommand(scriptPath string, source *scriptSource) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		powershell, err := exec.LookPath("powershell")
		if err != nil {
			return nil, fmt.Errorf("powershell not found: %w", err)
		}
		args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
		args = append(args, source.args...)
		return exec.Command(powershell, args...), nil
	}

	bash, err := exec.LookPath("bash")
	if err != nil {
		return nil, fmt.Errorf("bash not found: %w", err)
	}

	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = bash
	}
	if _, err := os.Stat(shellPath); err != nil {
		shellPath = bash
	}

	args := []string{
		"-lc",
		`exec "$@"`,
		"csghub-app-installer",
		bash,
		scriptPath,
	}
	args = append(args, source.args...)
	return exec.Command(shellPath, args...), nil
}

func (m *Manager) currentScriptSource(spec appSpec, action string) (*scriptSource, error) {
	if runtime.GOOS == "windows" {
		if action == "uninstall" {
			if spec.uninstallWin == nil {
				return nil, fmt.Errorf("no Windows uninstaller configured for %s", spec.id)
			}
			return spec.uninstallWin, nil
		}
		if spec.windows == nil {
			return nil, fmt.Errorf("no Windows installer configured for %s", spec.id)
		}
		return spec.windows, nil
	}
	if action == "uninstall" {
		if spec.uninstallUnix == nil {
			return nil, fmt.Errorf("no Unix uninstaller configured for %s", spec.id)
		}
		return spec.uninstallUnix, nil
	}
	if spec.unix == nil {
		return nil, fmt.Errorf("no Unix installer configured for %s", spec.id)
	}
	return spec.unix, nil
}

func detectInstalled(ctx context.Context, spec appSpec) (string, string, bool) {
	if spec.binaryName == "" {
		return "", "", false
	}
	path, ok := detectInstalledBinaryPath(spec.binaryName)
	if !ok {
		return "", "", false
	}
	version := path
	if len(spec.versionArgs) > 0 {
		cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(cmdCtx, path, spec.versionArgs...).CombinedOutput()
		if err == nil {
			version = strings.TrimSpace(string(out))
		}
	}
	return path, version, true
}

func detectInstalledBinaryPath(binaryName string) (string, bool) {
	if path, err := exec.LookPath(binaryName); err == nil {
		return path, true
	}
	for _, dir := range commonInstallerBinDirs() {
		if path, ok := lookupBinaryInDir(dir, binaryName); ok {
			return path, true
		}
	}
	return "", false
}

func commonInstallerBinDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home != "" {
		dirs = append([]string{
			filepath.Join(home, "bin"),
			filepath.Join(home, ".local", "bin"),
		}, dirs...)
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			dirs = append([]string{filepath.Join(appData, "npm")}, dirs...)
		}
	}
	return uniqueNonEmptyPaths(dirs)
}

func lookupBinaryInDir(dir, name string) (string, bool) {
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{"", ".exe", ".cmd", ".bat", ".ps1"}
	}
	for _, ext := range exts {
		path := filepath.Join(dir, name+ext)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func uniqueNonEmptyPaths(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	unique := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		unique = append(unique, item)
	}
	return unique
}

func (m *Manager) refreshStateLocked(ctx context.Context, spec appSpec, st *appState) {
	detected := m.detectInstallState(ctx, spec)
	st.info.Installed = detected.installed
	st.info.Managed = detected.managed
	st.info.InstallPath = detected.installPath
	st.info.Version = detected.version
	st.info.ProgressMode = spec.progressMode
	st.info.UpdatedAt = time.Now()

	if detected.installed {
		st.info.Status = "installed"
		if st.info.Phase == "uninstall_failed" && st.info.LastError != "" {
			st.info.Phase = "uninstall_failed"
		} else {
			st.info.Phase = "installed"
			st.info.LastError = ""
		}
		st.info.Progress = 100
		return
	}

	if st.info.Status == "failed" {
		if st.info.Phase == "" {
			st.info.Phase = "failed"
		}
		st.info.Progress = 0
		return
	}

	st.info.Status = "idle"
	st.info.Phase = "ready"
	st.info.Progress = 0
}

func (m *Manager) detectInstallState(ctx context.Context, spec appSpec) installDetectionState {
	installPath, version, installed := detectInstalled(ctx, spec)
	if !installed {
		_ = m.clearManagedInstallMarker(spec.id)
		return installDetectionState{}
	}
	managed := m.hasManagedInstallMarker(spec.id)
	if !managed && inferLegacyManagedInstall(spec, installPath) {
		managed = true
		_ = m.markManagedInstall(spec.id)
	}
	return installDetectionState{
		installPath: installPath,
		version:     version,
		installed:   true,
		managed:     managed,
	}
}

func inferLegacyManagedInstall(spec appSpec, installPath string) bool {
	switch spec.id {
	case "claude-code":
		return looksLikeLegacyRuntimeInstall(installPath, spec.binaryName, "claude")
	case "open-code":
		return looksLikeLegacyRuntimeInstall(installPath, spec.binaryName, "opencode")
	case "codex":
		return looksLikeLegacyRuntimeInstall(installPath, spec.binaryName, "codex")
	case "openclaw":
		return looksLikeLegacyOpenClawInstall(installPath)
	default:
		return false
	}
}

func looksLikeLegacyRuntimeInstall(installPath, binaryName, runtimeDir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || installPath == "" {
		return false
	}

	launcherPath := filepath.Join(home, ".local", "bin", launcherBinaryName(binaryName))
	if !samePath(installPath, launcherPath) {
		return false
	}

	runtimeRoot := filepath.Join(home, ".local", "share", runtimeDir, "versions")
	if resolvedPath, err := filepath.EvalSymlinks(installPath); err == nil && pathWithinBase(resolvedPath, runtimeRoot) {
		return true
	}

	_, ok := findLegacyRuntimeBinary(runtimeRoot, binaryName)
	return ok
}

func findLegacyRuntimeBinary(runtimeRoot, binaryName string) (string, bool) {
	entries, err := os.ReadDir(runtimeRoot)
	if err != nil {
		return "", false
	}

	name := launcherBinaryName(binaryName)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(runtimeRoot, entry.Name(), name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func looksLikeLegacyOpenClawInstall(installPath string) bool {
	if runtime.GOOS == "windows" || installPath == "" {
		return false
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}

	candidates := []string{
		filepath.Join(home, "bin", "openclaw"),
		filepath.Join(home, ".local", "bin", "openclaw"),
	}
	matchedLauncher := false
	for _, candidate := range candidates {
		if samePath(installPath, candidate) {
			matchedLauncher = true
			break
		}
	}
	if !matchedLauncher {
		return false
	}

	data, err := os.ReadFile(installPath)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 3 {
		return false
	}

	line1 := strings.TrimSpace(lines[0])
	line2 := strings.TrimSpace(lines[1])
	line3 := strings.TrimSpace(lines[2])
	if line1 != "#!/usr/bin/env bash" {
		return false
	}
	if !strings.HasPrefix(line2, `export PATH="`) || !strings.HasSuffix(line2, `:$PATH"`) {
		return false
	}
	if !strings.HasPrefix(line3, `exec "`) || !strings.HasSuffix(line3, `" "$@"`) {
		return false
	}

	target := strings.TrimSuffix(strings.TrimPrefix(line3, `exec "`), `" "$@"`)
	if target == "" || samePath(target, installPath) {
		return false
	}
	info, err := os.Stat(target)
	return err == nil && !info.IsDir()
}

func launcherBinaryName(binaryName string) string {
	if runtime.GOOS == "windows" {
		return binaryName + ".exe"
	}
	return binaryName
}

func samePath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}

	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func pathWithinBase(target, base string) bool {
	if target == "" || base == "" {
		return false
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (m *Manager) managedMarkerPath(appID string) string {
	home, err := config.AppHome()
	if err != nil {
		return filepath.Join(os.TempDir(), appID+".installed")
	}
	return filepath.Join(home, "apps", "managed", appID+".installed")
}

func (m *Manager) hasManagedInstallMarker(appID string) bool {
	_, err := os.Stat(m.managedMarkerPath(appID))
	return err == nil
}

func (m *Manager) markManagedInstall(appID string) error {
	path := m.managedMarkerPath(appID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("managed\n"), 0o644)
}

func (m *Manager) clearManagedInstallMarker(appID string) error {
	err := os.Remove(m.managedMarkerPath(appID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (m *Manager) logPath(appID string) string {
	home, err := config.AppHome()
	if err != nil {
		return filepath.Join(os.TempDir(), appID+".log")
	}
	return filepath.Join(home, "apps", "logs", appID+".log")
}

func (m *Manager) specStateLocked(appID string) (appSpec, *appState, error) {
	for _, spec := range m.specs {
		if spec.id == appID {
			st, ok := m.states[appID]
			if !ok {
				return appSpec{}, nil, fmt.Errorf("unknown app %q", appID)
			}
			return spec, st, nil
		}
	}
	return appSpec{}, nil, fmt.Errorf("unknown app %q", appID)
}

func trimLine(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "(empty line)"
	}
	return line
}

func cloneInfo(info api.AIAppInfo) api.AIAppInfo {
	return info
}
