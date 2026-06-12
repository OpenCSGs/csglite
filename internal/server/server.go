package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/opencsgs/csghub-lite/internal/apps"
	"github.com/opencsgs/csghub-lite/internal/asr"
	"github.com/opencsgs/csghub-lite/internal/chathistory"
	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/convert"
	"github.com/opencsgs/csghub-lite/internal/dataset"
	"github.com/opencsgs/csghub-lite/internal/imagegen"
	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/internal/model"
)

const (
	DefaultKeepAlive = 5 * time.Minute
	evictorInterval  = 30 * time.Second
	engineModeChat   = "chat"
	engineModeEmbed  = "embedding"
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

type engineLoadState struct {
	done   chan struct{}
	engine inference.Engine
	err    error
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
	appShells      *aiAppShellManager
	cloud          *cloud.Service
	http           *http.Server
	logBuf         *LogBuffer

	mu           sync.RWMutex
	engines      map[string]*managedEngine
	loading      map[string]*engineLoadState
	imageEngines map[string]*managedImageEngine
	imageLoading map[string]*imageEngineLoadState
	asrEngines   map[string]*managedASREngine
	asrLoading   map[string]*asrEngineLoadState
	imageJobs    *imageGenerationJobStore
	pullJobs     *pullJobStore
	prefsMu      sync.Mutex
	openclawMu   sync.Mutex
	csgclawMu    sync.Mutex

	cloudRefreshMu   sync.Mutex
	cloudRefreshAt   time.Time
	cloudRefreshWait chan struct{}

	conversations *chathistory.Store
	apiKeys       *config.APIKeyStore
	apiUsage      *config.APIUsageStore
}

func New(cfg *config.Config, version string) *Server {
	mgr := model.NewManager(cfg)
	dsMgr := dataset.NewManager(cfg)
	logBuf := NewLogBuffer(500)
	SetupLogging(logBuf)

	cloudSvc := cloud.NewService(resolveCloudURL(cfg))
	cloudSvc.SetAccessToken(cfg.Token)

	s := &Server{
		cfg:            cfg,
		version:        version,
		manager:        mgr,
		datasetManager: dsMgr,
		appManager:     apps.NewManager(cfg),
		cloud:          cloudSvc,
		engines:        make(map[string]*managedEngine),
		loading:        make(map[string]*engineLoadState),
		imageEngines:   make(map[string]*managedImageEngine),
		imageLoading:   make(map[string]*imageEngineLoadState),
		asrEngines:     make(map[string]*managedASREngine),
		asrLoading:     make(map[string]*asrEngineLoadState),
		imageJobs:      newImageGenerationJobStore(cfg.StorageDir()),
		pullJobs:       newPullJobStore(),
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

	// Port conflict detection
	if err := checkPort(s.cfg.ListenAddr); err != nil {
		return fmt.Errorf("port %s is already in use; try a different port with --listen :PORT\n  %w", s.cfg.ListenAddr, err)
	}

	go s.startEvictor(ctx)
	go s.refreshCloudModelsOnStartup(ctx)

	errCh := make(chan error, 1)
	go func() {
		addr := s.cfg.ListenAddr
		if strings.HasPrefix(addr, ":") {
			addr = "localhost" + addr
		}
		log.Printf("csghub-lite server listening on %s", s.cfg.ListenAddr)
		log.Printf("  Web UI: %s", "http://"+addr+"/")
		log.Printf("  Ollama API: %s", "http://"+addr+"/api/chat")
		log.Printf("  OpenAI API: %s", "http://"+addr+"/v1/chat/completions")
		log.Printf("  Anthropic API: %s", "http://"+addr+"/v1/messages")
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

func (s *Server) shutdownRuntime() {
	if s.appShells != nil {
		s.appShells.CloseAll()
	}
	s.closeAllEngines()
}

// checkPort attempts to listen on the address to detect conflicts early.
func checkPort(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	ln.Close()
	return nil
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

func (s *Server) getOrLoadEmbeddingEngineWithOpts(modelID string, numCtx, nGPULayers int, dtype string) (inference.Engine, error) {
	return s.getOrLoadEngineFullMode(modelID, nil, numCtx, 0, nGPULayers, "", "", dtype, engineModeEmbed)
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
	normalizedDType, err := convert.NormalizeDType(dtype)
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

		state := &engineLoadState{done: make(chan struct{})}
		s.loading[cacheKey] = state
		log.Printf("MODEL %s: %s engine load started num_ctx=%d num_parallel=%d n_gpu_layers=%d cache_type_k=%q cache_type_v=%q dtype=%q", modelID, mode, effectiveNumCtx, effectiveNumParallel, effectiveNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV, normalizedDType)

		var oldEngine inference.Engine
		nextKeepAlive := DefaultKeepAlive
		if me, ok := s.engines[cacheKey]; ok {
			log.Printf("reloading model %s %s engine due to config change (num_ctx %d->%d, parallel %d->%d, n_gpu_layers %d->%d, cache_type_k %q->%q, cache_type_v %q->%q, dtype %q->%q)", modelID, mode, me.numCtx, effectiveNumCtx, me.numParallel, effectiveNumParallel, me.nGPULayers, effectiveNGPULayers, me.cacheTypeK, normalizedCacheTypeK, me.cacheTypeV, normalizedCacheTypeV, me.dtype, normalizedDType)
			oldEngine = me.engine
			nextKeepAlive = me.keepAlive
			delete(s.engines, cacheKey)
		}
		s.mu.Unlock()

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
