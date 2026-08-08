package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

type fakeEngine struct{}

func (f *fakeEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (f *fakeEngine) Chat(context.Context, []inference.Message, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (f *fakeEngine) Close() error { return nil }

func (f *fakeEngine) ModelName() string { return "fake" }

type scriptedChatEngine struct {
	chatResponses []scriptedChatResponse
	closeCalls    int
}

type scriptedChatResponse struct {
	text string
	err  error
}

func (e *scriptedChatEngine) Generate(context.Context, string, inference.Options, inference.TokenCallback) (string, error) {
	return "", nil
}

func (e *scriptedChatEngine) Chat(context.Context, []inference.Message, inference.Options, inference.TokenCallback) (string, error) {
	if len(e.chatResponses) == 0 {
		return "", nil
	}
	resp := e.chatResponses[0]
	e.chatResponses = e.chatResponses[1:]
	return resp.text, resp.err
}

func (e *scriptedChatEngine) Close() error {
	e.closeCalls++
	return nil
}

func (e *scriptedChatEngine) ModelName() string { return "scripted" }

func TestMain(m *testing.M) {
	_ = os.Setenv(config.DisableFileLoggingEnv, "1")
	if home, err := os.MkdirTemp("", "csghub-lite-server-test-home-*"); err == nil {
		_ = os.Setenv("HOME", home)
		_ = os.Setenv("USERPROFILE", home)
	}
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	config.ResetProviderPools()
	os.Exit(m.Run())
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetProviders()
	config.ResetProviderModelAllowlist()
	config.ResetProviderPools()
	t.Cleanup(config.ResetProviders)
	t.Cleanup(config.ResetProviderModelAllowlist)
	t.Cleanup(config.ResetProviderPools)
	dir := t.TempDir()
	cfg := &config.Config{
		ServerURL:  "https://hub.opencsg.com",
		ListenAddr: ":0",
		ModelDir:   filepath.Join(dir, "models"),
		DatasetDir: filepath.Join(dir, "datasets"),
	}
	s := New(cfg, "test")
	s.cloud = cloud.NewService("")
	return s
}

func TestValidateDesktopConfig(t *testing.T) {
	valid := &config.Config{
		DesktopMode:         true,
		ListenAddr:          "127.0.0.1:0",
		DesktopAPIAddr:      config.DefaultDesktopAPIAddr,
		DesktopAPIBindAddr:  config.DefaultDesktopAPIBindAddr,
		DesktopToken:        strings.Repeat("a", 64),
		DesktopSessionToken: strings.Repeat("b", 64),
		DesktopControlToken: strings.Repeat("c", 64),
		DesktopInstanceID:   strings.Repeat("d", 32),
	}
	if err := validateDesktopConfig(valid); err != nil {
		t.Fatalf("valid desktop config rejected: %v", err)
	}

	nonLoopback := *valid
	nonLoopback.ListenAddr = "0.0.0.0:0"
	if err := validateDesktopConfig(&nonLoopback); err == nil {
		t.Fatal("non-loopback desktop listen address accepted")
	}

	wrongAPIAddr := *valid
	wrongAPIAddr.DesktopAPIAddr = "127.0.0.1:11435"
	if err := validateDesktopConfig(&wrongAPIAddr); err == nil {
		t.Fatal("unexpected desktop API address accepted")
	}

	wrongAPIBindAddr := *valid
	wrongAPIBindAddr.DesktopAPIBindAddr = config.DefaultDesktopAPIAddr
	if err := validateDesktopConfig(&wrongAPIBindAddr); err == nil {
		t.Fatal("unexpected desktop API bind address accepted")
	}

	weakToken := *valid
	weakToken.DesktopSessionToken = "predictable"
	if err := validateDesktopConfig(&weakToken); err == nil {
		t.Fatal("weak desktop session token accepted")
	}
}

func TestDesktopRunFailsWhenExternalAPIPortIsOccupied(t *testing.T) {
	blocker, err := net.Listen("tcp", config.DefaultDesktopAPIBindAddr)
	if err != nil {
		t.Skipf("desktop API port is already unavailable: %v", err)
	}
	defer blocker.Close()

	base := newTestServer(t)
	cfg := base.cfg
	cfg.DesktopMode = true
	cfg.ListenAddrOverride = "127.0.0.1:0"
	cfg.DesktopAPIAddr = config.DefaultDesktopAPIAddr
	cfg.DesktopAPIBindAddr = config.DefaultDesktopAPIBindAddr
	cfg.DesktopToken = strings.Repeat("a", 64)
	cfg.DesktopSessionToken = strings.Repeat("b", 64)
	cfg.DesktopControlToken = strings.Repeat("c", 64)
	cfg.DesktopInstanceID = strings.Repeat("d", 32)
	s := New(cfg, "test")

	err = s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "desktop API port") {
		t.Fatalf("Run error = %v, want desktop API port conflict", err)
	}
}

func TestDesktopRunServesExternalAPIOnStablePort(t *testing.T) {
	probe, err := net.Listen("tcp", config.DefaultDesktopAPIBindAddr)
	if err != nil {
		t.Skipf("desktop API port is already unavailable: %v", err)
	}
	_ = probe.Close()

	base := newTestServer(t)
	cfg := base.cfg
	cfg.DesktopMode = true
	cfg.ListenAddrOverride = "127.0.0.1:0"
	cfg.DesktopAPIAddr = config.DefaultDesktopAPIAddr
	cfg.DesktopAPIBindAddr = config.DefaultDesktopAPIBindAddr
	cfg.DesktopToken = strings.Repeat("a", 64)
	cfg.DesktopSessionToken = strings.Repeat("b", 64)
	cfg.DesktopControlToken = strings.Repeat("c", 64)
	cfg.DesktopInstanceID = strings.Repeat("d", 32)
	s := New(cfg, "test")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("desktop server shutdown: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("desktop server did not shut down")
		}
	})

	client := &http.Client{Timeout: time.Second}
	var response *http.Response
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		response, err = client.Get("http://" + config.DefaultDesktopAPIAddr + "/api/health")
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET desktop API health: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", response.StatusCode)
	}
	boundHost, boundPort, err := net.SplitHostPort(cfg.DesktopAPIBoundAddr)
	if err != nil {
		t.Fatalf("desktop API bound address = %q: %v", cfg.DesktopAPIBoundAddr, err)
	}
	boundIP := net.ParseIP(boundHost)
	if boundIP == nil || !boundIP.IsUnspecified() || boundPort != "11436" {
		t.Fatalf("desktop API bound address = %q, want all interfaces on port 11436", cfg.DesktopAPIBoundAddr)
	}

	response, err = client.Get("http://" + config.DefaultDesktopAPIAddr + "/api/settings")
	if err != nil {
		t.Fatalf("GET desktop API management route: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("management route status = %d, want 404", response.StatusCode)
	}
}

