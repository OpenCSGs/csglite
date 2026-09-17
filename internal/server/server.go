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
	"runtime"
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
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/internal/modelmetadata"
	"github.com/opencsgs/csglite/internal/observability"
	"github.com/opencsgs/csglite/internal/tts"
	"github.com/opencsgs/csglite/pkg/api"
	routerprofile "github.com/opencsgs/semantic-router"
)

const (
	DefaultKeepAlive = 5 * time.Minute
	// DefaultSpeechKeepAlive is the idle window for recognition and speech
	// engines. Five minutes suits a text model answered on demand, but a voice
	// conversation pauses for minutes at a time and then expects an immediate
	// reply: at five minutes the model was evicted between almost every call,
	// so the caller paid an eight-second cold load before hearing anything.
	// These workers are also the ones whose load is most visible, since nothing
	// can be streamed until they are up.
	DefaultSpeechKeepAlive = 15 * time.Minute
	DesktopAPIProtocol     = "2"
	evictorInterval        = 30 * time.Second
	engineModeChat         = "chat"
	engineModeEmbed        = "embedding"
	selfHealBreakerWindow  = 2 * time.Minute
	selfHealBreakerMaxHits = 3
)

type managedEngine struct {
	engine         inference.Engine
	numCtx         int
	numParallel    int
	nGPULayers     int
	cacheTypeK     string
	cacheTypeV     string
	dtype          string
	speculativeKey string
	lastUsed       time.Time
	keepAlive      time.Duration
	activeRequests int
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
	done      chan struct{}
	configKey string
	engine    inference.Engine
	err       error
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
	// activeRequests counts callers currently holding this engine. A realtime
	// session holds it for the length of a call, which can be far longer than
	// the idle window, and evicting mid-call kills the worker under a live
	// stream: recognition then stops silently for the rest of the call.
	activeRequests int
}

type asrEngineLoadState struct {
	done   chan struct{}
	engine asr.Engine
	err    error
}

type managedTTSEngine struct {
	engine    tts.Engine
	lastUsed  time.Time
	keepAlive time.Duration
	// activeRequests counts callers currently holding this engine; see
	// managedASREngine.
	activeRequests int
}

type ttsEngineLoadState struct {
	done   chan struct{}
	engine tts.Engine
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
	cfg              *config.Config
	version          string
	manager          *model.Manager
	datasetManager   *dataset.Manager
	appManager       *apps.Manager
	sourceSwitches   *apps.SourceSwitchManager
	appShells        *aiAppShellManager
	cloud            *cloud.Service
	license          *license.Manager
	http             *http.Server
	externalHTTP     *http.Server
	authCallbackHTTP *http.Server
	logBuf           *LogBuffer

	// modelSettingsMu guards cfg.Inference.Models. It is written by the
	// model-config handler and read by every engine load, on different request
	// goroutines, and a concurrent map read and write is a non-recoverable
	// runtime fatal rather than a panic. It is always released before mu is
	// taken, so the two never nest in both directions.
	modelSettingsMu sync.RWMutex

	mu           sync.RWMutex
	engines      map[string]*managedEngine
	loading      map[string]*engineLoadState
	selfHeal     map[string]selfHealBreakerState
	imageEngines map[string]*managedImageEngine
	imageLoading map[string]*imageEngineLoadState
	asrEngines   map[string]*managedASREngine
	asrLoading   map[string]*asrEngineLoadState
	ttsEngines   map[string]*managedTTSEngine
	ttsLoading   map[string]*ttsEngineLoadState
	// ttsVoices caches each model's voice list, which is fixed for the model
	// and therefore outlives the worker that reported it.
	ttsVoices map[string]*api.SpeechVoicesResponse
	// loadCtx outlives any one request and is what model loads run on, so a
	// caller that disconnects cannot cancel a load other callers are waiting
	// for. Guarded by mu; nil before Run, which is how the tests get a plain
	// background context.
	loadCtx       context.Context
	realtimeCalls map[string]*realtimeCall
	// realtimeSockets counts live WebSocket realtime sessions, which have no
	// registry of their own but share the session cap with WebRTC calls.
	realtimeSockets    int
	imageJobs          *imageGenerationJobStore
	pullJobs           *pullJobStore
	datasetExportJobs  *datasetExportJobStore
	loadStepMu         sync.Mutex
	loadSteps          map[string]loadStepState
	prefsMu            sync.Mutex
	openclawMu         sync.Mutex
	csgclawMu          sync.Mutex
	poolMu             sync.Mutex
	poolCurrent        map[string]int
	poolRuntime        map[string]*providerPoolMemberRuntime
	poolAffinity       map[string]providerPoolAffinityEntry
	routerProfileMu    sync.RWMutex
	routerProfileCache map[string]*routerprofile.Profile
	pricingMu          sync.RWMutex
	pricingCache       map[string]requestCostSnapshot

	cloudRefreshMu   sync.Mutex
	cloudRefreshAt   time.Time
	cloudRefreshWait chan struct{}

	// aiAppRuntimeMu guards aiAppRuntimeCache, which memoizes the result of
	// runtime liveness probes (openclaw/csgclaw/dsh/xiaozhi) so the AI Apps
	// poll does not dial TCP or fork `docker compose ps` every few seconds.
	aiAppRuntimeMu    sync.Mutex
	aiAppRuntimeCache map[string]aiAppRuntimeCacheEntry

	conversations             *chathistory.Store
	apiKeys                   *config.APIKeyStore
	apiUsage                  *config.APIUsageStore
	observabilityMu           sync.RWMutex
	observability             *observability.Store
	observabilityCleanupAt    atomic.Int64
	modelMetadataMu           sync.RWMutex
	modelMetadata             *modelmetadata.Store
	routerProfiles            *routerprofile.Store
	routerStoreMu             sync.RWMutex
	retiredRouterProfiles     []*routerprofile.Store
	routerCurationMu          sync.Mutex
	routerCurationState       map[string]uint8
	routerCurationQueue       chan string
	routerCurationWG          sync.WaitGroup
	routerCurationCancel      context.CancelFunc
	routerEvaluationWG        sync.WaitGroup
	routerEvaluationCancel    context.CancelFunc
	routerEvaluationWake      chan struct{}
	routerEvaluationMu        sync.Mutex
	routerEvaluationPoolID    string
	routerEvaluationJobID     string
	routerEvaluationRunCancel context.CancelFunc
	evaluationEngineFactory   func(context.Context, string, string) (inference.Engine, error)
	evaluationCatalogLoader   func(context.Context) ([]api.ModelInfo, error)
	desktopBootstrapped       atomic.Bool

	// shutdownCancel stops the Run loop. It is set in Run and invoked by the
	// /api/shutdown handler so an HTTP-initiated shutdown actually exits the
	// process instead of only closing the listeners.
	shutdownCancel context.CancelFunc
}

