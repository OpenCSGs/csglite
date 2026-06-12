package server

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/xpty"
	"github.com/gorilla/websocket"

	"github.com/opencsgs/csghub-lite/internal/claudeagent"
	"github.com/opencsgs/csghub-lite/internal/codexagent"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/opencodeagent"
	"github.com/opencsgs/csghub-lite/internal/piagent"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const (
	aiAppShellDefaultCols   = 120
	aiAppShellDefaultRows   = 36
	aiAppShellReplayLimit   = 256 * 1024
	aiAppShellEventBuffer   = 1024
	aiAppShellReadBuffer    = 64 * 1024
	aiAppShellWriteBatch    = 64 * 1024
	openCodeWebProviderID   = "csghub-lite"
	codexWebProviderID      = "csghub_lite"
	codexCloudContextWindow = 200000
	codexLocalContextWindow = 8192
	codexBaseInstructions   = "You are Codex, a coding agent. You and the user share the same workspace and collaborate to achieve the user's goals. Focus on practical, safe, concise help for software tasks."
)

var (
	aiAppShellIdleTimeout     = 15 * time.Minute
	aiAppShellDetachedTimeout = time.Hour
	aiAppShellPingInterval    = 30 * time.Second
	aiAppShellPongWait        = 75 * time.Second
	aiAppShellWriteTimeout    = 10 * time.Second
)

var aiAppShellUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type aiAppOpenTarget struct {
	AppID       string
	DisplayName string
	Binaries    []string
}

type aiAppPreparedLaunch struct {
	Binary string
	Args   []string
	Env    []string
	Dir    string
}

type codexModelCatalog struct {
	Models []codexModelCatalogEntry `json:"models"`
}

type codexModelCatalogEntry struct {
	Slug                       string                       `json:"slug"`
	DisplayName                string                       `json:"display_name"`
	Description                string                       `json:"description"`
	SupportedReasoningLevels   []codexReasoningEffortPreset `json:"supported_reasoning_levels"`
	ShellType                  string                       `json:"shell_type"`
	Visibility                 string                       `json:"visibility"`
	SupportedInAPI             bool                         `json:"supported_in_api"`
	Priority                   int                          `json:"priority"`
	BaseInstructions           string                       `json:"base_instructions"`
	SupportsReasoningSummaries bool                         `json:"supports_reasoning_summaries"`
	SupportVerbosity           bool                         `json:"support_verbosity"`
	TruncationPolicy           codexTruncationPolicy        `json:"truncation_policy"`
	SupportsParallelToolCalls  bool                         `json:"supports_parallel_tool_calls"`
	ExperimentalSupportedTools []string                     `json:"experimental_supported_tools"`
	InputModalities            []string                     `json:"input_modalities,omitempty"`
	ContextWindow              int64                        `json:"context_window,omitempty"`
}

type codexReasoningEffortPreset struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

type aiAppShellClientMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type aiAppShellControlMessage struct {
	Type     string `json:"type"`
	Session  string `json:"session_id,omitempty"`
	AppID    string `json:"app_id,omitempty"`
	Title    string `json:"title,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
	WorkDir  string `json:"work_dir,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Error    string `json:"error,omitempty"`
}

type aiAppShellEvent struct {
	output []byte
	exit   *aiAppShellControlMessage
}

type aiAppShellAttach struct {
	ready   aiAppShellControlMessage
	replay  []byte
	events  chan aiAppShellEvent
	exitMsg *aiAppShellControlMessage
}

type aiAppShellSession struct {
	manager *aiAppShellManager

	id      string
	appID   string
	title   string
	modelID string
	workDir string
	cmd     *exec.Cmd
	pty     xpty.Pty

	mu          sync.Mutex
	replay      []byte
	subs        map[chan aiAppShellEvent]struct{}
	done        bool
	exitCode    int
	exitErr     string
	idleTimer   *time.Timer
	terminating bool
}

type aiAppShellManager struct {
	mu       sync.RWMutex
	sessions map[string]*aiAppShellSession
}

func newAIAppShellManager() *aiAppShellManager {
	return &aiAppShellManager{
		sessions: make(map[string]*aiAppShellSession),
	}
}