func TestRunWithLocalInferenceSelfHealReloadsAndRetries(t *testing.T) {
	s := newTestServer(t)
	initial := &scriptedChatEngine{
		chatResponses: []scriptedChatResponse{{err: errors.New("dial tcp 127.0.0.1:1: connect: connection refused")}},
	}
	reloaded := &scriptedChatEngine{
		chatResponses: []scriptedChatResponse{{text: "healed"}},
	}
	s.engines["test/model"] = &managedEngine{
		engine:    initial,
		lastUsed:  time.Now(),
		keepAlive: DefaultKeepAlive,
	}

	got, err := runWithLocalInferenceSelfHeal(s, "local", "test/model", engineModeChat, initial,
		func(engine inference.Engine) (string, error) {
			return engine.Chat(context.Background(), nil, inference.DefaultOptions(), nil)
		},
		func() (inference.Engine, error) {
			return reloaded, nil
		},
	)
	if err != nil {
		t.Fatalf("runWithLocalInferenceSelfHeal() error = %v", err)
	}
	if got != "healed" {
		t.Fatalf("runWithLocalInferenceSelfHeal() = %q, want healed", got)
	}
	if initial.closeCalls != 1 {
		t.Fatalf("initial closeCalls = %d, want 1", initial.closeCalls)
	}
}

