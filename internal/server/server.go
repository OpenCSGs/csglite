package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/opencsgs/csglite/internal/apps"
	"github.com/opencsgs/csglite/internal/asr"
	"github.com/opencsgs/csglite/internal/chathistory"
	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/internal/embedding"
	"github.com/opencsgs/csglite/internal/imagegen"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
)

const (
	DefaultKeepAlive       = 5 * time.Minute
	DesktopAPIProtocol     = "1"
	evictorInterval        = 30 * time.Second
	engineModeChat         = "chat"
	engineModeEmbed        = "embedding"
	selfHealBreakerWindow  = 2 * time.Minute
	selfHealBreakerMaxHits = 3
)

type managedEngine struct {
	engine      inference.Engine
	numCtx      int
	numParallel int
	nGPULayers  int
	cacheTypeK  string
	cacheTypeV  string
	dtype       string
	lastUsed    time.Time
	keepAlive   time.Duration
}

// loadStepState records the latest load/conversion progress step for a model
// so that /api/ps can report what a "loading" model is actually doing
// (e.g. installing PyTorch, converting to GGUF) instead of a generic status.
type loadStepState struct {
	step    string
	current int
	total   int
}

func (s *Server) setLoadStep(modelID, step string, current, total int) {
	if modelID == "" || step == "" {
		return
	}
	s.loadStepMu.Lock()
	s.loadSteps[modelID] = loadStepState{step: step, current: current, total: total}
	s.loadStepMu.Unlock()
}

func (s *Server) clearLoadStep(modelID string) {
	s.loadStepMu.Lock()
	delete(s.loadSteps, modelID)
	s.loadStepMu.Unlock()
}

func (s *Server) loadStepFor(modelID string) (loadStepState, bool) {
	s.loadStepMu.Lock()
	state, ok := s.loadSteps[modelID]
	s.loadStepMu.Unlock()
	return state, ok
}

type engineLoadState struct {
	done   chan struct{}
	engine inference.Engine
	err    error
}

type selfHealBreakerState struct {
	first time.Time
	count int
}

type managedImageEngine struct {
	engine    imagegen.Engine
	lastUsed  time.Time
	keepAlive time.Duration
}

type imageEngineLoadState struct {
	done   chan struct{}
	engine imagegen.Engine
	err    error
}

type managedASREngine struct {
	engine    asr.Engine
	lastUsed  time.Time
	keepAlive time.Duration
}

type asrEngineLoadState struct {
	done   chan struct{}
	engine asr.Engine
	err    error
}

func (m *managedEngine) keepAliveForever() bool {
	return m.keepAlive < 0
}

func (m *managedEngine) expiresAt() time.Time {
	if m.keepAliveForever() {
		return time.Time{}
	}
	return m.lastUsed.Add(m.keepAlive)
}

func engineCacheKey(modelID, mode string) string {
	if mode == "" || mode == engineModeChat {
		return modelID
	}
	return modelID + "\x00" + mode
}

func engineModelIDFromKey(key string) string {
	if modelID, _, ok := strings.Cut(key, "\x00"); ok {
		return modelID
	}
	return key
}

type Server struct {
	cfg            *config.Config
	version        string
	manager        *model.Manager
	datasetManager *dataset.Manager
	appManager     *apps.Manager
	sourceSwitches *apps.SourceSwitchManager
	appShells      *aiAppShellManager
	cloud          *cloud.Service
	http           *http.Server
	logBuf         *LogBuffer

	mu           sync.RWMutex
	engines      map[string]*managedEngine
	loading      map[string]*engineLoadState
	selfHeal     map[string]selfHealBreakerState
	imageEngines map[string]*managedImageEngine
	imageLoading map[string]*imageEngineLoadState
	asrEngines   map[string]*managedASREngine
	asrLoading   map[string]*asrEngineLoadState
	imageJobs    *imageGenerationJobStore
	pullJobs     *pullJobStore
	loadStepMu   sync.Mutex
	loadSteps    map[string]loadStepState
	prefsMu      sync.Mutex
	openclawMu   sync.Mutex
	csgclawMu    sync.Mutex

	cloudRefreshMu   sync.Mutex
	cloudRefreshAt   time.Time
	cloudRefreshWait chan struct{}

	conversations       *chathistory.Store
	apiKeys             *config.APIKeyStore
	apiUsage            *config.APIUsageStore
	desktopBootstrapped atomic.Bool
}