func (m *aiAppShellManager) Create(appID, title, modelID string, prepared aiAppPreparedLaunch) (*aiAppShellSession, error) {
	pty, err := xpty.NewPty(aiAppShellDefaultCols, aiAppShellDefaultRows)
	if err != nil {
		return nil, fmt.Errorf("creating terminal: %w", err)
	}

	cmd := exec.Command(prepared.Binary, prepared.Args...)
	cmd.Env = prepared.Env
	cmd.Dir = prepared.Dir
	prepareAIAppShellCommand(cmd)

	if err := pty.Start(cmd); err != nil {
		_ = pty.Close()
		return nil, fmt.Errorf("starting %s terminal: %w", title, err)
	}
	log.Printf("AI APP %s: shell process started title=%q pid=%d model=%q work_dir=%q command=%s %s", appID, title, cmd.Process.Pid, modelID, prepared.Dir, prepared.Binary, strings.Join(prepared.Args, " "))

	session := &aiAppShellSession{
		manager: m,
		id:      newAIAppShellSessionID(),
		appID:   appID,
		title:   title,
		modelID: modelID,
		workDir: prepared.Dir,
		cmd:     cmd,
		pty:     pty,
		subs:    make(map[chan aiAppShellEvent]struct{}),
	}

	m.mu.Lock()
	m.sessions[session.id] = session
	m.mu.Unlock()

	session.scheduleDetachedTimeout()
	session.start()
	return session, nil
}

func (m *aiAppShellManager) Get(id string) (*aiAppShellSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[id]
	return session, ok
}

func (m *aiAppShellManager) Close(id string) bool {
	session, ok := m.Get(id)
	if !ok {
		return false
	}
	session.Terminate()
	m.remove(id)
	return true
}

func (m *aiAppShellManager) CloseAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	sessions := make([]*aiAppShellSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*aiAppShellSession)
	m.mu.Unlock()

	for _, session := range sessions {
		session.Terminate()
	}
}

func (m *aiAppShellManager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

func (s *aiAppShellSession) start() {
	go s.streamOutput()
	go s.wait()
}

func (s *aiAppShellSession) streamOutput() {
	buf := make([]byte, aiAppShellReadBuffer)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.appendReplay(chunk)
			s.touchActivity()
			s.broadcast(aiAppShellEvent{output: chunk})
		}
		if err != nil {
			if err != io.EOF {
				// Best effort: the wait goroutine will publish the final exit state.
			}
			return
		}
	}
}

func (s *aiAppShellSession) wait() {
	err := xpty.WaitProcess(context.Background(), s.cmd)

	exitCode := 0
	if s.cmd.ProcessState != nil {
		exitCode = s.cmd.ProcessState.ExitCode()
	}
	exitErr := ""
	if err != nil {
		exitErr = err.Error()
		if exitCode == 0 {
			exitCode = 1
		}
	}
	log.Printf("AI APP %s: shell session exited id=%s exit_code=%d error=%q", s.appID, s.id, exitCode, exitErr)

	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	s.exitCode = exitCode
	s.exitErr = exitErr
	subscribers := s.subscribersLocked()
	if len(subscribers) == 0 {
		s.scheduleIdleTimeoutLocked()
	}
	s.mu.Unlock()

	s.broadcast(aiAppShellEvent{
		exit: &aiAppShellControlMessage{
			Type:     "exit",
			Session:  s.id,
			AppID:    s.appID,
			Title:    s.title,
			ModelID:  s.modelID,
			WorkDir:  s.workDir,
			ExitCode: exitCode,
			Error:    exitErr,
		},
	})
	_ = s.pty.Close()
}

func (s *aiAppShellSession) Attach() aiAppShellAttach {
	s.mu.Lock()
	defer s.mu.Unlock()

	attach := aiAppShellAttach{
		ready: aiAppShellControlMessage{
			Type:    "ready",
			Session: s.id,
			AppID:   s.appID,
			Title:   s.title,
			ModelID: s.modelID,
			WorkDir: s.workDir,
		},
		replay: append([]byte(nil), s.replay...),
	}

	if s.done {
		s.scheduleIdleTimeoutLocked()
		attach.exitMsg = &aiAppShellControlMessage{
			Type:     "exit",
			Session:  s.id,
			AppID:    s.appID,
			Title:    s.title,
			ModelID:  s.modelID,
			WorkDir:  s.workDir,
			ExitCode: s.exitCode,
			Error:    s.exitErr,
		}
		return attach
	}

	s.stopIdleTimeoutLocked()
	ch := make(chan aiAppShellEvent, aiAppShellEventBuffer)
	s.subs[ch] = struct{}{}
	attach.events = ch
	return attach
}

func (s *aiAppShellSession) Detach(ch chan aiAppShellEvent) {
	if ch == nil {
		return
	}

	s.mu.Lock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
	if len(s.subs) == 0 {
		if s.done {
			s.scheduleIdleTimeoutLocked()
		} else {
			s.scheduleDetachedTimeoutLocked()
		}
	}
	s.mu.Unlock()
}

func (s *aiAppShellSession) WriteInput(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	_, err := s.pty.Write(p)
	if err == nil {
		s.touchActivity()
	}
	return err
}

func (s *aiAppShellSession) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	if err := s.pty.Resize(cols, rows); err != nil {
		return err
	}
	s.touchActivity()
	return nil
}