func TestRunWithLocalInferenceSelfHealEvictsWithoutRetryWhenCannotRetry(t *testing.T) {
	s := newTestServer(t)
	streamErr := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	initial := &scriptedChatEngine{
		chatResponses: []scriptedChatResponse{{err: streamErr}},
	}
	s.engines["test/model"] = &managedEngine{
		engine:    initial,
		lastUsed:  time.Now(),
		keepAlive: DefaultKeepAlive,
	}

	reloadCalls := 0
	wroteChunk := false
	_, err := runWithLocalInferenceSelfHealWhen(s, "local", "test/model", engineModeChat, initial,
		func(engine inference.Engine) (string, error) {
			wroteChunk = true
			return engine.Chat(context.Background(), nil, inference.DefaultOptions(), nil)
		},
		func() bool {
			return !wroteChunk
		},
		func() (inference.Engine, error) {
			reloadCalls++
			return &scriptedChatEngine{}, nil
		},
	)
	if !errors.Is(err, streamErr) {
		t.Fatalf("runWithLocalInferenceSelfHealWhen() error = %v, want %v", err, streamErr)
	}
	if reloadCalls != 0 {
		t.Fatalf("reloadCalls = %d, want 0", reloadCalls)
	}
	if initial.closeCalls != 1 {
		t.Fatalf("initial closeCalls = %d, want 1", initial.closeCalls)
	}
}

func TestRunWithLocalInferenceSelfHealFallsBackAfterSecondFailure(t *testing.T) {
	s := newTestServer(t)
	initial := &scriptedChatEngine{
		chatResponses: []scriptedChatResponse{{err: errors.New("dial tcp 127.0.0.1:1: connect: connection refused")}},
	}
	firstReload := &scriptedChatEngine{
		chatResponses: []scriptedChatResponse{{err: errors.New("read tcp 127.0.0.1:2->127.0.0.1:3: read: connection reset by peer")}},
	}
	secondReload := &scriptedChatEngine{
		chatResponses: []scriptedChatResponse{{text: "recovered-after-fallback"}},
	}
	survivor := &scriptedChatEngine{}
	s.engines["test/model"] = &managedEngine{
		engine:    initial,
		lastUsed:  time.Now(),
		keepAlive: DefaultKeepAlive,
	}
	s.engines["other/model"] = &managedEngine{
		engine:    survivor,
		lastUsed:  time.Now(),
		keepAlive: DefaultKeepAlive,
	}

	reloadCalls := 0
	got, err := runWithLocalInferenceSelfHeal(s, "local", "test/model", engineModeChat, initial,
		func(engine inference.Engine) (string, error) {
			return engine.Chat(context.Background(), nil, inference.DefaultOptions(), nil)
		},
		func() (inference.Engine, error) {
			reloadCalls++
			switch reloadCalls {
			case 1:
				s.engines["test/model"] = &managedEngine{
					engine:    firstReload,
					lastUsed:  time.Now(),
					keepAlive: DefaultKeepAlive,
				}
				return firstReload, nil
			case 2:
				s.engines["test/model"] = &managedEngine{
					engine:    secondReload,
					lastUsed:  time.Now(),
					keepAlive: DefaultKeepAlive,
				}
				return secondReload, nil
			default:
				return nil, errors.New("unexpected reload")
			}
		},
	)
	if err != nil {
		t.Fatalf("runWithLocalInferenceSelfHeal() error = %v", err)
	}
	if got != "recovered-after-fallback" {
		t.Fatalf("runWithLocalInferenceSelfHeal() = %q, want recovered-after-fallback", got)
	}
	if reloadCalls != 2 {
		t.Fatalf("reloadCalls = %d, want 2", reloadCalls)
	}
	if initial.closeCalls != 1 {
		t.Fatalf("initial closeCalls = %d, want 1", initial.closeCalls)
	}
	if firstReload.closeCalls != 1 {
		t.Fatalf("firstReload closeCalls = %d, want 1", firstReload.closeCalls)
	}
	if survivor.closeCalls != 1 {
		t.Fatalf("survivor closeCalls = %d, want 1", survivor.closeCalls)
	}
}

func TestRunWithLocalInferenceSelfHealBreakerStopsRestartStorm(t *testing.T) {
	s := newTestServer(t)
	reloadCalls := 0
	run := func(engine inference.Engine) (string, error) {
		return "", errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	}
	reload := func() (inference.Engine, error) {
		reloadCalls++
		return nil, errors.New("reload failed")
	}

	for i := 0; i < selfHealBreakerMaxHits-1; i++ {
		_, _ = runWithLocalInferenceSelfHeal(s, "local", "test/model", engineModeChat, &scriptedChatEngine{}, run, reload)
	}
	_, err := runWithLocalInferenceSelfHeal(s, "local", "test/model", engineModeChat, &scriptedChatEngine{}, run, reload)
	if err == nil {
		t.Fatal("expected self-heal breaker error")
	}
	if !strings.Contains(err.Error(), "automatic restart stopped") {
		t.Fatalf("error = %q, want breaker message", err.Error())
	}
	if reloadCalls != selfHealBreakerMaxHits-1 {
		t.Fatalf("reloadCalls = %d, want %d", reloadCalls, selfHealBreakerMaxHits-1)
	}
}