type desktopReady struct {
	Event        string `json:"event"`
	URL          string `json:"url"`
	BootstrapURL string `json:"bootstrap_url"`
	ControlToken string `json:"control_token"`
	Version      string `json:"version"`
	APIProtocol  string `json:"api_protocol"`
	InstanceID   string `json:"instance_id"`
	PID          int    `json:"pid"`
}

func New(cfg *config.Config, version string) *Server {
	mgr := model.NewManager(cfg)
	dsMgr := dataset.NewManager(cfg)
	logBuf := NewLogBuffer(500)
	SetupLogging(logBuf)

	cloudSvc := cloud.NewService(resolveCloudURL(cfg))
	cloudSvc.SetAccessToken(cfg.Token)
	storageRoot := cfg.StorageDir()
	if storageRoot == "" {
		if defaultRoot, err := config.DefaultStorageDir(); err == nil {
			storageRoot = defaultRoot
		}
	}

	s := &Server{
		cfg:            cfg,
		version:        version,
		manager:        mgr,
		datasetManager: dsMgr,
		appManager:     apps.NewManager(cfg),
		sourceSwitches: apps.NewSourceSwitchManager(storageRoot),
		cloud:          cloudSvc,
		engines:        make(map[string]*managedEngine),
		loading:        make(map[string]*engineLoadState),
		selfHeal:       make(map[string]selfHealBreakerState),
		imageEngines:   make(map[string]*managedImageEngine),
		imageLoading:   make(map[string]*imageEngineLoadState),
		asrEngines:     make(map[string]*managedASREngine),
		asrLoading:     make(map[string]*asrEngineLoadState),
		imageJobs:      newImageGenerationJobStore(cfg.StorageDir()),
		pullJobs:       newPullJobStore(),
		loadSteps:      make(map[string]loadStepState),
		logBuf:         logBuf,
	}
	s.appShells = newAIAppShellManager()

	if appHome, err := config.AppHome(); err == nil {
		s.conversations = chathistory.NewStore(appHome)
		s.apiKeys = config.NewAPIKeyStore(appHome)
		s.apiUsage = config.NewAPIUsageStore(appHome)
	}

	handler := s.routes()
	s.http = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
		WriteTimeout:      0, // streaming responses and large uploads
		IdleTimeout:       120 * time.Second,
	}
	return s
}

func resolveCloudURL(cfg *config.Config) string {
	if u := strings.TrimSpace(cfg.AIGatewayURL); u != "" {
		return u
	}
	return cloud.DefaultBaseURL
}

func (s *Server) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if s.cfg.DesktopMode {
		if err := validateDesktopConfig(s.cfg); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("port %s is already in use; try a different port with --listen :PORT\n  %w", s.cfg.ListenAddr, err)
	}
	defer listener.Close()
	boundAddr := listener.Addr().String()
	s.cfg.BoundAddr = boundAddr

	go s.startEvictor(ctx)
	go s.refreshCloudModelsOnStartup(ctx)

	errCh := make(chan error, 1)
	if s.cfg.DesktopMode {
		baseURL := "http://" + boundAddr
		ready := desktopReady{
			Event:        "ready",
			URL:          baseURL,
			BootstrapURL: baseURL + "/?desktop_token=" + url.QueryEscape(s.cfg.DesktopToken),
			ControlToken: s.cfg.DesktopControlToken,
			Version:      s.version,
			APIProtocol:  DesktopAPIProtocol,
			InstanceID:   s.cfg.DesktopInstanceID,
			PID:          os.Getpid(),
		}
		payload, marshalErr := json.Marshal(ready)
		if marshalErr != nil {
			return fmt.Errorf("encoding desktop ready event: %w", marshalErr)
		}
		fmt.Printf("CSGLITE_DESKTOP_READY %s\n", payload)
	}
	go func() {
		addr := boundAddr
		if strings.HasPrefix(addr, ":") {
			addr = "localhost" + addr
		}
		log.Printf("csghub-lite server listening on %s", boundAddr)
		log.Printf("  Web UI: %s", "http://"+addr+"/")
		log.Printf("  Ollama API: %s", "http://"+addr+"/api/chat")
		log.Printf("  OpenAI API: %s", "http://"+addr+"/v1/chat/completions")
		log.Printf("  Anthropic API: %s", "http://"+addr+"/v1/messages")
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		s.shutdownRuntime()
		return err
	case <-ctx.Done():
		log.Println("shutting down server...")
		s.shutdownRuntime()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.http.Shutdown(shutCtx)
	}
}