func (s *aiAppShellSession) Terminate() {
	s.mu.Lock()
	if s.terminating {
		s.mu.Unlock()
		return
	}
	s.terminating = true
	s.stopIdleTimeoutLocked()
	var process *os.Process
	if s.cmd != nil {
		process = s.cmd.Process
	}
	pty := s.pty
	s.mu.Unlock()

	if process != nil {
		terminateAIAppShellProcess(process)
	}
	if pty != nil {
		_ = pty.Close()
	}
}

func (s *aiAppShellSession) touchActivity() {
	s.mu.Lock()
	if s.done {
		s.scheduleIdleTimeoutLocked()
	}
	s.mu.Unlock()
}

func (s *aiAppShellSession) appendReplay(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.replay = append(s.replay, chunk...)
	if len(s.replay) > aiAppShellReplayLimit {
		s.replay = append([]byte(nil), s.replay[len(s.replay)-aiAppShellReplayLimit:]...)
	}
}

func (s *aiAppShellSession) broadcast(event aiAppShellEvent) {
	s.mu.Lock()
	subscribers := s.subscribersLocked()
	s.mu.Unlock()
	for _, ch := range subscribers {
		sendAIAppShellEvent(ch, event)
	}
}

func sendAIAppShellEvent(ch chan aiAppShellEvent, event aiAppShellEvent) (sent bool) {
	sent = true
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	ch <- event
	return sent
}

func drainAIAppShellOutput(first aiAppShellEvent, events <-chan aiAppShellEvent) ([]byte, *aiAppShellControlMessage, bool) {
	payload := append([]byte(nil), first.output...)
	if first.exit != nil {
		return payload, first.exit, false
	}

	for len(payload) < aiAppShellWriteBatch {
		select {
		case next, ok := <-events:
			if !ok {
				return payload, nil, true
			}
			if len(next.output) > 0 {
				payload = append(payload, next.output...)
			}
			if next.exit != nil {
				return payload, next.exit, false
			}
		default:
			return payload, nil, false
		}
	}

	return payload, nil, false
}

func (s *aiAppShellSession) subscribersLocked() []chan aiAppShellEvent {
	subscribers := make([]chan aiAppShellEvent, 0, len(s.subs))
	for ch := range s.subs {
		subscribers = append(subscribers, ch)
	}
	return subscribers
}

func (s *aiAppShellSession) scheduleIdleTimeout() {
	s.mu.Lock()
	s.scheduleIdleTimeoutLocked()
	s.mu.Unlock()
}

func (s *aiAppShellSession) scheduleIdleTimeoutLocked() {
	s.scheduleTimeoutLocked(aiAppShellIdleTimeout)
}

func (s *aiAppShellSession) scheduleDetachedTimeout() {
	s.mu.Lock()
	s.scheduleDetachedTimeoutLocked()
	s.mu.Unlock()
}

func (s *aiAppShellSession) scheduleDetachedTimeoutLocked() {
	s.scheduleTimeoutLocked(aiAppShellDetachedTimeout)
}

func (s *aiAppShellSession) scheduleTimeoutLocked(timeout time.Duration) {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = time.AfterFunc(timeout, func() {
		s.Terminate()
		s.manager.remove(s.id)
	})
}

func (s *aiAppShellSession) stopIdleTimeoutLocked() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
}

func (s *Server) openAIAppShellURL(ctx context.Context, appID, requestedModel, requestedSource, requestedWorkDir string, publicBaseURL ...string) (string, error) {
	if s.appShells == nil {
		s.appShells = newAIAppShellManager()
	}

	target, err := resolveAIAppOpenTarget(appID)
	if err != nil {
		return "", err
	}

	defaultModel, modelIDs, err := s.resolveAIAppShellLaunchModels(ctx, appID, requestedModel, requestedSource)
	if err != nil {
		return "", err
	}
	log.Printf("AI APP %s: preparing shell launch model=%q models=%d work_dir=%q", appID, defaultModel, len(modelIDs), requestedWorkDir)

	prepared, err := s.prepareAIAppShellLaunch(target, defaultModel, modelIDs, requestedWorkDir)
	if err != nil {
		return "", err
	}

	session, err := s.appShells.Create(target.AppID, target.DisplayName, defaultModel, prepared)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(requestedModel) != "" {
		s.savePreferredAIAppModel(appID, defaultModel)
	}

	baseURL := s.localBaseURL()
	if len(publicBaseURL) > 0 && strings.TrimSpace(publicBaseURL[0]) != "" {
		baseURL = strings.TrimSpace(publicBaseURL[0])
	}
	u, err := neturl.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u.Path = "/ai-apps/shell"
	query := u.Query()
	query.Set("session_id", session.id)
	query.Set("app_id", session.appID)
	u.RawQuery = query.Encode()
	log.Printf("AI APP %s: shell ready session=%s url=%s", appID, session.id, u.String())
	return u.String(), nil
}