func TestHandleHealth(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if resp.APIProtocol != DesktopAPIProtocol || resp.PID == 0 {
		t.Errorf("incomplete health response: %#v", resp)
	}
}

func TestGetChatEngineReusesLoadedEngineWhenOverridesOmitted(t *testing.T) {
	s := newTestServer(t)
	engine := &fakeChatCompletionEngine{}
	s.engines["test/model"] = &managedEngine{
		engine:      engine,
		numCtx:      160000,
		numParallel: 4,
		lastUsed:    time.Now(),
		keepAlive:   DefaultKeepAlive,
	}

	got, err := s.getChatEngine(context.Background(), "test/model", "", 0, 0, -1, "", "", "")
	if err != nil {
		t.Fatalf("getChatEngine returned error: %v", err)
	}
	if got != engine {
		t.Fatalf("getChatEngine returned %#v, want existing engine %#v", got, engine)
	}
	if s.engines["test/model"].numCtx != 160000 {
		t.Fatalf("numCtx = %d, want 160000", s.engines["test/model"].numCtx)
	}
	if s.engines["test/model"].numParallel != 4 {
		t.Fatalf("numParallel = %d, want 4", s.engines["test/model"].numParallel)
	}
}

func TestLoadedEngineReloadsWhenRequestedDTypeGGUFMissing(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    model.FormatSafeTensors,
		Size:      123,
		Files:     []string{"model.safetensors"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.safetensors"), []byte("safetensors"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()

	newEngine := &fakeEngine{}
	loadEngineWithProgress = func(_ string, _ *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, _ int, _ int, _ int, _ string, _ string, dtype string) (inference.Engine, error) {
		if dtype != "q8_0" {
			t.Fatalf("dtype = %q, want q8_0", dtype)
		}
		return newEngine, nil
	}

	s.engines["test/model"] = &managedEngine{
		engine:      &fakeEngine{},
		numCtx:      inference.ResolveNumCtx(modelDir, 0),
		numParallel: inference.ResolveNumParallel(0),
		nGPULayers:  inference.ResolveNGPULayers(-1),
		lastUsed:    time.Now(),
		keepAlive:   api.KeepAliveForever,
	}

	got, err := s.getOrLoadEngineWithOpts("test/model", 0, 0, -1, "", "", "q8_0")
	if err != nil {
		t.Fatalf("getOrLoadEngineWithOpts returned error: %v", err)
	}
	if got != newEngine {
		t.Fatalf("engine = %#v, want reloaded engine %#v", got, newEngine)
	}
	if got := s.engines["test/model"].keepAlive; got != api.KeepAliveForever {
		t.Fatalf("keepAlive = %s, want forever", got)
	}
}

func TestLoadedEngineReusesRequestedDTypeWhenGGUFExists(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    model.FormatGGUF,
		Size:      123,
		Files:     []string{"model-q8_0.gguf"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model-q8_0.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		t.Fatal("loadEngineWithProgress should not be called")
		return nil, nil
	}

	engine := &fakeEngine{}
	s.engines["test/model"] = &managedEngine{
		engine:      engine,
		numCtx:      inference.ResolveNumCtx(modelDir, 0),
		numParallel: inference.ResolveNumParallel(0),
		nGPULayers:  inference.ResolveNGPULayers(-1),
		dtype:       "q8_0",
		lastUsed:    time.Now(),
		keepAlive:   api.KeepAliveForever,
	}

	got, err := s.getOrLoadEngineWithOpts("test/model", 0, 0, -1, "", "", "q8_0")
	if err != nil {
		t.Fatalf("getOrLoadEngineWithOpts returned error: %v", err)
	}
	if got != engine {
		t.Fatalf("engine = %#v, want existing engine %#v", got, engine)
	}
}

func TestReloadPreservesExistingKeepAlive(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "model",
		Format:    model.FormatGGUF,
		Size:      123,
		Files:     []string{"model.gguf"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()

	newEngine := &fakeEngine{}
	loadEngineWithProgress = func(_ string, _ *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, numCtx int, _ int, _ int, _ string, _ string, dtype string) (inference.Engine, error) {
		if numCtx != 131072 {
			t.Fatalf("numCtx = %d, want 131072", numCtx)
		}
		if dtype != "q8_0" {
			t.Fatalf("dtype = %q, want q8_0", dtype)
		}
		return newEngine, nil
	}

	s.engines["test/model"] = &managedEngine{
		engine:      &fakeEngine{},
		numCtx:      8192,
		numParallel: inference.ResolveNumParallel(0),
		nGPULayers:  inference.ResolveNGPULayers(-1),
		lastUsed:    time.Now().Add(-time.Hour),
		keepAlive:   api.KeepAliveForever,
	}

	got, err := s.getOrLoadEngineWithOpts("test/model", 131072, 0, -1, "", "", "q8_0")
	if err != nil {
		t.Fatalf("getOrLoadEngineWithOpts returned error: %v", err)
	}
	if got != newEngine {
		t.Fatalf("engine = %#v, want reloaded engine %#v", got, newEngine)
	}
	if got := s.engines["test/model"].keepAlive; got != api.KeepAliveForever {
		t.Fatalf("keepAlive = %s, want forever", got)
	}
}

func TestHandlePsRemainsResponsiveWhileModelLoads(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "slow",
		Format:    model.FormatSafeTensors,
		Size:      123,
		Files:     []string{"model.safetensors"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "slow")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()

	started := make(chan struct{})
	release := make(chan struct{})
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		close(started)
		<-release
		return &fakeEngine{}, nil
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := s.getOrLoadEngineWithOpts("test/slow", 0, 0, -1, "", "", "")
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for model load to start")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/ps", nil)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handlePs(w, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handlePs blocked while model load was in progress")
	}
	var psResp api.PsResponse
	if err := json.NewDecoder(w.Body).Decode(&psResp); err != nil {
		t.Fatalf("decode ps response: %v", err)
	}
	if len(psResp.Models) != 1 {
		t.Fatalf("running models = %d, want loading model; body=%s", len(psResp.Models), w.Body.String())
	}
	if got := psResp.Models[0].Status; got != "loading" {
		t.Fatalf("status = %q, want loading", got)
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("getOrLoadEngineWithOpts() error = %v", err)
	}
}

func TestConcurrentLoadsShareSingleInFlightLoad(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "shared",
		Format:    model.FormatSafeTensors,
		Size:      123,
		Files:     []string{"model.safetensors"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "shared")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	origLoader := loadEngineWithProgress
	defer func() { loadEngineWithProgress = origLoader }()

	var (
		calls   int
		engine  = &fakeEngine{}
		started = make(chan struct{})
		release = make(chan struct{})
	)
	loadEngineWithProgress = func(string, *model.LocalModel, inference.ConvertProgressFunc, bool, int, int, int, string, string, string) (inference.Engine, error) {
		calls++
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return engine, nil
	}

	type result struct {
		eng inference.Engine
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			eng, err := s.getOrLoadEngineWithOpts("test/shared", 0, 0, -1, "", "", "")
			results <- result{eng: eng, err: err}
		}()
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shared load to start")
	}
	close(release)

	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil {
			t.Fatalf("getOrLoadEngineWithOpts() error = %v", res.err)
		}
		if res.eng != engine {
			t.Fatalf("engine = %#v, want shared engine %#v", res.eng, engine)
		}
	}
	if calls != 1 {
		t.Fatalf("loadEngineWithProgress call count = %d, want 1", calls)
	}
}