type desktopReady struct {
	Event          string `json:"event"`
	URL            string `json:"url"`
	BootstrapURL   string `json:"bootstrap_url"`
	ExternalAPIURL string `json:"external_api_url"`
	ControlToken   string `json:"control_token"`
	Version        string `json:"version"`
	APIProtocol    string `json:"api_protocol"`
	InstanceID     string `json:"instance_id"`
	PID            int    `json:"pid"`
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
		cfg:                  cfg,
		version:              version,
		license:              newLicenseManager(storageRoot, version),
		manager:              mgr,
		datasetManager:       dsMgr,
		appManager:           apps.NewManager(cfg),
		sourceSwitches:       apps.NewSourceSwitchManager(storageRoot),
		cloud:                cloudSvc,
		engines:              make(map[string]*managedEngine),
		loading:              make(map[string]*engineLoadState),
		poolCurrent:          make(map[string]int),
		poolRuntime:          make(map[string]*providerPoolMemberRuntime),
		poolAffinity:         make(map[string]providerPoolAffinityEntry),
		routerProfileCache:   make(map[string]*routerprofile.Profile),
		pricingCache:         make(map[string]requestCostSnapshot),
		aiAppRuntimeCache:    make(map[string]aiAppRuntimeCacheEntry),
		selfHeal:             make(map[string]selfHealBreakerState),
		imageEngines:         make(map[string]*managedImageEngine),
		imageLoading:         make(map[string]*imageEngineLoadState),
		ttsVoices:            make(map[string]*api.SpeechVoicesResponse),
		asrEngines:           make(map[string]*managedASREngine),
		asrLoading:           make(map[string]*asrEngineLoadState),
		ttsEngines:           make(map[string]*managedTTSEngine),
		ttsLoading:           make(map[string]*ttsEngineLoadState),
		realtimeCalls:        make(map[string]*realtimeCall),
		imageJobs:            newImageGenerationJobStore(cfg.StorageDir()),
		pullJobs:             newPullJobStore(),
		datasetExportJobs:    newDatasetExportJobStore(),
		loadSteps:            make(map[string]loadStepState),
		logBuf:               logBuf,
		routerCurationState:  make(map[string]uint8),
		routerCurationQueue:  make(chan string, 16),
		routerEvaluationWake: make(chan struct{}, 1),
	}
	s.appShells = newAIAppShellManager()
	if store, err := observability.Open(storageRoot); err != nil {
		log.Printf("OBSERVABILITY: database unavailable: %v", err)
	} else {
		s.observability = store
		if err := store.ReconcileUsage(context.Background(), func(body string) (int64, int64, bool) {
			usage := observationResponseUsageFromBodies([]byte(body))
			return usage.inputTokens, usage.outputTokens, usage.hasInputTokens && usage.hasOutputTokens
		}); err != nil {
			log.Printf("OBSERVABILITY: usage reconciliation failed: %v", err)
		}
		if _, err := store.Cleanup(context.Background(), config.ObservabilityRetentionDays(cfg.Observability)); err != nil {
			log.Printf("OBSERVABILITY: retention cleanup failed: %v", err)
		}
	}
	if store, err := modelmetadata.Open(storageRoot); err != nil {
		log.Printf("MODEL METADATA: cache unavailable: %v", err)
	} else {
		s.modelMetadata = store
	}
	if store, err := routerprofile.Open(storageRoot); err != nil {
		log.Printf("SEMANTIC ROUTER: profile database unavailable: %v", err)
	} else {
		s.routerProfiles = store
		cutoff := time.Now().UTC().Add(-time.Duration(config.ObservabilityRetentionDays(cfg.Observability)) * 24 * time.Hour)
		if _, err := store.PurgeTraceDataBefore(context.Background(), cutoff); err != nil &&
			!errors.Is(err, routerprofile.ErrConflict) {
			log.Printf("SEMANTIC ROUTER: retention cleanup failed: %v", err)
		}
		if err := s.refreshAllRouterProfiles(context.Background()); err != nil {
			log.Printf("SEMANTIC ROUTER: loading active profiles failed: %v", err)
		}
	}

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
	if cfg.DesktopMode {
		s.externalHTTP = &http.Server{
			Addr:              cfg.DesktopAPIBindAddr,
			Handler:           s.externalAPIRoutes(),
			ReadHeaderTimeout: 30 * time.Second,
			WriteTimeout:      0,
			IdleTimeout:       120 * time.Second,
		}
	}
	return s
}