func (s *Server) resolveAIAppShellLaunchModels(ctx context.Context, appID, requestedModel, requestedSource string) (string, []string, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel != "" {
		return s.resolveAIAppLaunchModels(ctx, requestedModel, requestedSource)
	}

	preferredModel := s.localInferenceModelID(s.preferredAIAppModel(appID))
	if preferredModel != "" {
		modelID, modelIDs, err := s.resolveAIAppLaunchModels(ctx, preferredModel, "")
		if err == nil {
			return modelID, modelIDs, nil
		}
		// Don't clear preference on lookup failure - the model might be from
		// a third-party provider whose API is temporarily unavailable.
		// The preference will be used when the provider API is available again.
		// Only report error if it's not a "not available" issue.
		if !strings.Contains(err.Error(), "is not available for AI Apps") {
			return "", nil, err
		}
		// Fall through to default model selection
	}

	return s.resolveAIAppLaunchModels(ctx, "", "")
}

func resolveAIAppOpenTarget(appID string) (aiAppOpenTarget, error) {
	switch appID {
	case "claude-code":
		return aiAppOpenTarget{
			AppID:       "claude-code",
			DisplayName: "Claude Code",
			Binaries:    []string{"claude"},
		}, nil
	case "open-code":
		return aiAppOpenTarget{
			AppID:       "open-code",
			DisplayName: "OpenCode",
			Binaries:    []string{"opencode"},
		}, nil
	case "codex":
		return aiAppOpenTarget{
			AppID:       "codex",
			DisplayName: "Codex",
			Binaries:    []string{"codex"},
		}, nil
	case "pi":
		return aiAppOpenTarget{
			AppID:       "pi",
			DisplayName: "Pi",
			Binaries:    []string{"pi"},
		}, nil
	default:
		return aiAppOpenTarget{}, fmt.Errorf("%s does not provide a web shell entry yet", appID)
	}
}

func (s *Server) resolveAIAppLaunchModels(ctx context.Context, requestedModel, requestedSource string) (string, []string, error) {
	availableModels, err := s.listAvailableModelsWithRefresh(ctx, true)
	if err != nil {
		return "", nil, fmt.Errorf("listing available models: %w", err)
	}
	availableModels = filterAIAppLaunchModels(availableModels)
	modelIDs, seen := s.modelIDsFromInfos(availableModels)
	defaultModel := ""
	if len(modelIDs) > 0 {
		defaultModel = modelIDs[0]
	}
	hasLocalModels := hasLocalAIAppModels(availableModels)

	if defaultModel == "" {
		if !hasLocalModels {
			if !s.hasCloudCredential() {
				return "", nil, fmt.Errorf("no local models were found. Pull a model first, or open csghub-lite Settings to sign in to OpenCSG or save an API Key")
			}
			return "", nil, fmt.Errorf("no local or OpenCSG models were found. Pull a model first, or check OpenCSG model access in csghub-lite Settings")
		}
		return "", nil, fmt.Errorf("no models were found. Pull a model first, then open the app")
	}
	if defaultInfo, ok := firstModelInfoByID(availableModels, defaultModel); ok && isCloudModelInfo(defaultInfo) && !s.hasCloudCredential() {
		return "", nil, fmt.Errorf("no local models were found. Pull a model first, or open csghub-lite Settings to sign in to OpenCSG or save an API Key")
	}

	requestedModel = strings.TrimSpace(requestedModel)
	requestedSource = strings.TrimSpace(requestedSource)
	if requestedModel != "" {
		requestedModel = s.localInferenceModelID(requestedModel)
		if requestedSource != "" {
			normalizedSource := strings.ToLower(requestedSource)
			if (normalizedSource == "local" || normalizedSource == "cloud" || providerIDFromSource(requestedSource) != "") &&
				modelInfoListContainsSource(availableModels, requestedModel, requestedSource) {
				if normalizedSource == "cloud" && !s.hasCloudCredential() {
					return "", nil, fmt.Errorf("model %q is not available for AI Apps. If you are trying to use an OpenCSG model, please open csghub-lite Settings to sign in to OpenCSG or save an API Key first", requestedModel)
				}
				return requestedModel, modelIDs, nil
			}
			if normalizedSource == "cloud" && refreshRequestedCloudModel(ctx, s, requestedModel, seen, &modelIDs) {
				if !s.hasCloudCredential() {
					return "", nil, fmt.Errorf("model %q is not available for AI Apps. If you are trying to use an OpenCSG model, please open csghub-lite Settings to sign in to OpenCSG or save an API Key first", requestedModel)
				}
				return requestedModel, modelIDs, nil
			}
			if normalizedSource == "cloud" && !s.hasCloudCredential() {
				return "", nil, fmt.Errorf("model %q is not available for AI Apps. If you are trying to use an OpenCSG model, please open csghub-lite Settings to sign in to OpenCSG or save an API Key first", requestedModel)
			}
			if normalizedSource == "local" || normalizedSource == "cloud" || providerIDFromSource(requestedSource) != "" {
				return "", nil, fmt.Errorf("model %q is not available for AI Apps", requestedModel)
			}
		}
		if _, ok := seen[requestedModel]; !ok {
			if refreshRequestedCloudModel(ctx, s, requestedModel, seen, &modelIDs) && !s.hasCloudCredential() {
				return "", nil, fmt.Errorf("model %q is not available for AI Apps. If you are trying to use an OpenCSG model, please open csghub-lite Settings to sign in to OpenCSG or save an API Key first", requestedModel)
			}
		}
		if _, ok := seen[requestedModel]; !ok {
			if !s.hasCloudCredential() {
				return "", nil, fmt.Errorf("model %q is not available for AI Apps. If you are trying to use an OpenCSG model, please open csghub-lite Settings to sign in to OpenCSG or save an API Key first", requestedModel)
			}
			return "", nil, fmt.Errorf("model %q is not available for AI Apps", requestedModel)
		}
		if requestedInfo, ok := firstModelInfoByID(availableModels, requestedModel); ok && isCloudModelInfo(requestedInfo) && !s.hasCloudCredential() {
			return "", nil, fmt.Errorf("model %q is not available for AI Apps. If you are trying to use an OpenCSG model, please open csghub-lite Settings to sign in to OpenCSG or save an API Key first", requestedModel)
		}
		return requestedModel, modelIDs, nil
	}

	return defaultModel, modelIDs, nil
}