func TestConcurrentLoadsWithDifferentSpeculativeConfigsReloadAfterWaiting(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace: "test",
		Name:      "speculative",
		Format:    model.FormatGGUF,
		Size:      123,
		Files:     []string{"model.gguf"},
	}
	if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	modelDir := filepath.Join(s.cfg.ModelDir, "test", "speculative")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	origLoader := loadEngineWithSpeculativeProgress
	defer func() { loadEngineWithSpeculativeProgress = origLoader }()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstEngine := &fakeEngine{}
	secondEngine := &fakeEngine{}
	calls := 0
	loadEngineWithSpeculativeProgress = func(_ string, _ *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, _ int, _ int, _ int, _ string, _ string, _ string, speculative inference.SpeculativeConfig) (inference.Engine, error) {
		calls++
		switch speculative.Key() {
		case "ngram-mod||0|0|":
			close(firstStarted)
			<-releaseFirst
			return firstEngine, nil
		case "ngram-simple||0|0|":
			return secondEngine, nil
		default:
			t.Fatalf("unexpected speculative config %q", speculative.Key())
			return nil, nil
		}
	}

	type result struct {
		eng inference.Engine
		err error
	}
	firstResult := make(chan result, 1)
	go func() {
		eng, err := s.getOrLoadEngineFullSpeculative(
			"test/speculative", nil, 0, 0, -1, "", "", "",
			inference.SpeculativeConfig{Types: []string{"ngram-mod"}},
		)
		firstResult <- result{eng: eng, err: err}
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first speculative load")
	}

	secondResult := make(chan result, 1)
	go func() {
		eng, err := s.getOrLoadEngineFullSpeculative(
			"test/speculative", nil, 0, 0, -1, "", "", "",
			inference.SpeculativeConfig{Types: []string{"ngram-simple"}},
		)
		secondResult <- result{eng: eng, err: err}
	}()

	close(releaseFirst)
	first := <-firstResult
	second := <-secondResult
	if first.err != nil {
		t.Fatalf("first load error = %v", first.err)
	}
	if second.err != nil {
		t.Fatalf("second load error = %v", second.err)
	}
	if first.eng != firstEngine {
		t.Fatalf("first engine = %#v, want %#v", first.eng, firstEngine)
	}
	if second.eng != secondEngine {
		t.Fatalf("second engine = %#v, want %#v", second.eng, secondEngine)
	}
	if calls != 2 {
		t.Fatalf("loader call count = %d, want 2", calls)
	}
	if got := s.engines["test/speculative"].speculativeKey; got != "ngram-simple||0|0|" {
		t.Fatalf("cached speculative key = %q, want ngram-simple", got)
	}
}