func validateDesktopConfig(cfg *config.Config) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(cfg.ListenAddr))
	if err != nil || !isDesktopLoopbackHost(host) {
		return fmt.Errorf("desktop mode requires an explicit loopback listen address")
	}
	for name, value := range map[string]string{
		"bootstrap token": cfg.DesktopToken,
		"session token":   cfg.DesktopSessionToken,
		"control token":   cfg.DesktopControlToken,
	} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return fmt.Errorf("desktop mode requires a random 32-byte %s", name)
		}
	}
	instanceID, err := hex.DecodeString(cfg.DesktopInstanceID)
	if err != nil || len(instanceID) != 16 {
		return fmt.Errorf("desktop mode requires a random 16-byte instance ID")
	}
	return nil
}

func (s *Server) shutdownRuntime() {
	if s.appShells != nil {
		s.appShells.CloseAll()
	}
	s.closeAllEngines()
}

// startEvictor periodically closes engines that have exceeded their keep-alive.
func (s *Server) startEvictor(ctx context.Context) {
	ticker := time.NewTicker(evictorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.evictExpired(now)
		}
	}
}

func (s *Server) evictExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, me := range s.engines {
		if me.keepAliveForever() {
			continue
		}
		if now.After(me.expiresAt()) {
			log.Printf("evicting idle model %s (unused for %s)", id, me.keepAlive)
			me.engine.Close()
			delete(s.engines, id)
		}
	}
	for id, me := range s.imageEngines {
		if me.keepAlive < 0 {
			continue
		}
		if now.After(me.lastUsed.Add(me.keepAlive)) {
			log.Printf("evicting idle image model %s (unused for %s)", id, me.keepAlive)
			me.engine.Close()
			delete(s.imageEngines, id)
		}
	}
	for id, me := range s.asrEngines {
		if me.keepAlive < 0 {
			continue
		}
		if now.After(me.lastUsed.Add(me.keepAlive)) {
			log.Printf("evicting idle ASR model %s (unused for %s)", id, me.keepAlive)
			me.engine.Close()
			delete(s.asrEngines, id)
		}
	}
}

// touchEngine updates lastUsed for the given model. Must be called after
// every inference request so the evictor knows the engine is still active.
func (s *Server) touchEngine(modelID string) {
	s.touchEngineKey(engineCacheKey(s.resolveLocalModelStorageID(modelID), engineModeChat))
}

func (s *Server) touchEngineKey(key string) {
	s.mu.Lock()
	if me, ok := s.engines[key]; ok {
		me.lastUsed = time.Now()
	}
	s.mu.Unlock()
}

// closeEngineKey removes a text inference engine from the cache and closes it.
// The Close call happens outside the cache lock.
func (s *Server) closeEngineKey(key string) {
	s.mu.Lock()
	me, ok := s.engines[key]
	if ok {
		delete(s.engines, key)
	}
	s.mu.Unlock()
	if ok && me != nil && me.engine != nil {
		_ = me.engine.Close()
	}
}

// closeAllInferenceEngines closes and clears only text inference engines.
// It does not affect image or ASR engines.
func (s *Server) closeAllInferenceEngines() {
	s.mu.Lock()
	engines := s.engines
	s.engines = make(map[string]*managedEngine)
	s.mu.Unlock()
	for _, me := range engines {
		if me != nil && me.engine != nil {
			_ = me.engine.Close()
		}
	}
}

func (s *Server) closeOtherInferenceEnginesLocked(keepKey string) []inference.Engine {
	closed := make([]inference.Engine, 0, len(s.engines))
	for key, me := range s.engines {
		if key == keepKey {
			continue
		}
		if me != nil && me.engine != nil {
			log.Printf("ROCm single-engine mode: closing text engine %s before loading %s", key, keepKey)
			closed = append(closed, me.engine)
		}
		delete(s.engines, key)
	}
	return closed
}

func closeInferenceEngines(engines []inference.Engine) {
	for _, eng := range engines {
		if eng != nil {
			_ = eng.Close()
		}
	}
}

func (s *Server) otherInferenceLoadLocked(cacheKey string) (string, chan struct{}) {
	for key, state := range s.loading {
		if key == cacheKey || state == nil {
			continue
		}
		return key, state.done
	}
	return "", nil
}