func (s *Server) modelIDsFromInfos(models []api.ModelInfo) ([]string, map[string]struct{}) {
	modelIDs := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, item := range models {
		modelID := strings.TrimSpace(item.Model)
		modelIDs = appendUniqueModelID(modelIDs, seen, modelID)
		if isLocalModelInfo(item) {
			s.registerLocalModelAliases(seen, modelID)
		}
	}
	return modelIDs, seen
}

func filterAIAppLaunchModels(models []api.ModelInfo) []api.ModelInfo {
	out := make([]api.ModelInfo, 0, len(models))
	for _, item := range models {
		if isAIAppLaunchModel(item) {
			out = append(out, item)
		}
	}
	return out
}

func isAIAppLaunchModel(model api.ModelInfo) bool {
	if !isCloudModelInfo(model) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(model.Model)) {
	case "opus4.7":
		return false
	default:
		return true
	}
}

func hasLocalAIAppModels(models []api.ModelInfo) bool {
	for _, item := range models {
		if isLocalModelInfo(item) {
			return true
		}
	}
	return false
}

func modelInfoListContainsSource(models []api.ModelInfo, modelID, source string) bool {
	modelID = strings.TrimSpace(modelID)
	source = strings.TrimSpace(source)
	for _, item := range models {
		if strings.TrimSpace(item.Model) == modelID && strings.EqualFold(strings.TrimSpace(item.Source), source) {
			return true
		}
	}
	return false
}

func firstModelInfoByID(models []api.ModelInfo, modelID string) (api.ModelInfo, bool) {
	modelID = strings.TrimSpace(modelID)
	for _, item := range models {
		if strings.TrimSpace(item.Model) == modelID {
			return item, true
		}
	}
	return api.ModelInfo{}, false
}

func refreshRequestedCloudModel(ctx context.Context, s *Server, requestedModel string, seen map[string]struct{}, modelIDs *[]string) bool {
	if s == nil || s.cloud == nil {
		return false
	}
	cloudModels, err := s.cloud.RefreshChatModels(ctx)
	if err != nil {
		return false
	}
	cloudModels = filterAIAppLaunchModels(cloudModels)
	for _, item := range cloudModels {
		*modelIDs = appendUniqueModelID(*modelIDs, seen, item.Model)
	}
	return modelInfoListContains(cloudModels, requestedModel)
}

func appendUniqueModelID(modelIDs []string, seen map[string]struct{}, modelID string) []string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return modelIDs
	}
	if _, ok := seen[modelID]; ok {
		return modelIDs
	}
	seen[modelID] = struct{}{}
	return append(modelIDs, modelID)
}

func (s *Server) preferredAIAppModel(appID string) string {
	s.prefsMu.Lock()
	defer s.prefsMu.Unlock()
	if s.cfg == nil || s.cfg.AIAppPreferredModels == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.AIAppPreferredModels[strings.TrimSpace(appID)])
}

func (s *Server) savePreferredAIAppModel(appID, modelID string) {
	appID = strings.TrimSpace(appID)
	modelID = strings.TrimSpace(modelID)
	if appID == "" || modelID == "" || s.cfg == nil {
		return
	}

	s.prefsMu.Lock()
	defer s.prefsMu.Unlock()
	if s.cfg.AIAppPreferredModels == nil {
		s.cfg.AIAppPreferredModels = map[string]string{}
	}
	s.cfg.AIAppPreferredModels[appID] = s.localInferenceModelID(modelID)
	_ = config.Save(s.cfg)
}