func TestROCMSingleEngineModeClosesOtherTextEnginesBeforeLoad(t *testing.T) {
	s := newTestServer(t)
	for _, name := range []string{"first", "second"} {
		lm := &model.LocalModel{
			Namespace: "test",
			Name:      name,
			Format:    model.FormatGGUF,
			Size:      123,
			Files:     []string{"model.gguf"},
		}
		if err := model.SaveManifest(s.cfg.ModelDir, lm); err != nil {
			t.Fatalf("SaveManifest(%s) error = %v", name, err)
		}
		modelDir := filepath.Join(s.cfg.ModelDir, "test", name)
		if err := os.MkdirAll(modelDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", name, err)
		}
	}

	origLoader := loadEngineWithProgress
	origEmbeddingLoader := loadEmbeddingEngineWithProgress
	origROCM := rocmSingleEngineMode
	defer func() {
		loadEngineWithProgress = origLoader
		loadEmbeddingEngineWithProgress = origEmbeddingLoader
		rocmSingleEngineMode = origROCM
	}()

	firstEngine := &scriptedChatEngine{}
	secondEngine := &scriptedChatEngine{}
	loadEngineWithProgress = func(_ string, lm *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, _ int, _ int, _ int, _ string, _ string, _ string) (inference.Engine, error) {
		switch lm.Name {
		case "first":
			return firstEngine, nil
		case "second":
			return secondEngine, nil
		default:
			t.Fatalf("unexpected model %s", lm.Name)
			return nil, nil
		}
	}
	loadEmbeddingEngineWithProgress = func(_ string, lm *model.LocalModel, _ inference.ConvertProgressFunc, _ bool, _ int, _ int, _ int, _ string, _ string, _ string) (inference.Engine, error) {
		if lm.Name != "second" {
			t.Fatalf("unexpected embedding model %s", lm.Name)
		}
		return secondEngine, nil
	}
	rocmSingleEngineMode = func() bool { return true }

	if _, err := s.getOrLoadEngineWithOpts("test/first", 0, 0, -1, "", "", ""); err != nil {
		t.Fatalf("load first error = %v", err)
	}
	if _, err := s.getOrLoadEmbeddingEngineWithOpts(context.Background(), "test/second", 0, -1, ""); err != nil {
		t.Fatalf("load second embedding error = %v", err)
	}

	if firstEngine.closeCalls != 1 {
		t.Fatalf("first closeCalls = %d, want 1", firstEngine.closeCalls)
	}
	if _, ok := s.engines[engineCacheKey("test/first", engineModeChat)]; ok {
		t.Fatal("first chat engine should have been evicted")
	}
	if got := s.engines[engineCacheKey("test/second", engineModeEmbed)].engine; got != secondEngine {
		t.Fatalf("second engine = %#v, want %#v", got, secondEngine)
	}
}

func TestHandleTags_Empty(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Errorf("models len = %d, want 0", len(resp.Models))
	}
}

