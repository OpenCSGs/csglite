package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/modelregistry"
	"github.com/opencsgs/csglite/pkg/api"
)

// Cluster environment variables. The cluster itself is configured through
// its own state file; these only shape how this process participates.
const (
	EnvClusterDisabled      = "CSGHUB_LITE_CLUSTER_DISABLED"
	EnvClusterAddr          = "CSGHUB_LITE_CLUSTER_ADDR"
	EnvClusterSeeds         = "CSGHUB_LITE_CLUSTER_SEEDS"
	EnvClusterJoinToken     = "CSGHUB_LITE_CLUSTER_JOIN_TOKEN"
	EnvClusterAdvertiseHost = "CSGHUB_LITE_CLUSTER_ADVERTISE_HOST"
	EnvClusterDiscovery     = "CSGHUB_LITE_CLUSTER_DISCOVERY"
)

// clusterStorageDir is where identity, membership and perf samples live.
const clusterStorageDir = "cluster"

// clusterHost adapts the server to what the cluster manager needs. It is the
// only place the Apache-licensed server hands capabilities to the EE code.
type clusterHost struct {
	s *Server
}

// clusterTestHooks lets tests run several servers in one process with an
// in-memory discovery bus and ephemeral ports. Nil in production.
var clusterTestHooks struct {
	discoverer func() cluster.Discoverer
	listenAddr string
}

// newClusterManager builds the manager; nil means the cluster is disabled
// for this process (env) or could not be initialised (logged).
func newClusterManager(s *Server, storageRoot string) *cluster.Manager {
	if envTruthy(os.Getenv(EnvClusterDisabled)) {
		return nil
	}
	if storageRoot == "" {
		return nil
	}
	var disc cluster.Discoverer
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvClusterDiscovery))) {
	case "none", "off", "static":
		disc = cluster.NopDiscoverer{}
	default:
		disc = cluster.NewMDNSDiscoverer()
	}
	if clusterTestHooks.discoverer != nil {
		disc = clusterTestHooks.discoverer()
	}
	var seeds []string
	for _, seed := range strings.Split(os.Getenv(EnvClusterSeeds), ",") {
		if seed = strings.TrimSpace(seed); seed != "" {
			seeds = append(seeds, seed)
		}
	}
	addr := strings.TrimSpace(os.Getenv(EnvClusterAddr))
	if addr == "" {
		addr = cluster.DefaultListenAddr
	}
	if clusterTestHooks.listenAddr != "" {
		addr = clusterTestHooks.listenAddr
	}
	m, err := cluster.New(cluster.Options{
		Dir:           filepath.Join(storageRoot, clusterStorageDir),
		Host:          &clusterHost{s: s},
		Discoverer:    disc,
		ListenAddr:    addr,
		Seeds:         seeds,
		JoinToken:     strings.TrimSpace(os.Getenv(EnvClusterJoinToken)),
		AdvertiseHost: strings.TrimSpace(os.Getenv(EnvClusterAdvertiseHost)),
		// The shared secret comes from config.json (installer or
		// `config set cluster_secret`), with CSGHUB_LITE_CLUSTER_SECRET
		// applied on top by config.ApplyEnvironmentDefaults.
		AutoFormSecret: strings.TrimSpace(s.cfg.Cluster.Secret),
		AutoFormName:   strings.TrimSpace(s.cfg.Cluster.Name),
		Logf:           log.Printf,
	})
	if err != nil {
		log.Printf("cluster: disabled: %v", err)
		return nil
	}
	return m
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// startCluster opens the peer listener once the API port is known.
func (s *Server) startCluster(ctx context.Context) {
	if s.cluster == nil {
		return
	}
	if err := s.cluster.Start(ctx); err != nil {
		log.Printf("cluster: %v; the cluster feature is unavailable in this process", err)
		s.cluster = nil
	}
}

func (s *Server) stopCluster() {
	if s.cluster != nil {
		s.cluster.Stop()
	}
}

// ---- cluster.Host ----

func (h *clusterHost) Version() string { return h.s.version }

func (h *clusterHost) Licensed() bool {
	return h.s.license != nil && h.s.license.State().Licensed()
}

func (h *clusterHost) NodeLimit() int {
	if h.s.license == nil {
		return license.QuotaMaxClusterNodes.CommunityValue
	}
	return h.s.license.Limit(license.QuotaMaxClusterNodes)
}