func (s *Server) resetSelfHealBreaker(cacheKey string) {
	s.mu.Lock()
	delete(s.selfHeal, cacheKey)
	s.mu.Unlock()
}

func (s *Server) recordSelfHealFailure(cacheKey string, now time.Time) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selfHeal == nil {
		s.selfHeal = make(map[string]selfHealBreakerState)
	}
	state := s.selfHeal[cacheKey]
	if state.first.IsZero() || now.Sub(state.first) > selfHealBreakerWindow {
		state = selfHealBreakerState{first: now, count: 1}
	} else {
		state.count++
	}
	s.selfHeal[cacheKey] = state
	return state.count, state.count >= selfHealBreakerMaxHits
}

func (s *Server) setEngineKeepAlive(modelID string, keepAlive time.Duration) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	for _, key := range []string{engineCacheKey(modelID, engineModeChat), engineCacheKey(modelID, engineModeEmbed)} {
		if me, ok := s.engines[key]; ok {
			me.keepAlive = keepAlive
		}
	}
	s.mu.Unlock()
}

func (s *Server) setImageEngineKeepAlive(modelID string, keepAlive time.Duration) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	if me, ok := s.imageEngines[modelID]; ok {
		me.keepAlive = keepAlive
	}
	s.mu.Unlock()
}

func (s *Server) setASREngineKeepAlive(modelID string, keepAlive time.Duration) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	if me, ok := s.asrEngines[modelID]; ok {
		me.keepAlive = keepAlive
	}
	s.mu.Unlock()
}

func (s *Server) touchImageEngine(modelID string) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	if me, ok := s.imageEngines[modelID]; ok {
		me.lastUsed = time.Now()
	}
	s.mu.Unlock()
}

func (s *Server) touchASREngine(modelID string) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	if me, ok := s.asrEngines[modelID]; ok {
		me.lastUsed = time.Now()
	}
	s.mu.Unlock()
}

func (s *Server) getOrLoadEngine(modelID string) (inference.Engine, error) {
	return s.getOrLoadEngineFull(modelID, nil, 0, 0, -1, "", "", "")
}

func (s *Server) getOrLoadEngineWithProgress(modelID string, progress inference.ConvertProgressFunc) (inference.Engine, error) {
	return s.getOrLoadEngineFull(modelID, progress, 0, 0, -1, "", "", "")
}

func (s *Server) getOrLoadEngineWithNumCtx(modelID string, numCtx int) (inference.Engine, error) {
	return s.getOrLoadEngineFull(modelID, nil, numCtx, 0, -1, "", "", "")
}