func TestHandleTags_WithModels(t *testing.T) {
	s := newTestServer(t)

	// Create a model manifest
	lm := &model.LocalModel{
		Namespace:    "test",
		Name:         "model",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	model.SaveManifest(s.cfg.ModelDir, lm)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()

	s.handleTags(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.TagsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Models) != 1 {
		t.Fatalf("models len = %d, want 1", len(resp.Models))
	}
	if resp.Models[0].Name != "model" {
		t.Errorf("model name = %q, want %q", resp.Models[0].Name, "model")
	}
	if resp.Models[0].Provider != "local" {
		t.Errorf("provider = %q, want local", resp.Models[0].Provider)
	}
	if resp.Models[0].Category != "language_model" {
		t.Errorf("category = %q, want language_model", resp.Models[0].Category)
	}
}

func TestHandlePipelineTags(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pipeline-tags", nil)
	w := httptest.NewRecorder()

	s.handlePipelineTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.PipelineTagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.PipelineTags) != 6 {
		t.Fatalf("pipeline_tags len = %d, want 6", len(resp.PipelineTags))
	}
	if resp.PipelineTags[0].Category != "language_model" || resp.PipelineTags[0].Label != "语言模型" {
		t.Fatalf("first group = %#v, want language model group", resp.PipelineTags[0])
	}
	if got := strings.Join(resp.PipelineTags[0].Tags, ","); got != "text-generation,conversational,text2text-generation,fill-mask" {
		t.Fatalf("language model tags = %q", got)
	}
}

func TestHandleTagsProviderFilterLocal(t *testing.T) {
	s := newTestServer(t)
	lm := &model.LocalModel{
		Namespace:    "test",
		Name:         "model",
		Format:       model.FormatGGUF,
		Size:         1024,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	model.SaveManifest(s.cfg.ModelDir, lm)

	req := httptest.NewRequest(http.MethodGet, "/api/tags?provider=local", nil)
	w := httptest.NewRecorder()
	s.handleTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp api.TagsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Provider != "local" || resp.Models[0].Source != "local" {
		t.Fatalf("models = %#v, want one local provider model", resp.Models)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/tags?provider=xiaomi", nil)
	w = httptest.NewRecorder()
	s.handleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode filtered response: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Fatalf("models len = %d, want 0 for non-local provider", len(resp.Models))
	}
}

func TestHandleShow(t *testing.T) {
	s := newTestServer(t)

	// Create a model
	lm := &model.LocalModel{
		Namespace:    "ns",
		Name:         "mdl",
		Format:       model.FormatGGUF,
		Size:         2048,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Now(),
	}
	model.SaveManifest(s.cfg.ModelDir, lm)

	body := `{"model": "ns/mdl"}`
	req := httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleShow(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.ShowResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Details.Name != "mdl" {
		t.Errorf("details.name = %q, want %q", resp.Details.Name, "mdl")
	}
}

func TestHandleShow_NotFound(t *testing.T) {
	s := newTestServer(t)

	body := `{"model": "nonexistent/model"}`
	req := httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleShow(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDelete(t *testing.T) {
	s := newTestServer(t)

	// Create a model
	lm := &model.LocalModel{
		Namespace: "ns",
		Name:      "todelete",
		Format:    model.FormatGGUF,
		Size:      100,
		Files:     []string{"model.gguf"},
	}
	model.SaveManifest(s.cfg.ModelDir, lm)

	body := `{"model": "ns/todelete"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/delete", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleDelete(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleDelete_NotFound(t *testing.T) {
	s := newTestServer(t)

	body := `{"model": "nonexistent/model"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/delete", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGenerate_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()

	s.handleGenerate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleLoad_InvalidCacheType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","cache_type_k":"fp8"}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported cache type") {
		t.Fatalf("body = %q, want unsupported cache type", w.Body.String())
	}
}

func TestHandleLoad_InvalidDType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","dtype":"q9_x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported dtype") {
		t.Fatalf("body = %q, want unsupported dtype", w.Body.String())
	}
}

func TestHandleLoad_InvalidSpeculativeType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","speculative":{"types":["magic-draft"]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported speculative decoding type") {
		t.Fatalf("body = %q, want speculative type validation error", w.Body.String())
	}
}

func TestHandleLoad_InvalidKeepAlive(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","keep_alive":"later"}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "keep_alive") {
		t.Fatalf("body = %q, want keep_alive validation error", w.Body.String())
	}
}