func (h *clusterHost) APIPort() int {
	addr := h.s.cfg.BoundAddr
	if addr == "" {
		addr = h.s.cfg.EffectiveListenAddr()
	}
	_, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

func (h *clusterHost) LocalEngine(ctx context.Context, modelID string, opts cluster.EngineOptions) (inference.Engine, error) {
	// The kind matters: an embedding request needs a process started in
	// embedding mode, and the llama.cpp cache and speculative options mean
	// nothing to it. Serving one from the chat engine reaches a llama-server
	// that was never given --embeddings.
	if opts.Kind == cluster.EngineEmbedding {
		return h.s.getOrLoadEmbeddingEngineWithOpts(ctx, modelID, opts.NumCtx, opts.NumParallel, opts.NGPULayers, opts.DType)
	}
	return h.s.getOrLoadEngineWithOpts(modelID, opts.NumCtx, opts.NumParallel, opts.NGPULayers, opts.CacheTypeK, opts.CacheTypeV, opts.DType)
}

// InferenceHandler serves a request forwarded by another member. The route
// source is pinned to "local" so the request can never be routed onward, and
// there is no API-key check: the mTLS client certificate already proved the
// caller is a paired member.
func (h *clusterHost) InferenceHandler() http.Handler {
	s := h.s
	mux := http.NewServeMux()
	local := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), providerRouteSourceContextKey{}, "local")
			handler(w, r.WithContext(ctx))
		}
	}
	mux.HandleFunc("POST /v1/chat/completions", local(s.handleOpenAIChatCompletions))
	mux.HandleFunc("POST /v1/embeddings", local(s.handleOpenAIEmbeddings))
	mux.HandleFunc("POST /v1/responses", local(s.handleOpenAIResponses))
	mux.HandleFunc("POST /v1/messages", local(s.handleAnthropicMessages))
	mux.HandleFunc("POST /v1/messages/count_tokens", local(s.handleAnthropicCountTokens))
	mux.HandleFunc("POST /api/chat", local(s.handleChat))
	mux.HandleFunc("POST /api/generate", local(s.handleGenerate))
	mux.HandleFunc("POST /v1/audio/transcriptions", local(s.handleOpenAIAudioTranscriptions))
	mux.HandleFunc("POST /v1/audio/speech", local(s.handleOpenAIAudioSpeech))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not a forwardable inference endpoint")
	})
	return correlationMiddleware(s.observabilityMiddleware(providerPoolUsageMiddleware(mux)))
}

func (h *clusterHost) PullHandler() http.Handler {
	return http.HandlerFunc(h.s.handlePullJobCreate)
}

// PullSpec splits a registry-prefixed public model id into the repository
// and artifact source a pull job expects. Ids without a known prefix are
// OpenCSG repositories and pass through unchanged.
func (h *clusterHost) PullSpec(modelID string) (string, string) {
	modelID = strings.TrimSpace(modelID)
	// A model this node holds answers the question exactly: an OpenCSG
	// model is advertised under its short name, which is not a repository.
	if lm, err := h.s.manager.ResolveLocalModel(modelID); err == nil && lm != nil {
		repo := strings.TrimSpace(lm.Repository)
		if repo == "" {
			repo = lm.Namespace + "/" + lm.Name
		}
		source := strings.TrimSpace(lm.ArtifactSource)
		if source == "opencsg" {
			source = ""
		}
		return repo, source
	}
	for _, source := range []modelregistry.Source{modelregistry.SourceHuggingFace, modelregistry.SourceModelScope} {
		prefix := string(source) + "/"
		if strings.HasPrefix(strings.ToLower(modelID), prefix) && strings.Count(modelID, "/") >= 2 {
			return modelID[len(prefix):], string(source)
		}
	}
	return modelID, ""
}