func (s *Server) getOrLoadEngineWithOpts(modelID string, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (inference.Engine, error) {
	return s.getOrLoadEngineFull(modelID, nil, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
}

func (s *Server) getOrLoadEngineWithProgressAndOpts(modelID string, progress inference.ConvertProgressFunc, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (inference.Engine, error) {
	return s.getOrLoadEngineFull(modelID, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
}

func runtimeOverridesRequested(numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV string) bool {
	return numCtx > 0 || numParallel > 0 || nGPULayers >= 0 || cacheTypeK != "" || cacheTypeV != ""
}

func loadedDTypeMatchesRequest(loaded, requested string) bool {
	if requested == "" {
		return true
	}
	if loaded == requested {
		return true
	}
	return loaded == "" && requested == "f16"
}

var loadEngineWithProgress = inference.LoadEngineWithProgress
var loadEmbeddingEngineWithProgress = inference.LoadEmbeddingEngineWithProgress
var rocmSingleEngineMode = inference.ROCMSingleEngineMode
var newPythonEmbeddingEngine = func(ctx context.Context, modelName, modelDir string, runtimeManager *imagegen.RuntimeManager) (inference.Engine, error) {
	return embedding.NewPythonEngine(ctx, modelName, modelDir, runtimeManager)
}
var ensureEmbeddingRuntimeReady = func(ctx context.Context, runtimeManager *imagegen.RuntimeManager, progress imagegen.ProgressFunc, upgradePackages bool) error {
	if status := runtimeManager.EmbeddingStatus(ctx); status.Ready && !upgradePackages {
		return nil
	}
	status, err := runtimeManager.InstallEmbeddingWithProgressOptions(ctx, progress, upgradePackages)
	if err != nil {
		return err
	}
	// Never hand a broken runtime to the worker: the post-install status runs
	// the real import verification on Windows (issue #54).
	if !status.Ready {
		if status.Error != "" {
			return errors.New(status.Error)
		}
		return errors.New("embedding runtime is not ready after install")
	}
	return nil
}
var newDiffusersEngine = func(ctx context.Context, modelName, modelDir string, runtimeManager *imagegen.RuntimeManager) (imagegen.Engine, error) {
	return imagegen.NewDiffusersEngine(ctx, modelName, modelDir, runtimeManager)
}
var ensureImageRuntimeReady = func(ctx context.Context, runtimeManager *imagegen.RuntimeManager, progress imagegen.ProgressFunc, upgradePackages bool) error {
	if status := runtimeManager.Status(ctx); status.Ready && !upgradePackages {
		return nil
	}
	_, err := runtimeManager.InstallWithProgressOptions(ctx, progress, upgradePackages)
	return err
}

func (s *Server) getOrLoadEngineFull(modelID string, progress inference.ConvertProgressFunc, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (inference.Engine, error) {
	return s.getOrLoadEngineFullMode(modelID, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, engineModeChat)
}

func (s *Server) getOrLoadEmbeddingEngineWithOpts(ctx context.Context, modelID string, numCtx, nGPULayers int, dtype string) (inference.Engine, error) {
	return s.getOrLoadEmbeddingEngineWithProgress(ctx, modelID, nil, numCtx, nGPULayers, dtype)
}

func (s *Server) getOrLoadEmbeddingEngineWithProgress(ctx context.Context, modelID string, progress inference.ConvertProgressFunc, numCtx, nGPULayers int, dtype string) (inference.Engine, error) {
	if s.shouldUsePythonEmbeddingRuntime(modelID) {
		return s.getOrLoadPythonEmbeddingEngine(ctx, modelID)
	}
	return s.getOrLoadEngineFullMode(modelID, progress, numCtx, 0, nGPULayers, "", "", dtype, engineModeEmbed)
}

func (s *Server) shouldUsePythonEmbeddingRuntime(modelID string) bool {
	modelID = s.resolveLocalModelStorageID(modelID)
	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		return false
	}
	lm, err := s.manager.Get(modelID)
	if err != nil || lm == nil {
		return false
	}
	pipelineTag := s.resolvedLocalPipelineTag(modelID, strings.TrimSpace(lm.PipelineTag))
	if !isEmbeddingPipelineTag(pipelineTag) {
		return false
	}
	if lm.Format == model.FormatGGUF {
		return false
	}
	if !convert.HasConvertibleHFWeights(modelDir) {
		return false
	}
	arch := readLocalModelArchitecture(modelDir)
	if arch == "" {
		return false
	}
	return model.IsPythonEmbeddingArchitecture(arch) && !convert.IsSupportedHFArchitecture(arch)
}

func readLocalModelArchitecture(modelDir string) string {
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Architectures []string `json:"architectures"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	for _, arch := range cfg.Architectures {
		if arch = strings.TrimSpace(arch); arch != "" {
			return arch
		}
	}
	return ""
}

func (s *Server) getOrLoadPythonEmbeddingEngine(ctx context.Context, modelID string) (inference.Engine, error) {
	modelID = s.resolveLocalModelStorageID(modelID)
	cacheKey := engineCacheKey(modelID, engineModeEmbed)

	s.mu.RLock()
	me, ok := s.engines[cacheKey]
	s.mu.RUnlock()
	if ok {
		return me.engine, nil
	}

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found locally; use 'csghub-lite pull %s' first", modelID, modelID)
	}

	for {
		s.mu.Lock()
		if me, ok := s.engines[cacheKey]; ok {
			eng := me.engine
			s.mu.Unlock()
			return eng, nil
		}
		if state, ok := s.loading[cacheKey]; ok {
			log.Printf("MODEL %s: waiting for in-flight python embedding load", modelID)
			s.mu.Unlock()
			<-state.done
			if state.err != nil {
				return nil, state.err
			}
			if state.engine != nil {
				return state.engine, nil
			}
			continue
		}
		state := &engineLoadState{done: make(chan struct{})}
		s.loading[cacheKey] = state
		s.mu.Unlock()

		log.Printf("MODEL %s: python embedding engine load started", modelID)
		runtimeManager, err := imagegen.NewEmbeddingRuntimeManager()
		if err == nil {
			err = ensureEmbeddingRuntimeReady(ctx, runtimeManager, nil, false)
			if err == nil {
				state.engine, err = newPythonEmbeddingEngine(ctx, modelID, modelDir, runtimeManager)
			}
		}
		state.err = err

		s.mu.Lock()
		delete(s.loading, cacheKey)
		if state.err == nil {
			s.engines[cacheKey] = &managedEngine{
				engine:    state.engine,
				lastUsed:  time.Now(),
				keepAlive: DefaultKeepAlive,
			}
		}
		close(state.done)
		s.mu.Unlock()

		if state.err != nil {
			log.Printf("MODEL %s: python embedding engine load failed: %v", modelID, state.err)
			return nil, state.err
		}
		log.Printf("MODEL %s: python embedding engine load complete", modelID)
		return state.engine, nil
	}
}

func (s *Server) getOrLoadEngineFullMode(modelID string, progress inference.ConvertProgressFunc, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype, mode string) (inference.Engine, error) {
	modelID = s.resolveLocalModelStorageID(modelID)
	normalizedCacheTypeK, err := inference.NormalizeCacheType(cacheTypeK)
	if err != nil {
		return nil, err
	}
	normalizedCacheTypeV, err := inference.NormalizeCacheType(cacheTypeV)
	if err != nil {
		return nil, err
	}
	normalizedNGPULayers, err := inference.NormalizeNGPULayers(nGPULayers)
	if err != nil {
		return nil, err
	}
	normalizedDType, err := convert.NormalizeRuntimeDType(dtype)
	if err != nil {
		return nil, err
	}
	requestedOverrides := runtimeOverridesRequested(numCtx, numParallel, normalizedNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV)
	cacheKey := engineCacheKey(modelID, mode)

	s.mu.RLock()
	me, ok := s.engines[cacheKey]
	s.mu.RUnlock()
	if ok && !requestedOverrides && normalizedDType == "" {
		log.Printf("MODEL %s: using already loaded %s engine", modelID, mode)
		return me.engine, nil
	}

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found locally; use 'csghub-lite pull %s' first", modelID, modelID)
	}
	effectiveNumCtx := inference.ResolveNumCtx(modelDir, numCtx)
	effectiveNumParallel := inference.ResolveNumParallel(numParallel)
	effectiveNGPULayers := inference.ResolveNGPULayers(normalizedNGPULayers)
	needsRequestedDTypeConversion := false
	if normalizedDType != "" {
		if needs, err := convert.NeedsConversionForDType(modelDir, normalizedDType); err != nil {
			return nil, err
		} else {
			needsRequestedDTypeConversion = needs
		}
	}

	for {
		s.mu.Lock()

		if me, ok := s.engines[cacheKey]; ok {
			if !requestedOverrides && normalizedDType == "" {
				eng := me.engine
				s.mu.Unlock()
				return eng, nil
			}
			dtypeReady := normalizedDType == "" || (loadedDTypeMatchesRequest(me.dtype, normalizedDType) && !needsRequestedDTypeConversion)
			if me.numCtx == effectiveNumCtx && me.numParallel == effectiveNumParallel && me.nGPULayers == effectiveNGPULayers && me.cacheTypeK == normalizedCacheTypeK && me.cacheTypeV == normalizedCacheTypeV && dtypeReady {
				eng := me.engine
				s.mu.Unlock()
				return eng, nil
			}
		}

		if state, ok := s.loading[cacheKey]; ok {
			log.Printf("MODEL %s: waiting for in-flight %s load", modelID, mode)
			s.mu.Unlock()
			<-state.done
			if state.err != nil {
				return nil, state.err
			}
			if state.engine != nil {
				return state.engine, nil
			}
			continue
		}

		if rocmSingleEngineMode() {
			if loadingKey, done := s.otherInferenceLoadLocked(cacheKey); done != nil {
				log.Printf("ROCm single-engine mode: waiting for in-flight text engine load %s before loading %s", loadingKey, cacheKey)
				s.mu.Unlock()
				<-done
				continue
			}
		}

		state := &engineLoadState{done: make(chan struct{})}
		s.loading[cacheKey] = state
		log.Printf("MODEL %s: %s engine load started num_ctx=%d num_parallel=%d n_gpu_layers=%d cache_type_k=%q cache_type_v=%q dtype=%q", modelID, mode, effectiveNumCtx, effectiveNumParallel, effectiveNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV, normalizedDType)

		var oldEngine inference.Engine
		var closedEngines []inference.Engine
		nextKeepAlive := DefaultKeepAlive
		if rocmSingleEngineMode() {
			closedEngines = s.closeOtherInferenceEnginesLocked(cacheKey)
		}
		if me, ok := s.engines[cacheKey]; ok {
			log.Printf("reloading model %s %s engine due to config change (num_ctx %d->%d, parallel %d->%d, n_gpu_layers %d->%d, cache_type_k %q->%q, cache_type_v %q->%q, dtype %q->%q)", modelID, mode, me.numCtx, effectiveNumCtx, me.numParallel, effectiveNumParallel, me.nGPULayers, effectiveNGPULayers, me.cacheTypeK, normalizedCacheTypeK, me.cacheTypeV, normalizedCacheTypeV, me.dtype, normalizedDType)
			oldEngine = me.engine
			nextKeepAlive = me.keepAlive
			delete(s.engines, cacheKey)
		}
		s.mu.Unlock()

		closeInferenceEngines(closedEngines)
		if oldEngine != nil {
			oldEngine.Close()
		}

		lm, err := s.manager.Get(modelID)
		if err == nil {
			loader := loadEngineWithProgress
			if mode == engineModeEmbed {
				loader = loadEmbeddingEngineWithProgress
			}
			state.engine, err = loader(modelDir, lm, progress, false, effectiveNumCtx, effectiveNumParallel, effectiveNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV, normalizedDType)
		}
		state.err = err

		s.mu.Lock()
		delete(s.loading, cacheKey)
		if state.err == nil {
			s.engines[cacheKey] = &managedEngine{
				engine:      state.engine,
				numCtx:      effectiveNumCtx,
				numParallel: effectiveNumParallel,
				nGPULayers:  effectiveNGPULayers,
				cacheTypeK:  normalizedCacheTypeK,
				cacheTypeV:  normalizedCacheTypeV,
				dtype:       normalizedDType,
				lastUsed:    time.Now(),
				keepAlive:   nextKeepAlive,
			}
		}
		close(state.done)
		s.mu.Unlock()

		if state.err != nil {
			log.Printf("MODEL %s: %s engine load failed: %v", modelID, mode, state.err)
			return nil, state.err
		}
		log.Printf("MODEL %s: %s engine load complete", modelID, mode)
		return state.engine, nil
	}
}

func (s *Server) getOrLoadImageEngine(ctx context.Context, modelID string) (imagegen.Engine, error) {
	return s.getOrLoadImageEngineWithProgress(ctx, modelID, nil, false)
}

func (s *Server) getOrLoadImageEngineWithProgress(ctx context.Context, modelID string, progress imagegen.ProgressFunc, upgradePackages bool) (imagegen.Engine, error) {
	modelID = s.resolveLocalModelStorageID(modelID)
	if upgradePackages {
		s.mu.Lock()
		if me, ok := s.imageEngines[modelID]; ok {
			_ = me.engine.Close()
			delete(s.imageEngines, modelID)
		}
		s.mu.Unlock()
	}

	s.mu.RLock()
	me, ok := s.imageEngines[modelID]
	s.mu.RUnlock()
	if ok {
		return me.engine, nil
	}

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found locally; use 'csghub-lite pull %s' first", modelID, modelID)
	}
	lm, err := s.manager.Get(modelID)
	if err != nil {
		return nil, err
	}
	pipelineTag := s.resolvedLocalPipelineTag(modelID, strings.TrimSpace(lm.PipelineTag))
	if !isImageGenerationPipelineTag(pipelineTag) {
		return nil, fmt.Errorf("model %q is not a text-to-image model", modelID)
	}

	for {
		s.mu.Lock()
		if me, ok := s.imageEngines[modelID]; ok {
			eng := me.engine
			s.mu.Unlock()
			return eng, nil
		}
		if state, ok := s.imageLoading[modelID]; ok {
			s.mu.Unlock()
			<-state.done
			if state.err != nil {
				return nil, state.err
			}
			if state.engine != nil {
				return state.engine, nil
			}
			continue
		}
		state := &imageEngineLoadState{done: make(chan struct{})}
		s.imageLoading[modelID] = state
		s.mu.Unlock()

		log.Printf("MODEL %s: image engine load started", modelID)
		runtimeManager, err := imagegen.NewRuntimeManager()
		if err == nil {
			err = ensureImageRuntimeReady(ctx, runtimeManager, progress, upgradePackages)
			if err == nil {
				state.engine, err = newDiffusersEngine(ctx, modelID, modelDir, runtimeManager)
			}
		}
		state.err = err

		s.mu.Lock()
		delete(s.imageLoading, modelID)
		if state.err == nil {
			s.imageEngines[modelID] = &managedImageEngine{
				engine:    state.engine,
				lastUsed:  time.Now(),
				keepAlive: DefaultKeepAlive,
			}
		}
		close(state.done)
		s.mu.Unlock()

		if state.err != nil {
			log.Printf("MODEL %s: image engine load failed: %v", modelID, state.err)
			return nil, state.err
		}
		log.Printf("MODEL %s: image engine load complete", modelID)
		return state.engine, nil
	}
}

var newASREngine = func(ctx context.Context, modelName, modelDir string, runtimeManager *imagegen.RuntimeManager) (asr.Engine, error) {
	return asr.NewPythonEngine(ctx, modelName, modelDir, runtimeManager)
}

func (s *Server) getOrLoadASREngine(ctx context.Context, modelID string) (asr.Engine, error) {
	modelID = s.resolveLocalModelStorageID(modelID)

	s.mu.RLock()
	me, ok := s.asrEngines[modelID]
	s.mu.RUnlock()
	if ok {
		return me.engine, nil
	}

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found locally; use 'csghub-lite pull %s' first", modelID, modelID)
	}
	lm, err := s.manager.Get(modelID)
	if err != nil {
		return nil, err
	}
	pipelineTag := s.resolvedLocalPipelineTag(modelID, strings.TrimSpace(lm.PipelineTag))
	if !isASRPipelineTag(pipelineTag) {
		return nil, fmt.Errorf("model %q is not an automatic speech recognition model", modelID)
	}

	for {
		s.mu.Lock()
		if me, ok := s.asrEngines[modelID]; ok {
			eng := me.engine
			s.mu.Unlock()
			return eng, nil
		}
		if state, ok := s.asrLoading[modelID]; ok {
			s.mu.Unlock()
			<-state.done
			if state.err != nil {
				return nil, state.err
			}
			if state.engine != nil {
				return state.engine, nil
			}
			continue
		}
		state := &asrEngineLoadState{done: make(chan struct{})}
		s.asrLoading[modelID] = state
		s.mu.Unlock()

		log.Printf("MODEL %s: ASR engine load started", modelID)
		runtimeManager, err := imagegen.NewASRRuntimeManager()
		if err == nil {
			err = ensureASRRuntimeReady(ctx, runtimeManager, nil, false)
			if err == nil {
				state.engine, err = newASREngine(ctx, modelID, modelDir, runtimeManager)
			}
		}
		state.err = err

		s.mu.Lock()
		delete(s.asrLoading, modelID)
		if state.err == nil {
			s.asrEngines[modelID] = &managedASREngine{
				engine:    state.engine,
				lastUsed:  time.Now(),
				keepAlive: DefaultKeepAlive,
			}
		}
		close(state.done)
		s.mu.Unlock()

		if state.err != nil {
			log.Printf("MODEL %s: ASR engine load failed: %v", modelID, state.err)
			return nil, state.err
		}
		log.Printf("MODEL %s: ASR engine load complete", modelID)
		return state.engine, nil
	}
}

func (s *Server) closeASREngine(modelID string) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	me, ok := s.asrEngines[modelID]
	if ok {
		delete(s.asrEngines, modelID)
	}
	s.mu.Unlock()
	if ok {
		_ = me.engine.Close()
	}
}

var ensureASRRuntimeReady = func(ctx context.Context, runtimeManager *imagegen.RuntimeManager, progress imagegen.ProgressFunc, upgradePackages bool) error {
	if status := runtimeManager.ASRStatus(ctx); status.Ready && !upgradePackages {
		return nil
	}
	_, err := runtimeManager.InstallASRWithProgressOptions(ctx, progress, upgradePackages)
	return err
}

func (s *Server) closeAllEngines() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, me := range s.engines {
		me.engine.Close()
		delete(s.engines, id)
	}
	for id, me := range s.imageEngines {
		me.engine.Close()
		delete(s.imageEngines, id)
	}
	for id, me := range s.asrEngines {
		me.engine.Close()
		delete(s.asrEngines, id)
	}
}
