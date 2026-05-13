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
	"github.com/opencsgs/csghub-lite/internal/chathistory"
	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/convert"
	"github.com/opencsgs/csghub-lite/internal/dataset"
	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const (
	DefaultKeepAlive = 5 * time.Minute
	evictorInterval  = 30 * time.Second
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

func (m *managedEngine) keepAliveForever() bool {
	return m.keepAlive < 0
}

func (m *managedEngine) expiresAt() time.Time {
	if m.keepAliveForever() {
		return time.Time{}
	}
	return m.lastUsed.Add(m.keepAlive)
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

	mu         sync.RWMutex
	engines    map[string]*managedEngine
	loading    map[string]*engineLoadState
	prefsMu    sync.Mutex
	openclawMu sync.Mutex
	csgclawMu  sync.Mutex

	cloudRefreshMu   sync.Mutex
	cloudRefreshAt   time.Time
	cloudRefreshWait chan struct{}

	// Cache for third-party provider models to avoid repeated API calls.
	thirdPartyModelsCache   []api.ModelInfo
	thirdPartyModelsCacheAt time.Time
	thirdPartyModelsCacheMu sync.Mutex

	conversations *chathistory.Store
	apiKeys       *config.APIKeyStore
	apiUsage      *config.APIUsageStore
}

func New(cfg *config.Config, version string) *Server {
	mgr := model.NewManager(cfg)
	dsMgr := dataset.NewManager(cfg)
	logBuf := NewLogBuffer(500)
	SetupLogging(logBuf)

	s := &Server{
		cfg:            cfg,
		version:        version,
		manager:        mgr,
		datasetManager: dsMgr,
		appManager:     apps.NewManager(cfg),
		cloud:          cloud.NewService(resolveCloudURL(cfg)),
		engines:        make(map[string]*managedEngine),
		loading:        make(map[string]*engineLoadState),
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
		Addr:         cfg.ListenAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streaming responses
		IdleTimeout:  120 * time.Second,
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
		return err
	case <-ctx.Done():
		log.Println("shutting down server...")
		s.closeAllEngines()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.http.Shutdown(shutCtx)
	}
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
}

// touchEngine updates lastUsed for the given model. Must be called after
// every inference request so the evictor knows the engine is still active.
func (s *Server) touchEngine(modelID string) {
	s.mu.Lock()
	if me, ok := s.engines[modelID]; ok {
		me.lastUsed = time.Now()
	}
	s.mu.Unlock()
}

func (s *Server) setEngineKeepAlive(modelID string, keepAlive time.Duration) {
	s.mu.Lock()
	if me, ok := s.engines[modelID]; ok {
		me.keepAlive = keepAlive
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

func (s *Server) getOrLoadEngineFull(modelID string, progress inference.ConvertProgressFunc, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (inference.Engine, error) {
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

	s.mu.RLock()
	me, ok := s.engines[modelID]
	s.mu.RUnlock()
	if ok && !requestedOverrides && normalizedDType == "" {
		log.Printf("MODEL %s: using already loaded engine", modelID)
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

		if me, ok := s.engines[modelID]; ok {
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

		if state, ok := s.loading[modelID]; ok {
			log.Printf("MODEL %s: waiting for in-flight load", modelID)
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
		s.loading[modelID] = state
		log.Printf("MODEL %s: engine load started num_ctx=%d num_parallel=%d n_gpu_layers=%d cache_type_k=%q cache_type_v=%q dtype=%q", modelID, effectiveNumCtx, effectiveNumParallel, effectiveNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV, normalizedDType)

		var oldEngine inference.Engine
		nextKeepAlive := DefaultKeepAlive
		if me, ok := s.engines[modelID]; ok {
			log.Printf("reloading model %s due to config change (num_ctx %d->%d, parallel %d->%d, n_gpu_layers %d->%d, cache_type_k %q->%q, cache_type_v %q->%q, dtype %q->%q)", modelID, me.numCtx, effectiveNumCtx, me.numParallel, effectiveNumParallel, me.nGPULayers, effectiveNGPULayers, me.cacheTypeK, normalizedCacheTypeK, me.cacheTypeV, normalizedCacheTypeV, me.dtype, normalizedDType)
			oldEngine = me.engine
			nextKeepAlive = me.keepAlive
			delete(s.engines, modelID)
		}
		s.mu.Unlock()

		if oldEngine != nil {
			oldEngine.Close()
		}

		lm, err := s.manager.Get(modelID)
		if err == nil {
			state.engine, err = loadEngineWithProgress(modelDir, lm, progress, false, effectiveNumCtx, effectiveNumParallel, effectiveNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV, normalizedDType)
		}
		state.err = err

		s.mu.Lock()
		delete(s.loading, modelID)
		if state.err == nil {
			s.engines[modelID] = &managedEngine{
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
			log.Printf("MODEL %s: engine load failed: %v", modelID, state.err)
			return nil, state.err
		}
		log.Printf("MODEL %s: engine load complete", modelID)
		return state.engine, nil
	}
}

func (s *Server) closeAllEngines() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, me := range s.engines {
		me.engine.Close()
		delete(s.engines, id)
	}
}