// LocalStatus reports this node's models, load and hardware.
func (h *clusterHost) LocalStatus(ctx context.Context) cluster.Status {
	s := h.s
	st := cluster.Status{Models: []cluster.ModelStatus{}}
	st.Hostname, _ = os.Hostname()
	if i := strings.IndexByte(st.Hostname, '.'); i > 0 {
		st.Hostname = st.Hostname[:i]
	}
	st.ModelSource = cluster.ModelSource{
		ServerURL:          strings.TrimSpace(s.cfg.ServerURL),
		HFEndpoint:         strings.TrimSpace(s.cfg.HuggingFaceEndpoint),
		ModelScopeEndpoint: strings.TrimSpace(s.cfg.ModelScopeEndpoint),
	}

	// Loaded engines, keyed by public model id. A model may run a chat and
	// an embedding engine at once; the chat engine's figures win where both
	// report the same field.
	type loaded struct {
		slots, active, ngl int
		expires            time.Time
		toolStreaming      bool
		loading            bool
	}
	engines := map[string]loaded{}
	s.mu.RLock()
	for key, me := range s.engines {
		modelKey := engineModelIDFromKey(key)
		public := s.localInferenceModelID(modelKey)
		cur, seen := engines[public]
		chatEngine := modelKey == key
		if !seen || chatEngine {
			cur.slots = me.numParallel
			cur.ngl = me.nGPULayers
			cur.toolStreaming = inference.SupportsNativeToolStreaming(me.engine)
			cur.expires = me.expiresAt()
		}
		cur.active += me.activeRequests
		cur.loading = false
		engines[public] = cur
		st.Inflight += me.activeRequests
	}
	for key := range s.loading {
		if _, ok := s.engines[key]; ok {
			continue
		}
		public := s.localInferenceModelID(engineModelIDFromKey(key))
		if _, ok := engines[public]; !ok {
			engines[public] = loaded{loading: true}
		}
	}
	// Python workers (speech recognition, synthesis, image generation) serve
	// one request at a time, so they count as a single slot.
	for id, me := range s.asrEngines {
		public := s.localInferenceModelID(id)
		engines[public] = loaded{slots: 1, active: me.activeRequests, ngl: -1, expires: workerExpiry(me.lastUsed, me.keepAlive)}
		st.Inflight += me.activeRequests
	}
	for id, me := range s.ttsEngines {
		public := s.localInferenceModelID(id)
		engines[public] = loaded{slots: 1, active: me.activeRequests, ngl: -1, expires: workerExpiry(me.lastUsed, me.keepAlive)}
		st.Inflight += me.activeRequests
	}
	for id, me := range s.imageEngines {
		public := s.localInferenceModelID(id)
		engines[public] = loaded{slots: 1, ngl: -1, expires: workerExpiry(me.lastUsed, me.keepAlive)}
	}
	for id := range s.asrLoading {
		if _, ok := s.asrEngines[id]; !ok {
			engines[s.localInferenceModelID(id)] = loaded{loading: true}
		}
	}
	for id := range s.ttsLoading {
		if _, ok := s.ttsEngines[id]; !ok {
			engines[s.localInferenceModelID(id)] = loaded{loading: true}
		}
	}
	for id := range s.imageLoading {
		if _, ok := s.imageEngines[id]; !ok {
			engines[s.localInferenceModelID(id)] = loaded{loading: true}
		}
	}
	s.mu.RUnlock()

	// Repository and artifact source per public id, so a peer can turn a
	// cluster model id back into a pull job.
	pullSpecs := map[string][2]string{}
	if locals, err := s.manager.List(); err == nil {
		for _, lm := range locals {
			if lm == nil {
				continue
			}
			repo := strings.TrimSpace(lm.Repository)
			if repo == "" {
				repo = lm.Namespace + "/" + lm.Name
			}
			source := strings.TrimSpace(lm.ArtifactSource)
			if source == "" {
				source = "opencsg"
			}
			pullSpecs[s.localInferenceModelID(lm.FullName())] = [2]string{repo, source}
		}
	}
	if infos, err := s.listLocalModelInfos(); err == nil {
		for _, info := range infos {
			ms := cluster.ModelStatus{ID: info.Model, Size: info.Size, Format: info.Format, PipelineTag: info.PipelineTag, Category: info.Category, NGPULayers: -1}
			if spec, ok := pullSpecs[info.Model]; ok {
				ms.Repo, ms.Source = spec[0], spec[1]
			}
			if l, ok := engines[info.Model]; ok {
				ms.Loaded = !l.loading
				ms.Loading = l.loading
				ms.Slots = l.slots
				ms.Active = l.active
				ms.NGPULayers = l.ngl
				ms.NativeToolStreaming = l.toolStreaming
				if !l.expires.IsZero() {
					t := l.expires
					ms.ExpiresAt = &t
				}
			}
			if d, ok := s.modelLoadDuration(info.Model); ok {
				ms.Perf = &cluster.ModelPerf{LoadSeconds: d.Seconds()}
			}
			st.Models = append(st.Models, ms)
		}
	}
	if s.pullJobs != nil {
		st.Jobs.Pulling = s.pullJobs.activeModelNames()
	}
	if st.Jobs.Pulling == nil {
		st.Jobs.Pulling = []string{}
	}
	st.Jobs.Converting = []string{}

	gpus, cpu, ram, disk := h.hardware()
	st.GPUs, st.CPU, st.RAM, st.Disk = gpus, cpu, ram, disk
	return st
}