func TestHandleLoad_ForeverKeepAliveOnExistingEngine(t *testing.T) {
	s := newTestServer(t)
	s.engines["test/model"] = &managedEngine{
		engine:    &fakeChatCompletionEngine{},
		lastUsed:  time.Now().Add(-time.Hour),
		keepAlive: DefaultKeepAlive,
	}

	body := `{"model":"test/model","keep_alive":"-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/load", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleLoad(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := s.engines["test/model"].keepAlive; got != api.KeepAliveForever {
		t.Fatalf("keepAlive = %s, want forever", got)
	}
	if !s.engines["test/model"].expiresAt().IsZero() {
		t.Fatalf("expiresAt = %v, want zero time for forever keep-alive", s.engines["test/model"].expiresAt())
	}
}

func TestEvictExpiredSkipsForeverKeepAlive(t *testing.T) {
	s := newTestServer(t)
	s.engines["test/model"] = &managedEngine{
		engine:    &fakeChatCompletionEngine{},
		lastUsed:  time.Now().Add(-24 * time.Hour),
		keepAlive: api.KeepAliveForever,
	}

	s.evictExpired(time.Now())

	if _, ok := s.engines["test/model"]; !ok {
		t.Fatal("expected forever keep-alive engine to remain loaded")
	}
}

func TestHandleChat_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleChat_CloudWithoutTokenReturnsUnauthorized(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"Qwen/Qwen3.5-35B-A3B-FP8:s-qwen-qwen3-5-35b-a3b-fp8-6dp9","source":"cloud","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp apiErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.ErrorCode != http.StatusUnauthorized {
		t.Fatalf("errorCode = %d, want %d", resp.ErrorCode, http.StatusUnauthorized)
	}
	if !strings.Contains(resp.Error, "Cloud login required") {
		t.Fatalf("error = %q, want Cloud login required", resp.Error)
	}
}

func TestHandleAnthropicMessages_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	s.handleAnthropicMessages(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleAnthropicCountTokens(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","system":"You are helpful","messages":[{"role":"user","content":"hello there"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleAnthropicCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp api.AnthropicCountTokensResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.InputTokens <= 0 {
		t.Fatalf("input_tokens = %d, want > 0", resp.InputTokens)
	}
}

func TestHandleOpenAIResponses_InvalidBody(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	s.handleOpenAIResponses(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGenerate_ModelNotFound(t *testing.T) {
	s := newTestServer(t)

	body := `{"model": "nonexistent/model", "prompt": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleGenerate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleGenerate_InvalidCacheType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","prompt":"hello","options":{"cache_type_v":"fp8"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleGenerate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported cache type") {
		t.Fatalf("body = %q, want unsupported cache type", w.Body.String())
	}
}

func TestHandleGenerate_InvalidDType(t *testing.T) {
	s := newTestServer(t)

	body := `{"model":"test/model","prompt":"hello","options":{"dtype":"q9_x"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleGenerate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported dtype") {
		t.Fatalf("body = %q, want unsupported dtype", w.Body.String())
	}
}

func TestRoutes(t *testing.T) {
	s := newTestServer(t)
	mux := s.routes()

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/"},
		{"GET", "/api/tags"},
		{"GET", "/api/pipeline-tags"},
		{"GET", "/api/tags/manage"},
		{"POST", "/api/tags/manage"},
		{"PUT", "/api/tags/manage"},
		{"DELETE", "/api/tags/manage"},
		{"GET", "/api/models/search"},
		{"GET", "/api/models/test/model/manifest"},
		{"GET", "/api/models/test/model/files/model.gguf"},
		{"GET", "/api/datasets"},
		{"GET", "/api/datasets/search"},
		{"GET", "/api/datasets/test/data/manifest"},
		{"GET", "/api/datasets/test/data/files/file.txt"},
		{"POST", "/api/show"},
		{"POST", "/api/pull"},
		{"DELETE", "/api/delete"},
		{"POST", "/api/generate"},
		{"POST", "/api/chat"},
		{"GET", "/api/settings"},
		{"POST", "/api/settings"},
		{"POST", "/api/settings/directories"},
		{"GET", "/v1/responses"},
		{"POST", "/v1/responses"},
		{"POST", "/v1/messages"},
		{"POST", "/v1/messages/count_tokens"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			// Just verify no panic and some response
			if w.Code == 0 {
				t.Error("got status 0")
			}
		})
	}
}