// authCallbackAddr returns the loopback address dedicated to receiving the
// "lite" SSO redirect, so the browser can hand the access token back to this
// process on a stable port regardless of the main listener address.
func (s *Server) authCallbackAddr() string {
	if s != nil && strings.TrimSpace(s.cfg.AuthCallbackAddr) != "" {
		return strings.TrimSpace(s.cfg.AuthCallbackAddr)
	}
	return config.DefaultAuthCallbackAddr
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
	s.shutdownCancel = stop

	if s.cfg.DesktopMode {
		if err := validateDesktopConfig(s.cfg); err != nil {
			return err
		}
	}
	listenAddr := s.cfg.EffectiveListenAddr()
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("port %s is already in use; try a different port with --listen :PORT\n  %w", listenAddr, err)
	}
	defer listener.Close()
	boundAddr := listener.Addr().String()
	s.cfg.BoundAddr = boundAddr

	var externalListener net.Listener
	if s.cfg.DesktopMode {
		externalListener, err = net.Listen("tcp", s.cfg.DesktopAPIBindAddr)
		if err != nil {
			return fmt.Errorf("desktop API port %s is already in use; close the conflicting application and restart csglite: %w", s.cfg.DesktopAPIBindAddr, err)
		}
		defer externalListener.Close()
		s.cfg.DesktopAPIBoundAddr = externalListener.Addr().String()
	}

	authCallbackListener, err := net.Listen("tcp", s.authCallbackAddr())
	if err != nil {
		log.Printf("cloud auth callback listener unavailable on %s: %v", s.authCallbackAddr(), err)
	} else {
		defer authCallbackListener.Close()
		s.authCallbackHTTP = &http.Server{
			Handler:           s.authCallbackRoutes(),
			ReadHeaderTimeout: 30 * time.Second,
			WriteTimeout:      0,
			IdleTimeout:       120 * time.Second,
		}
	}

	s.mu.Lock()
	s.loadCtx = ctx
	s.mu.Unlock()

	go s.startEvictor(ctx)
	go s.refreshCloudModelsOnStartup(ctx)
	go s.license.Run(ctx, license.DefaultRefreshInterval)
	curationCtx, cancelCuration := context.WithCancel(ctx)
	s.routerCurationCancel = cancelCuration
	s.routerCurationWG.Add(1)
	go func() {
		defer s.routerCurationWG.Done()
		s.startRouterCuration(curationCtx)
	}()
	if s.routerProfiles != nil {
		evaluationCtx, cancelEvaluation := context.WithCancel(ctx)
		s.routerEvaluationCancel = cancelEvaluation
		s.routerEvaluationWG.Add(1)
		go func() {
			defer s.routerEvaluationWG.Done()
			s.startRouterEvaluationWorker(evaluationCtx)
		}()
	}

	errCh := make(chan error, 3)
	if s.cfg.DesktopMode {
		baseURL := "http://" + boundAddr
		ready := desktopReady{
			Event:          "ready",
			URL:            baseURL,
			BootstrapURL:   baseURL + "/?desktop_token=" + url.QueryEscape(s.cfg.DesktopToken),
			ExternalAPIURL: "http://" + s.cfg.DesktopAPIAddr,
			ControlToken:   s.cfg.DesktopControlToken,
			Version:        s.version,
			APIProtocol:    DesktopAPIProtocol,
			InstanceID:     s.cfg.DesktopInstanceID,
			PID:            os.Getpid(),
		}
		payload, marshalErr := json.Marshal(ready)
		if marshalErr != nil {
			return fmt.Errorf("encoding desktop ready event: %w", marshalErr)
		}
		fmt.Printf("CSGLITE_DESKTOP_READY %s\n", payload)
	}
	go func() {
		addr := displayServerAddr(boundAddr)
		// The version goes in the log on every start: a user report arrives as
		// a log file, and without it the build has to be guessed from which
		// static assets the browser requested.
		log.Printf("csghub-lite %s (%s/%s, %s) starting", s.displayVersion(), runtime.GOOS, runtime.GOARCH, runtime.Version())
		log.Printf("csghub-lite server listening on %s", boundAddr)
		log.Printf("  Web UI: %s", "http://"+addr+"/")
		log.Printf("  Ollama API: %s", "http://"+addr+"/api/chat")
		log.Printf("  OpenAI API: %s", "http://"+addr+"/v1/chat/completions")
		log.Printf("  Anthropic API: %s", "http://"+addr+"/v1/messages")
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	if externalListener != nil {
		go func() {
			log.Printf("  Desktop API: http://%s", s.cfg.DesktopAPIBoundAddr)
			if err := s.externalHTTP.Serve(externalListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}
	if s.authCallbackHTTP != nil {
		go func() {
			log.Printf("  Auth callback: http://%s", s.authCallbackAddr())
			if err := s.authCallbackHTTP.Serve(authCallbackListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	select {
	case err := <-errCh:
		s.shutdownRuntime()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.shutdownHTTPServers(shutCtx)
		return err
	case <-ctx.Done():
		log.Println("shutting down server...")
		s.shutdownRuntime()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.shutdownHTTPServers(shutCtx)
	}
}

func displayServerAddr(boundAddr string) string {
	host, port, err := net.SplitHostPort(boundAddr)
	if err != nil {
		if strings.HasPrefix(boundAddr, ":") {
			return "localhost" + boundAddr
		}
		return boundAddr
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && (ip.IsUnspecified() || ip.IsLoopback())) {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func (s *Server) shutdownHTTPServers(ctx context.Context) error {
	var externalErr error
	if s.externalHTTP != nil {
		externalErr = s.externalHTTP.Shutdown(ctx)
	}
	if s.authCallbackHTTP != nil {
		if err := s.authCallbackHTTP.Shutdown(ctx); err != nil && externalErr == nil {
			externalErr = err
		}
	}
	internalErr := s.http.Shutdown(ctx)
	if internalErr != nil {
		return internalErr
	}
	return externalErr
}

func validateDesktopConfig(cfg *config.Config) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(cfg.EffectiveListenAddr()))
	if err != nil || !isDesktopLoopbackHost(host) {
		return fmt.Errorf("desktop mode requires an explicit loopback listen address")
	}
	if strings.TrimSpace(cfg.DesktopAPIAddr) != config.DefaultDesktopAPIAddr {
		return fmt.Errorf("desktop mode requires API address %s", config.DefaultDesktopAPIAddr)
	}
	if strings.TrimSpace(cfg.DesktopAPIBindAddr) != config.DefaultDesktopAPIBindAddr {
		return fmt.Errorf("desktop mode requires API bind address %s", config.DefaultDesktopAPIBindAddr)
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
	if s.routerCurationCancel != nil {
		s.routerCurationCancel()
	}
	if s.routerEvaluationCancel != nil {
		s.routerEvaluationCancel()
	}
	s.routerCurationWG.Wait()
	s.routerEvaluationWG.Wait()
	if s.appShells != nil {
		s.appShells.CloseAll()
	}
	s.closeRealtimeCalls()
	s.closeAllEngines()
	s.observabilityMu.Lock()
	if s.observability != nil {
		_ = s.observability.Close()
		s.observability = nil
	}
	s.observabilityMu.Unlock()
	if s.apiUsage != nil {
		_ = s.apiUsage.Close()
	}
	s.modelMetadataMu.Lock()
	if s.modelMetadata != nil {
		_ = s.modelMetadata.Close()
		s.modelMetadata = nil
	}
	s.modelMetadataMu.Unlock()
	s.routerStoreMu.Lock()
	defer s.routerStoreMu.Unlock()
	s.routerProfileMu.Lock()
	if s.routerProfiles != nil {
		_ = s.routerProfiles.Close()
		s.routerProfiles = nil
	}
	for _, store := range s.retiredRouterProfiles {
		_ = store.Close()
	}
	s.retiredRouterProfiles = nil
	s.routerProfileCache = make(map[string]*routerprofile.Profile)
	s.routerProfileMu.Unlock()
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
		if me.activeRequests > 0 {
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
		if me.activeRequests > 0 {
			continue
		}
		if now.After(me.lastUsed.Add(me.keepAlive)) {
			log.Printf("evicting idle ASR model %s (unused for %s)", id, me.keepAlive)
			me.engine.Close()
			delete(s.asrEngines, id)
		}
	}
	for id, me := range s.ttsEngines {
		if me.keepAlive < 0 {
			continue
		}
		if me.activeRequests > 0 {
			continue
		}
		if now.After(me.lastUsed.Add(me.keepAlive)) {
			log.Printf("evicting idle TTS model %s (unused for %s)", id, me.keepAlive)
			me.engine.Close()
			delete(s.ttsEngines, id)
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

// beginEngineUse keeps a cached engine alive while an inference request is in
// progress. Engines not managed by the local cache return a no-op release.
func (s *Server) beginEngineUse(modelID, mode string, engine inference.Engine) func() {
	key := engineCacheKey(s.resolveLocalModelStorageID(modelID), mode)
	s.mu.Lock()
	me, ok := s.engines[key]
	if !ok || me.engine != engine {
		s.mu.Unlock()
		return func() {}
	}
	me.activeRequests++
	me.lastUsed = time.Now()
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		if me.activeRequests > 0 {
			me.activeRequests--
		}
		me.lastUsed = time.Now()
		s.mu.Unlock()
	}
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

// displayVersion is the build version for logs. A binary built without the
// release ldflags reports "dev"; an empty value would otherwise print as a gap
// in the banner.
func (s *Server) displayVersion() string {
	if version := strings.TrimSpace(s.version); version != "" {
		return version
	}
	return "dev"
}

// defaultKeepAliveFor reports the idle window a model gets with no per-model
// setting, for a caller that does not already know which runtime serves it.
// Recognition and speech models idle out later than text models, and working
// this out reads the model directory, so the load paths pass their own runtime
// default to resolveModelKeepAlive instead of calling this.
func (s *Server) defaultKeepAliveFor(modelID string) time.Duration {
	if s.modelUsesASREngine(modelID) || s.modelUsesTTSEngine(modelID) {
		return DefaultSpeechKeepAlive
	}
	return DefaultKeepAlive
}

// resolveModelKeepAlive picks the idle window for a load that carries none of
// its own: the model's saved setting wins over runtimeDefault. A keep-alive
// sent with a request is applied on top of this by the caller, so the order is
// request > per-model setting > runtime default, matching how the context
// window and slot count resolve.
//
// Resolving here rather than only in /api/load is the point of the setting: a
// model loaded by an ordinary chat request carries no keep-alive at all, and
// before this it silently fell back to the default no matter what the user had
// chosen in the run dialog.
func (s *Server) resolveModelKeepAlive(modelID string, runtimeDefault time.Duration) time.Duration {
	if keepAlive, ok := s.modelKeepAliveSetting(modelID); ok {
		return keepAlive
	}
	return runtimeDefault
}

// effectiveModelKeepAlive is resolveModelKeepAlive for a caller that does not
// already know which runtime serves the model, such as the config endpoint. It
// reports what the next load of the model would use with no keep-alive of its
// own.
func (s *Server) effectiveModelKeepAlive(modelID string) time.Duration {
	return s.resolveModelKeepAlive(modelID, s.defaultKeepAliveFor(modelID))
}

// applyModelKeepAlive updates the idle window of whichever engine currently
// serves modelID, so a setting saved while a model is loaded takes effect
// without reloading it.
func (s *Server) applyModelKeepAlive(modelID string, keepAlive time.Duration) {
	s.setEngineKeepAlive(modelID, keepAlive)
	s.setImageEngineKeepAlive(modelID, keepAlive)
	s.setASREngineKeepAlive(modelID, keepAlive)
	s.setTTSEngineKeepAlive(modelID, keepAlive)
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

func (s *Server) setTTSEngineKeepAlive(modelID string, keepAlive time.Duration) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	if me, ok := s.ttsEngines[modelID]; ok {
		me.keepAlive = keepAlive
	}
	s.mu.Unlock()
}

func (s *Server) touchTTSEngine(modelID string) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	if me, ok := s.ttsEngines[modelID]; ok {
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

func (s *Server) getOrLoadEngineWithProgressAndSpeculativeOpts(modelID string, progress inference.ConvertProgressFunc, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string, speculative inference.SpeculativeConfig) (inference.Engine, error) {
	return s.getOrLoadEngineFullSpeculative(modelID, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, speculative)
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
var loadEngineWithSpeculativeProgress = func(modelDir string, lm *model.LocalModel, progress inference.ConvertProgressFunc, verbose bool, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string, speculative inference.SpeculativeConfig) (inference.Engine, error) {
	if !speculative.Enabled() {
		return loadEngineWithProgress(modelDir, lm, progress, verbose, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
	}
	return inference.LoadEngineWithSpeculativeProgress(modelDir, lm, progress, verbose, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, speculative)
}
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
	return s.getOrLoadEngineFullMode(modelID, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, engineModeChat, inference.SpeculativeConfig{}, false)
}

func (s *Server) getOrLoadEngineFullSpeculative(modelID string, progress inference.ConvertProgressFunc, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string, speculative inference.SpeculativeConfig) (inference.Engine, error) {
	return s.getOrLoadEngineFullMode(modelID, progress, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, engineModeChat, speculative, true)
}

func (s *Server) getOrLoadEmbeddingEngineWithOpts(ctx context.Context, modelID string, numCtx, numParallel, nGPULayers int, dtype string) (inference.Engine, error) {
	return s.getOrLoadEmbeddingEngineWithProgress(ctx, modelID, nil, numCtx, numParallel, nGPULayers, dtype)
}

// getOrLoadEmbeddingEngineWithProgress loads the embedding engine. numParallel
// is honoured here as it is for chat: llama-server serves that many requests at
// once, which is what an embedding workload batching many documents needs. It
// was previously pinned to the default, so the slot count could not be raised
// however the model was loaded.
func (s *Server) getOrLoadEmbeddingEngineWithProgress(ctx context.Context, modelID string, progress inference.ConvertProgressFunc, numCtx, numParallel, nGPULayers int, dtype string) (inference.Engine, error) {
	if s.shouldUsePythonEmbeddingRuntime(modelID) {
		// The Python embedding runtime batches inside the worker and takes none
		// of the llama.cpp load options.
		return s.getOrLoadPythonEmbeddingEngine(ctx, modelID)
	}
	return s.getOrLoadEngineFullMode(modelID, progress, numCtx, numParallel, nGPULayers, "", "", dtype, engineModeEmbed, inference.SpeculativeConfig{}, false)
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

	s.mu.Lock()
	me, ok := s.engines[cacheKey]
	if ok {
		me.lastUsed = time.Now()
		eng := me.engine
		s.mu.Unlock()
		return eng, nil
	}
	s.mu.Unlock()

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found locally; use 'csghub-lite pull %s' first", modelID, modelID)
	}

	for {
		s.mu.Lock()
		if me, ok := s.engines[cacheKey]; ok {
			me.lastUsed = time.Now()
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

		keepAlive := s.resolveModelKeepAlive(modelID, DefaultKeepAlive)
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
				keepAlive: keepAlive,
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

func (s *Server) getOrLoadEngineFullMode(modelID string, progress inference.ConvertProgressFunc, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype, mode string, speculative inference.SpeculativeConfig, speculativeRequested bool) (inference.Engine, error) {
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
	requestedDType, err := convert.NormalizeRuntimeDType(dtype)
	if err != nil {
		return nil, err
	}
	// A caller that names no quantization gets the one saved for this model
	// rather than the repository default. Without this a reload triggered by
	// anything else -- a changed runtime option, an idle eviction -- silently
	// swaps a repository's Q5 build back to the Q8 build that FindModelFile
	// picks, which is neither what the user chose nor what it costs in memory.
	normalizedDType := requestedDType
	if normalizedDType == "" {
		normalizedDType = s.modelDTypeSetting(modelID)
	}
	speculative, err = inference.NormalizeSpeculativeConfig(speculative)
	if err != nil {
		return nil, err
	}
	if mode == engineModeEmbed && speculative.Enabled() {
		return nil, fmt.Errorf("speculative decoding is not supported for embedding models")
	}
	speculativeKey := speculative.Key()
	runtimeOverrides := runtimeOverridesRequested(numCtx, numParallel, normalizedNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV)
	requestedOverrides := runtimeOverrides || speculativeRequested
	cacheKey := engineCacheKey(modelID, mode)

	s.mu.Lock()
	me, ok := s.engines[cacheKey]
	// A caller that named a dtype still takes the long route: this shortcut
	// skips the numCtx/numParallel/nGPULayers/cache comparison entirely, and a
	// load naming only a dtype carries no runtime override to force it open.
	if ok && !runtimeOverrides && requestedDType == "" && loadedDTypeMatchesRequest(me.dtype, normalizedDType) && (!speculativeRequested || me.speculativeKey == speculativeKey) {
		me.lastUsed = time.Now()
		eng := me.engine
		s.mu.Unlock()
		log.Printf("MODEL %s: using already loaded %s engine", modelID, mode)
		return eng, nil
	}
	s.mu.Unlock()

	modelDir, err := s.manager.ModelPath(modelID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found locally; use 'csghub-lite pull %s' first", modelID, modelID)
	}
	// Refuse text-to-speech models here rather than at each entry point: every
	// chat, generate and load path for the text-generation runtime funnels
	// through this function, and without the guard a safetensors TTS model is
	// converted to GGUF and served as a text model. See
	// docs/guides/realtime-audio-api.md.
	if s.modelUsesTTSEngine(modelID) {
		return nil, fmt.Errorf("model %q is a text-to-speech model; use POST /v1/audio/speech, which serves it through the Python text-to-speech runtime", modelID)
	}
	effectiveNumCtx := inference.ResolveNumCtxWithModelSetting(modelDir, numCtx, s.modelNumCtxSetting(modelID), s.cfg.Inference.LlamaUseModelMaxCtx)
	if mode == engineModeEmbed {
		// An embedding model cannot consume more than its own maximum sequence
		// length, and the context is allocated per slot, so anything above it
		// is reserved and never used.
		if capped := inference.CapNumCtxToEmbeddingModelMax(modelDir, effectiveNumCtx); capped != effectiveNumCtx {
			log.Printf("MODEL %s: capping embedding context %d to the model's maximum %d", modelID, effectiveNumCtx, capped)
			effectiveNumCtx = capped
		}
	}
	effectiveNumParallel := inference.ResolveNumParallelWithModelSetting(numParallel, s.modelNumParallelSetting(modelID), s.cfg.Inference.LlamaNumParallel)
	// Resolved with the other per-model settings, before the engine lock is
	// taken: reading them touches the config and the model directory.
	resolvedKeepAlive := s.resolveModelKeepAlive(modelID, DefaultKeepAlive)
	effectiveNGPULayers := inference.ResolveNGPULayers(normalizedNGPULayers)
	loadConfigKey := fmt.Sprintf(
		"%d|%d|%d|%s|%s|%s|%s",
		effectiveNumCtx,
		effectiveNumParallel,
		effectiveNGPULayers,
		normalizedCacheTypeK,
		normalizedCacheTypeV,
		normalizedDType,
		speculativeKey,
	)
	// Only a dtype the caller named is checked for a pending conversion: the
	// check parses the header of every GGUF in the model directory, and the
	// config endpoint already refused to save a dtype the model cannot serve,
	// so running it for every chat request would be pure cost.
	needsRequestedDTypeConversion := false
	if requestedDType != "" {
		if needs, err := convert.NeedsConversionForDType(modelDir, requestedDType); err != nil {
			return nil, err
		} else {
			needsRequestedDTypeConversion = needs
		}
	}

	for {
		s.mu.Lock()

		if me, ok := s.engines[cacheKey]; ok {
			if !requestedOverrides && requestedDType == "" && loadedDTypeMatchesRequest(me.dtype, normalizedDType) {
				me.lastUsed = time.Now()
				eng := me.engine
				s.mu.Unlock()
				return eng, nil
			}
			dtypeReady := normalizedDType == "" || (loadedDTypeMatchesRequest(me.dtype, normalizedDType) && !needsRequestedDTypeConversion)
			speculativeReady := !speculativeRequested || me.speculativeKey == speculativeKey
			if me.numCtx == effectiveNumCtx && me.numParallel == effectiveNumParallel && me.nGPULayers == effectiveNGPULayers && me.cacheTypeK == normalizedCacheTypeK && me.cacheTypeV == normalizedCacheTypeV && speculativeReady && dtypeReady {
				me.lastUsed = time.Now()
				eng := me.engine
				s.mu.Unlock()
				return eng, nil
			}
		}

		if state, ok := s.loading[cacheKey]; ok {
			log.Printf("MODEL %s: waiting for in-flight %s load", modelID, mode)
			sameConfig := state.configKey == loadConfigKey
			s.mu.Unlock()
			<-state.done
			if !sameConfig {
				continue
			}
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

		state := &engineLoadState{
			done:      make(chan struct{}),
			configKey: loadConfigKey,
		}
		s.loading[cacheKey] = state
		nextKeepAlive := resolvedKeepAlive
		log.Printf("MODEL %s: %s engine load started num_ctx=%d num_parallel=%d n_gpu_layers=%d cache_type_k=%q cache_type_v=%q dtype=%q speculative=%q keep_alive=%s", modelID, mode, effectiveNumCtx, effectiveNumParallel, effectiveNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV, normalizedDType, speculativeKey, api.FormatKeepAlive(nextKeepAlive))

		var oldEngine inference.Engine
		var closedEngines []inference.Engine
		if rocmSingleEngineMode() {
			closedEngines = s.closeOtherInferenceEnginesLocked(cacheKey)
		}
		if me, ok := s.engines[cacheKey]; ok {
			log.Printf("reloading model %s %s engine due to config change (num_ctx %d->%d, parallel %d->%d, n_gpu_layers %d->%d, cache_type_k %q->%q, cache_type_v %q->%q, dtype %q->%q, speculative %q->%q)", modelID, mode, me.numCtx, effectiveNumCtx, me.numParallel, effectiveNumParallel, me.nGPULayers, effectiveNGPULayers, me.cacheTypeK, normalizedCacheTypeK, me.cacheTypeV, normalizedCacheTypeV, me.dtype, normalizedDType, me.speculativeKey, speculativeKey)
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
			if mode == engineModeEmbed {
				state.engine, err = loadEmbeddingEngineWithProgress(modelDir, lm, progress, false, effectiveNumCtx, effectiveNumParallel, effectiveNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV, normalizedDType)
			} else {
				state.engine, err = loadEngineWithSpeculativeProgress(modelDir, lm, progress, false, effectiveNumCtx, effectiveNumParallel, effectiveNGPULayers, normalizedCacheTypeK, normalizedCacheTypeV, normalizedDType, speculative)
			}
		}
		state.err = err

		s.mu.Lock()
		delete(s.loading, cacheKey)
		if state.err == nil {
			s.engines[cacheKey] = &managedEngine{
				engine:         state.engine,
				numCtx:         effectiveNumCtx,
				numParallel:    effectiveNumParallel,
				nGPULayers:     effectiveNGPULayers,
				cacheTypeK:     normalizedCacheTypeK,
				cacheTypeV:     normalizedCacheTypeV,
				dtype:          normalizedDType,
				speculativeKey: speculativeKey,
				lastUsed:       time.Now(),
				keepAlive:      nextKeepAlive,
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

		keepAlive := s.resolveModelKeepAlive(modelID, DefaultKeepAlive)
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
				keepAlive: keepAlive,
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
		err := s.checkCachedEngine(ctx, me.engine)
		if err == nil {
			return me.engine, nil
		}
		// The worker is bound to its port but cannot serve a request. Left
		// cached it is handed to every session that follows, which is how a
		// single wedged worker took out recognition for hours; dropping it here
		// means the next session loads a healthy one.
		log.Printf("MODEL %s: replacing unresponsive ASR engine: %v", modelID, err)
		s.dropASREngine(modelID, me)
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
		state, loading := s.asrLoading[modelID]
		if !loading {
			state = &asrEngineLoadState{done: make(chan struct{})}
			s.asrLoading[modelID] = state
			go s.loadASREngine(modelID, modelDir, state)
		}
		s.mu.Unlock()

		select {
		case <-state.done:
		case <-ctx.Done():
			// This caller has given up, but the load has not: it keeps running
			// so the next request finds a loaded model instead of starting the
			// same multi-minute install over again.
			return nil, ctx.Err()
		}
		if state.err != nil {
			return nil, state.err
		}
		if state.engine != nil {
			return state.engine, nil
		}
	}
}

// loadASREngine brings up a worker and publishes it to the cache. It runs on
// the server's own context rather than the requesting caller's: a client that
// disconnects while a cold model is loading would otherwise cancel the load,
// and the request that follows would start it again from nothing.
func (s *Server) loadASREngine(modelID, modelDir string, state *asrEngineLoadState) {
	ctx := s.engineLoadContext()
	keepAlive := s.resolveModelKeepAlive(modelID, DefaultSpeechKeepAlive)
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
			keepAlive: keepAlive,
		}
	}
	close(state.done)
	s.mu.Unlock()

	if state.err != nil {
		log.Printf("MODEL %s: ASR engine load failed: %v", modelID, state.err)
		return
	}
	log.Printf("MODEL %s: ASR engine load complete", modelID)
}

// engineLoadContext is the context a model load runs on: the server's own, so
// it ends at shutdown and at nothing else.
func (s *Server) engineLoadContext() context.Context {
	s.mu.RLock()
	ctx := s.loadCtx
	s.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// healthChecker is implemented by engines whose worker can be asked whether it
// is still answering. Engines without it are assumed healthy, which is what
// keeps the in-memory fakes the tests use working unchanged.
type healthChecker interface {
	Health(ctx context.Context) error
}

// checkCachedEngine probes an engine taken from the cache. It reports nil for
// engines that cannot be probed, so an unprobeable engine is never discarded
// for failing a check it never ran.
func (s *Server) checkCachedEngine(ctx context.Context, engine interface{}) error {
	checker, ok := engine.(healthChecker)
	if !ok {
		return nil
	}
	return checker.Health(ctx)
}

// dropASREngine removes an engine from the cache and shuts its worker down. It
// only acts while the cache still holds the same engine, so two callers racing
// on the same broken worker close it once.
func (s *Server) dropASREngine(modelID string, expected *managedASREngine) {
	s.mu.Lock()
	current, ok := s.asrEngines[modelID]
	if !ok || (expected != nil && current != expected) {
		s.mu.Unlock()
		return
	}
	delete(s.asrEngines, modelID)
	s.mu.Unlock()
	_ = current.engine.Close()
}

// dropTTSEngine is dropASREngine for the speech side.
func (s *Server) dropTTSEngine(modelID string, expected *managedTTSEngine) {
	s.mu.Lock()
	current, ok := s.ttsEngines[modelID]
	if !ok || (expected != nil && current != expected) {
		s.mu.Unlock()
		return
	}
	delete(s.ttsEngines, modelID)
	s.mu.Unlock()
	_ = current.engine.Close()
}

// retainASREngine marks the engine as in use so the idle reaper leaves it
// alone, and refreshes its idle clock. The returned func releases it; it is
// safe to call once the engine has already been dropped.
func (s *Server) retainASREngine(modelID string) func() {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	me, ok := s.asrEngines[modelID]
	if ok {
		me.activeRequests++
		me.lastUsed = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if me.activeRequests > 0 {
				me.activeRequests--
			}
			me.lastUsed = time.Now()
			s.mu.Unlock()
		})
	}
}

// retainTTSEngine is retainASREngine for the speech side.
func (s *Server) retainTTSEngine(modelID string) func() {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	me, ok := s.ttsEngines[modelID]
	if ok {
		me.activeRequests++
		me.lastUsed = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if me.activeRequests > 0 {
				me.activeRequests--
			}
			me.lastUsed = time.Now()
			s.mu.Unlock()
		})
	}
}

var newTTSEngine = func(ctx context.Context, modelName, modelDir string, runtimeManager *imagegen.RuntimeManager) (tts.Engine, error) {
	return tts.NewPythonEngine(ctx, modelName, modelDir, runtimeManager)
}

func (s *Server) getOrLoadTTSEngine(ctx context.Context, modelID string) (tts.Engine, error) {
	modelID = s.resolveLocalModelStorageID(modelID)

	s.mu.RLock()
	me, ok := s.ttsEngines[modelID]
	s.mu.RUnlock()
	if ok {
		err := s.checkCachedEngine(ctx, me.engine)
		if err == nil {
			return me.engine, nil
		}
		// A worker that died mid-synthesis stays cached until something tries
		// to use it and gets "connection refused". Replacing it here turns that
		// into one slow request instead of a failed one.
		log.Printf("MODEL %s: replacing unresponsive TTS engine: %v", modelID, err)
		s.dropTTSEngine(modelID, me)
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
	if !isTTSPipelineTag(pipelineTag) {
		return nil, fmt.Errorf("model %q is not a text-to-speech model", modelID)
	}

	for {
		s.mu.Lock()
		if me, ok := s.ttsEngines[modelID]; ok {
			eng := me.engine
			s.mu.Unlock()
			return eng, nil
		}
		state, loading := s.ttsLoading[modelID]
		if !loading {
			state = &ttsEngineLoadState{done: make(chan struct{})}
			s.ttsLoading[modelID] = state
			go s.loadTTSEngine(modelID, modelDir, state)
		}
		s.mu.Unlock()

		select {
		case <-state.done:
		case <-ctx.Done():
			// See loadASREngine: the caller gives up, the load does not.
			return nil, ctx.Err()
		}
		if state.err != nil {
			return nil, state.err
		}
		if state.engine != nil {
			return state.engine, nil
		}
	}
}

// loadTTSEngine is loadASREngine for the speech side.
func (s *Server) loadTTSEngine(modelID, modelDir string, state *ttsEngineLoadState) {
	ctx := s.engineLoadContext()
	keepAlive := s.resolveModelKeepAlive(modelID, DefaultSpeechKeepAlive)
	log.Printf("MODEL %s: TTS engine load started", modelID)
	runtimeManager, err := imagegen.NewTTSRuntimeManager()
	if err == nil {
		err = ensureTTSRuntimeReady(ctx, runtimeManager, nil, false)
		if err == nil {
			state.engine, err = newTTSEngine(ctx, modelID, modelDir, runtimeManager)
		}
	}
	state.err = err

	s.mu.Lock()
	delete(s.ttsLoading, modelID)
	if state.err == nil {
		s.ttsEngines[modelID] = &managedTTSEngine{
			engine:    state.engine,
			lastUsed:  time.Now(),
			keepAlive: keepAlive,
		}
	}
	close(state.done)
	s.mu.Unlock()

	if state.err != nil {
		log.Printf("MODEL %s: TTS engine load failed: %v", modelID, state.err)
		return
	}
	log.Printf("MODEL %s: TTS engine load complete", modelID)
}

func (s *Server) closeTTSEngine(modelID string) {
	modelID = s.resolveLocalModelStorageID(modelID)
	s.mu.Lock()
	me, ok := s.ttsEngines[modelID]
	if ok {
		delete(s.ttsEngines, modelID)
	}
	s.mu.Unlock()
	if ok {
		_ = me.engine.Close()
	}
}

var ensureTTSRuntimeReady = func(ctx context.Context, runtimeManager *imagegen.RuntimeManager, progress imagegen.ProgressFunc, upgradePackages bool) error {
	if status := runtimeManager.TTSStatus(ctx); status.Ready && !upgradePackages {
		return nil
	}
	_, err := runtimeManager.InstallTTSWithProgressOptions(ctx, progress, upgradePackages)
	return err
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

// addRealtimeSocket adjusts the live WebSocket realtime session count.
func (s *Server) addRealtimeSocket(delta int) {
	s.mu.Lock()
	s.realtimeSockets += delta
	if s.realtimeSockets < 0 {
		s.realtimeSockets = 0
	}
	s.mu.Unlock()
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
	for id, me := range s.ttsEngines {
		me.engine.Close()
		delete(s.ttsEngines, id)
	}
	for id, me := range s.asrEngines {
		me.engine.Close()
		delete(s.asrEngines, id)
	}
}