// hardware collects GPU, CPU, RAM and disk figures. Each call shells out to
// nvidia-smi and friends, so it must not run per request: the manager caches
// the whole status it builds from this, and that is the only cache. A second
// one here used to hold these figures for two seconds of its own, which meant
// invalidating the status after a settings change still returned stale
// hardware from a cache the invalidation could not reach.
func (h *clusterHost) hardware() ([]cluster.GPUStatus, cluster.CPUStatus, cluster.RAMStatus, cluster.DiskStatus) {
	used, total, _ := getRAMInfo()
	ram := cluster.RAMStatus{Total: total, Used: used}
	gpus := clusterGPUStatuses(total)
	for _, g := range gpus {
		if g.Shared {
			ram.Unified = true
		}
	}
	cpu := cluster.CPUStatus{Cores: runtime.NumCPU()}
	if load, ok := cpuLoad1(); ok {
		cpu.Load1 = &load
	}
	if util, ok := cpuUtilQuick(); ok {
		cpu.Util = &util
	}
	disk := cluster.DiskStatus{Path: h.s.cfg.ModelDir}
	if dt, df, err := diskUsage(h.s.cfg.ModelDir); err == nil {
		disk.Total, disk.Free = dt, df
	}
	if h.s.pullJobs != nil && len(h.s.pullJobs.activeModelNames()) > 0 {
		disk.IOBusy = true
	}
	return gpus, cpu, ram, disk
}

// ---- routing hooks ----

// clusterRoutingWanted decides whether a request without a pinned source
// should go through the cluster router: always for models this node lacks
// but a peer holds, and for every model in balanced mode.
func (s *Server) clusterRoutingWanted(modelID string) bool {
	if s.cluster == nil || !s.cluster.Store().InCluster() {
		return false
	}
	// Any online member holding the model counts, even one that is
	// draining: the router then answers with a clear "no node available"
	// instead of a misleading "model not found locally".
	if !s.cluster.RemoteHasModel(modelID) {
		return false
	}
	if s.cluster.Store().Settings().RoutingMode == cluster.RoutingBalanced {
		return true
	}
	return !s.modelPresentLocally(modelID)
}

func (s *Server) modelPresentLocally(modelID string) bool {
	if s.manager == nil {
		return false
	}
	_, err := s.manager.ResolveLocalModel(strings.TrimSpace(modelID))
	return err == nil
}

// clusterRoutedEngine returns the cluster router for a request. kind travels
// with it so whichever node the scheduler picks loads the matching engine.
func (s *Server) clusterRoutedEngine(ctx context.Context, kind cluster.EngineKind, modelID, source string, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (inference.Engine, error) {
	if s.cluster == nil {
		return nil, inference.NewHTTPStatusError(http.StatusNotFound, "the cluster feature is disabled on this node")
	}
	if key := providerPoolUsageCaptureFromContext(ctx).affinityKey(); key != "" {
		ctx = cluster.WithAffinityKey(ctx, key)
	}
	return s.cluster.RoutedEngine(ctx, modelID, source, cluster.EngineOptions{
		Kind: kind, NumCtx: numCtx, NumParallel: numParallel, NGPULayers: nGPULayers, CacheTypeK: cacheTypeK, CacheTypeV: cacheTypeV, DType: dtype,
	})
}

// clusterModelInfos lists models that peers hold, for /v1/models and the UI,
// annotated with where each one lives. Models this node has locally are
// returned too so the caller can attach the node list to its own entry.
func (s *Server) clusterModelInfos(ctx context.Context) ([]api.ModelInfo, map[string][]api.ModelNodePresence) {
	if s.cluster == nil || !s.cluster.Store().InCluster() {
		return nil, nil
	}
	presence := map[string][]api.ModelNodePresence{}
	var remoteOnly []api.ModelInfo
	for _, cm := range s.cluster.Models(ctx) {
		local := false
		for _, n := range cm.Nodes {
			presence[cm.ID] = append(presence[cm.ID], api.ModelNodePresence{UUID: n.UUID, Name: n.Name, Loaded: n.Loaded, Online: n.Online, Local: n.Local})
			if n.Local {
				local = true
			}
		}
		if local {
			continue
		}
		category := cm.Category
		if category == "" {
			category = categoryForPipelineTag(cm.PipelineTag)
		}
		remoteOnly = append(remoteOnly, api.ModelInfo{
			Name: cm.ID, Model: cm.ID, Size: cm.Size, Format: cm.Format, Label: cm.ID, DisplayName: cm.ID,
			Source: cluster.SourceCluster, Provider: "cluster", Category: category, PipelineTag: cm.PipelineTag,
			Nodes: presence[cm.ID],
		})
	}
	return remoteOnly, presence
}

// clusterNodeHeaders are copied from a routed response to the caller.
var clusterNodeHeaders = []string{cluster.NodeHeader, cluster.NodeNameHeader}

// copyClusterNodeHeaders forwards the executing-node headers of a routed
// upstream response to the caller.
func copyClusterNodeHeaders(w http.ResponseWriter, upstream http.Header) {
	for _, header := range clusterNodeHeaders {
		if value := strings.TrimSpace(upstream.Get(header)); value != "" {
			w.Header().Set(header, value)
		}
	}
}

// ---- /api/cluster route helpers ----

// withCluster wraps a manager handler; without a manager the routes answer
// 503 so the UI can explain that the feature is off in this process.
func (s *Server) withCluster(fn func(*cluster.Manager) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cluster == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "the cluster feature is disabled on this node (" + EnvClusterDisabled + ")", "errorCode": http.StatusServiceUnavailable, "code": "cluster_disabled",
			})
			return
		}
		fn(s.cluster)(w, r)
	}
}