func (s *Server) clearPreferredAIAppModel(appID string) {
	appID = strings.TrimSpace(appID)
	if appID == "" || s.cfg == nil {
		return
	}

	s.prefsMu.Lock()
	defer s.prefsMu.Unlock()
	if s.cfg.AIAppPreferredModels == nil {
		return
	}
	if _, ok := s.cfg.AIAppPreferredModels[appID]; !ok {
		return
	}
	delete(s.cfg.AIAppPreferredModels, appID)
	_ = config.Save(s.cfg)
}

func aiAppShellEnvOverrides(overrides map[string]string) map[string]string {
	// Web shell sessions run in a PTY even when the background server itself was
	// started without an interactive terminal, so advertise a capable terminal.
	merged := map[string]string{
		"TERM":         "xterm-256color",
		"COLORTERM":    "truecolor",
		"FORCE_COLOR":  "1",
		"CLICOLOR":     "1",
		"TERM_PROGRAM": "csghub-lite",
	}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

func (s *Server) prepareAIAppShellLaunch(target aiAppOpenTarget, modelID string, modelIDs []string, requestedWorkDir string) (aiAppPreparedLaunch, error) {
	binary, err := resolveAIAppLaunchBinary(target.Binaries)
	if err != nil {
		return aiAppPreparedLaunch{}, fmt.Errorf("%s is installed, but the launch command was not found on PATH", target.DisplayName)
	}

	workingDir, err := normalizeAIAppWorkDir(requestedWorkDir)
	if err != nil {
		return aiAppPreparedLaunch{}, err
	}
	serverURL := s.localBaseURL()

	switch target.AppID {
	case "claude-code":
		if err := claudeagent.SyncConfig(serverURL, "csghub-lite", modelID); err != nil {
			log.Printf("AI APP claude-code: syncing config failed: %v", err)
		}
		return aiAppPreparedLaunch{
			Binary: binary,
			Args: []string{
				"--model", modelID,
				"--settings", claudeLaunchSettingsJSON(serverURL),
			},
			Env: envWithOverridesAndUnset(aiAppShellEnvOverrides(map[string]string{
				"ANTHROPIC_BASE_URL":             serverURL,
				"ANTHROPIC_API_KEY":              "csghub-lite",
				"CLAUDE_API_BASE_URL":            serverURL,
				"CLAUDE_API_KEY":                 "csghub-lite",
				"CLAUDE_CODE_ATTRIBUTION_HEADER": "0",
			}), "NO_COLOR", "ANTHROPIC_AUTH_TOKEN"),
			Dir: workingDir,
		}, nil
	case "open-code":
		models := make([]api.ModelInfo, 0, len(modelIDs))
		for _, modelID := range modelIDs {
			models = append(models, api.ModelInfo{Model: modelID})
		}
		if err := opencodeagent.SyncConfig(serverURL, openClawProviderAPIKey(s.cfg.Token), modelID, models); err != nil {
			log.Printf("AI APP open-code: syncing config failed: %v", err)
		}
		return aiAppPreparedLaunch{
			Binary: binary,
			Env:    envWithOverridesAndUnset(aiAppShellEnvOverrides(nil), "NO_COLOR"),
			Dir:    workingDir,
		}, nil
	case "codex":
		models := make([]api.ModelInfo, 0, len(modelIDs))
		for _, modelID := range modelIDs {
			models = append(models, api.ModelInfo{Model: modelID})
		}
		if err := codexagent.SyncConfig(serverURL, openClawProviderAPIKey(s.cfg.Token), modelID, models); err != nil {
			log.Printf("AI APP codex: syncing config failed: %v", err)
		}
		return aiAppPreparedLaunch{
			Binary: binary,
			Args:   []string{"--no-alt-screen", "--model", modelID},
			Env:    envWithOverridesAndUnset(aiAppShellEnvOverrides(nil), "NO_COLOR"),
			Dir:    workingDir,
		}, nil
	case "pi":
		models := make([]api.ModelInfo, 0, len(modelIDs))
		for _, modelID := range modelIDs {
			models = append(models, api.ModelInfo{Model: modelID})
		}
		if err := piagent.SyncConfig(serverURL, openClawProviderAPIKey(config.Get().Token), modelID, models); err != nil {
			return aiAppPreparedLaunch{}, err
		}
		return aiAppPreparedLaunch{
			Binary: binary,
			Args:   []string{"--provider", piagent.ProviderID, "--model", modelID},
			Env:    envWithOverridesAndUnset(aiAppShellEnvOverrides(nil), "NO_COLOR"),
			Dir:    workingDir,
		}, nil
	default:
		return aiAppPreparedLaunch{}, fmt.Errorf("%s does not support web shell launch yet", target.DisplayName)
	}
}

func normalizeAIAppWorkDir(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		dir, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("determining working directory: %w", err)
		}
		return dir, nil
	}

	if requested == "~" || strings.HasPrefix(requested, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
		if requested == "~" {
			requested = home
		} else {
			requested = filepath.Join(home, strings.TrimPrefix(requested, "~"+string(filepath.Separator)))
		}
	}

	if !filepath.IsAbs(requested) {
		abs, err := filepath.Abs(requested)
		if err != nil {
			return "", fmt.Errorf("resolving project directory: %w", err)
		}
		requested = abs
	}

	info, err := os.Stat(requested)
	if err != nil {
		return "", fmt.Errorf("project directory %q is not accessible: %w", requested, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project directory %q is not a directory", requested)
	}
	return requested, nil
}