// handleClusterSummary is GET /api/cluster/summary; it answers in every
// edition, with an empty summary when the cluster is disabled.
func (s *Server) handleClusterSummary(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		writeJSON(w, http.StatusOK, cluster.Summary{Nodes: []cluster.SummaryNode{}, NodeLimit: license.QuotaMaxClusterNodes.CommunityValue})
		return
	}
	s.cluster.Admin().HandleSummary(w, r)
}

func workerExpiry(lastUsed time.Time, keepAlive time.Duration) time.Time {
	if keepAlive < 0 {
		return time.Time{}
	}
	return lastUsed.Add(keepAlive)
}

// modelLoadDuration reports how long the last load of a model took.
func (s *Server) modelLoadDuration(modelID string) (time.Duration, bool) {
	s.loadDurMu.Lock()
	defer s.loadDurMu.Unlock()
	d, ok := s.loadDurations[s.resolveLocalModelStorageID(modelID)]
	return d, ok
}

func (s *Server) recordModelLoadDuration(storageID string, d time.Duration) {
	if d <= 0 {
		return
	}
	s.loadDurMu.Lock()
	if s.loadDurations == nil {
		s.loadDurations = map[string]time.Duration{}
	}
	s.loadDurations[storageID] = d
	s.loadDurMu.Unlock()
}

// activeModelNames lists models with a running or queued pull job.
func (st *pullJobStore) activeModelNames() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []string
	for _, id := range st.activeKey {
		job := st.jobs[id]
		if job == nil {
			continue
		}
		job.mu.Lock()
		if job.kind == "model" && (job.status == pullJobRunning || job.status == pullJobQueued) {
			out = append(out, job.name)
		}
		job.mu.Unlock()
	}
	return out
}

// clusterErrorResponse renders a routing failure in the local API's shape.
func clusterErrorResponse(err error) (int, any) {
	status := cluster.StatusCodeForError(err)
	body := map[string]any{"error": inference.HTTPErrorMessage(err), "errorCode": status}
	var noNode *cluster.ErrNoNode
	if errors.As(err, &noNode) {
		body["code"] = cluster.ErrNoNodeCode
		body["tried"] = noNode.Tried
	}
	return status, body
}

// checkClusterRouteSource validates a /providers/{cluster|node:<uuid>} route
// before the handler runs, so an app bound to a machine that has left the
// cluster gets a clear 404 instead of a routing error deep in the request.
func (s *Server) checkClusterRouteSource(source string) error {
	if s.cluster == nil {
		return inference.NewHTTPStatusError(http.StatusNotFound, "the cluster feature is disabled on this node")
	}
	if !s.cluster.Store().InCluster() {
		return inference.NewHTTPStatusError(http.StatusNotFound, "this node is not part of a cluster")
	}
	uuid := cluster.NodeUUIDFromSource(source)
	if uuid == "" || uuid == s.cluster.Identity().UUID {
		return nil
	}
	if _, ok := s.cluster.Store().Member(uuid); !ok {
		return inference.NewHTTPStatusError(http.StatusNotFound, "node is not a member of this cluster")
	}
	return nil
}