func claudeLaunchSettingsJSON(serverURL string) string {
	payload := map[string]interface{}{
		"env": map[string]string{
			"ANTHROPIC_BASE_URL":             serverURL,
			"ANTHROPIC_API_KEY":              "csghub-lite",
			"CLAUDE_API_BASE_URL":            serverURL,
			"CLAUDE_API_KEY":                 "csghub-lite",
			"CLAUDE_CODE_ATTRIBUTION_HEADER": "0",
		},
		"permissions": map[string]string{
			"defaultMode": "acceptEdits",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return `{"env":{}}`
	}
	return string(data)
}

func writeOpenCodeWebLaunchConfig(serverURL, defaultModel string, modelIDs []string) (string, error) {
	dir, err := aiAppLaunchDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating OpenCode web launch config dir: %w", err)
	}

	modelMap := make(map[string]interface{}, len(modelIDs))
	for _, modelID := range modelIDs {
		modelMap[modelID] = map[string]interface{}{
			"name": modelID,
		}
	}

	payload := map[string]interface{}{
		"$schema":           "https://opencode.ai/config.json",
		"enabled_providers": []string{openCodeWebProviderID},
		"provider": map[string]interface{}{
			openCodeWebProviderID: map[string]interface{}{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "OpenCSG",
				"options": map[string]interface{}{
					"baseURL": strings.TrimRight(serverURL, "/") + "/v1",
				},
				"models": modelMap,
			},
		},
		"model":       openCodeWebProviderID + "/" + defaultModel,
		"small_model": openCodeWebProviderID + "/" + defaultModel,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding OpenCode web launch config: %w", err)
	}

	path := filepath.Join(dir, "opencode-web.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing OpenCode web launch config: %w", err)
	}
	return path, nil
}

func (s *Server) writeCodexWebModelCatalog(modelIDs []string) (string, error) {
	dir, err := aiAppLaunchDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating Codex model catalog dir: %w", err)
	}

	catalog := codexModelCatalog{
		Models: s.codexModelCatalogEntries(modelIDs),
	}
	if len(catalog.Models) == 0 {
		return "", fmt.Errorf("building Codex model catalog: no models available")
	}

	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding Codex model catalog: %w", err)
	}

	path := filepath.Join(dir, "codex-web-models.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing Codex model catalog: %w", err)
	}
	return path, nil
}

func (s *Server) codexModelCatalogEntries(modelIDs []string) []codexModelCatalogEntry {
	entries := make([]codexModelCatalogEntry, 0, len(modelIDs))
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
		entries = append(entries, codexModelCatalogEntry{
			Slug:                       modelID,
			DisplayName:                modelID,
			Description:                "Model served by OpenCSG.",
			SupportedReasoningLevels:   []codexReasoningEffortPreset{},
			ShellType:                  "shell_command",
			Visibility:                 "list",
			SupportedInAPI:             true,
			Priority:                   len(entries),
			BaseInstructions:           codexBaseInstructions,
			SupportsReasoningSummaries: false,
			SupportVerbosity:           false,
			TruncationPolicy: codexTruncationPolicy{
				Mode:  "bytes",
				Limit: 10_000,
			},
			SupportsParallelToolCalls:  false,
			ExperimentalSupportedTools: []string{},
			InputModalities:            []string{"text"},
			ContextWindow:              s.codexContextWindowForModel(modelID),
		})
	}
	return entries
}

func (s *Server) codexContextWindowForModel(modelID string) int64 {
	modelID = strings.TrimSpace(modelID)
	if modelID != "" {
		if modelDir, err := s.manager.ModelPath(modelID); err == nil {
			return s.localModelContextWindow(modelID, modelDir)
		}
		if contextWindow, ok := codexagent.LookupContextWindow(modelID); ok {
			return contextWindow
		}
	}
	return codexagent.RemoteDefaultContextWindow(codexCloudContextWindow)
}

func (s *Server) codexShellConfigArgs(serverURL string, modelIDs []string) ([]string, error) {
	baseURL := strings.TrimRight(serverURL, "/") + "/v1"
	modelCatalogPath, err := s.writeCodexWebModelCatalog(modelIDs)
	if err != nil {
		return nil, err
	}
	return []string{
		"-c", fmt.Sprintf(`model_provider=%q`, codexWebProviderID),
		"-c", fmt.Sprintf(`model_providers.%s.name=%q`, codexWebProviderID, "OpenCSG"),
		"-c", fmt.Sprintf(`model_providers.%s.base_url=%q`, codexWebProviderID, baseURL),
		"-c", fmt.Sprintf(`model_providers.%s.supports_websockets=false`, codexWebProviderID),
		"-c", fmt.Sprintf(`model_catalog_json=%q`, modelCatalogPath),
	}, nil
}

func aiAppLaunchDir() (string, error) {
	appHome, err := config.AppHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(appHome, "apps", "launch"), nil
}

func resolveAIAppLaunchBinary(candidates []string) (string, error) {
	pathEnv := prependMissingPathEntries(os.Getenv("PATH"), commonAIAppBinDirs())
	_ = os.Setenv("PATH", pathEnv)

	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	for _, dir := range commonAIAppBinDirs() {
		for _, name := range candidates {
			if path, ok := lookupAIAppBinaryInDir(dir, name); ok {
				return path, nil
			}
		}
	}

	return "", fmt.Errorf("command not found")
}

func prependMissingPathEntries(current string, extras []string) string {
	items := strings.Split(current, string(os.PathListSeparator))
	seen := make(map[string]struct{}, len(items)+len(extras))
	filtered := make([]string, 0, len(items)+len(extras))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		filtered = append(filtered, item)
	}
	for _, item := range extras {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		filtered = append([]string{item}, filtered...)
		seen[item] = struct{}{}
	}
	return strings.Join(filtered, string(os.PathListSeparator))
}

func commonAIAppBinDirs() []string {
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
	return uniqueNonEmptyStrings(dirs)
}

func uniqueNonEmptyStrings(items []string) []string {
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

func lookupAIAppBinaryInDir(dir, name string) (string, bool) {
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

func newAIAppShellSessionID() string {
	buf := make([]byte, 12)
	if _, err := crand.Read(buf); err != nil {
		return fmt.Sprintf("shell-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (s *Server) handleAppShellWS(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	session, ok := s.appShells.Get(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "shell session not found")
		return
	}

	conn, err := aiAppShellUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(aiAppShellPongWait))
	conn.SetPongHandler(func(string) error {
		session.touchActivity()
		return conn.SetReadDeadline(time.Now().Add(aiAppShellPongWait))
	})

	attach := session.Attach()
	defer session.Detach(attach.events)

	if err := writeAIAppShellJSON(conn, attach.ready); err != nil {
		return
	}
	if len(attach.replay) > 0 {
		if err := writeAIAppShellBinary(conn, attach.replay); err != nil {
			return
		}
	}
	if attach.exitMsg != nil {
		_ = writeAIAppShellJSON(conn, attach.exitMsg)
		return
	}

	writerDone := make(chan struct{})
	pingTicker := time.NewTicker(aiAppShellPingInterval)
	go func() {
		defer pingTicker.Stop()
		defer close(writerDone)
		defer conn.Close()
		for {
			select {
			case event, ok := <-attach.events:
				if !ok {
					return
				}
				payload, exitMsg, closed := drainAIAppShellOutput(event, attach.events)
				if len(payload) > 0 {
					if err := writeAIAppShellBinary(conn, payload); err != nil {
						return
					}
				}
				if exitMsg != nil {
					_ = writeAIAppShellJSON(conn, exitMsg)
					return
				}
				if closed {
					return
				}
			case <-pingTicker.C:
				if err := writeAIAppShellPing(conn); err != nil {
					return
				}
			}
		}
	}()

	for {
		select {
		case <-writerDone:
			return
		default:
		}

		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(aiAppShellPongWait))

		switch messageType {
		case websocket.BinaryMessage:
			if err := session.WriteInput(payload); err != nil {
				return
			}
		case websocket.TextMessage:
			var message aiAppShellClientMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				continue
			}
			if message.Type == "resize" {
				_ = session.Resize(message.Cols, message.Rows)
			}
		}
	}
}

func writeAIAppShellJSON(conn *websocket.Conn, value interface{}) error {
	_ = conn.SetWriteDeadline(time.Now().Add(aiAppShellWriteTimeout))
	err := conn.WriteJSON(value)
	_ = conn.SetWriteDeadline(time.Time{})
	return err
}

func writeAIAppShellBinary(conn *websocket.Conn, payload []byte) error {
	_ = conn.SetWriteDeadline(time.Now().Add(aiAppShellWriteTimeout))
	err := conn.WriteMessage(websocket.BinaryMessage, payload)
	_ = conn.SetWriteDeadline(time.Time{})
	return err
}

func writeAIAppShellPing(conn *websocket.Conn) error {
	return conn.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(aiAppShellWriteTimeout))
}

func (s *Server) handleAppShellClose(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}
	if !s.appShells.Close(sessionID) {
		writeError(w, http.StatusNotFound, "shell session not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
