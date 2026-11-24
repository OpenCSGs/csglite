package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/localinference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/internal/modelregistry"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	marketplaceLocalInferenceMaxPer            = 50
	marketplaceLocalInferenceMinScanPer        = 64
	marketplaceLocalInferenceMaxScanPer        = 100
	marketplaceLocalInferenceScanPageLimit     = 50
	marketplaceLocalInferenceScanTimeout       = 8 * time.Second
	marketplaceLocalInferenceProbeBudget       = 120
	marketplaceLocalInferenceSizeBudget        = 80
	marketplaceLocalInferenceFileSizeBudget    = 16
	marketplaceLocalInferenceProbeConcurrency  = 24
	marketplaceLocalInferenceProbeTimeout      = 8 * time.Second
	marketplaceLocalInferenceAuthFailureLimit  = 3
	marketplaceLocalInferenceCacheLimit        = 2000
	marketplaceLocalInferenceCacheHitTTL       = 6 * time.Hour
	marketplaceLocalInferenceCacheMissTTL      = 5 * time.Minute
	marketplaceLocalInferenceCacheTransientTTL = 30 * time.Second
	marketplaceLocalInferenceSizeTimeout       = 4 * time.Second
	marketplaceLocalInferenceSizeUnknownTTL    = 2 * time.Minute
	marketplaceLocalInferenceConfigFileName    = "config.json"
	marketplaceLocalInferenceFlag              = "local_inference"
	// marketplaceLocalFilterEndpointTTL bounds how long Lite remembers that the
	// hub has no local-model-filter endpoint. A legacy hub then costs one failed
	// probe instead of one per request; a hub upgrade is picked up without a
	// restart after the TTL.
	marketplaceLocalFilterEndpointTTL = 20 * time.Minute
	// marketplaceLocalFilterTimeoutFirstCooldown parks the endpoint after the
	// first query_timeout. The hub reports a timeout only after its own budget
	// expires, so every page would otherwise pay that wait before the scan
	// fallback runs. A timeout after the cooldown expires means the environment
	// is still too slow, so later timeouts park it for the longer cooldown.
	marketplaceLocalFilterTimeoutFirstCooldown = 15 * time.Minute
	marketplaceLocalFilterTimeoutCooldown      = 30 * time.Minute
)

type marketplaceArchStatus int

const (
	marketplaceArchStatusHit marketplaceArchStatus = iota
	marketplaceArchStatusMiss
	marketplaceArchStatusTransient
)

type marketplaceArchEntry struct {
	architecture string
	writtenAt    time.Time
	status       marketplaceArchStatus
}

type marketplaceArchCache struct {
	mu      sync.Mutex
	entries map[string]marketplaceArchEntry
}

func newMarketplaceArchCache() *marketplaceArchCache {
	return &marketplaceArchCache{entries: make(map[string]marketplaceArchEntry)}
}

var marketplaceLocalInferenceArchCache = newMarketplaceArchCache()

// marketplaceRepoSizeEntry caches the repository size resolved from the file
// tree. The key carries updated_at, so a repository update invalidates it.
type marketplaceRepoSizeEntry struct {
	size        int64
	modelParams float64
	// lowerBound marks a tree walk that stopped once the total passed the
	// threshold, so size is a floor rather than the repository size. It stays
	// usable while it still exceeds the current threshold.
	lowerBound bool
	// unknown marks a size lookup that was attempted and did not produce a
	// value in time. It is cached with a short TTL so a pathological tree does
	// not cost the same deadline on every request.
	unknown   bool
	writtenAt time.Time
}

type marketplaceRepoSizeCache struct {
	mu      sync.Mutex
	entries map[string]marketplaceRepoSizeEntry
}

func newMarketplaceRepoSizeCache() *marketplaceRepoSizeCache {
	return &marketplaceRepoSizeCache{entries: make(map[string]marketplaceRepoSizeEntry)}
}

var marketplaceLocalInferenceRepoSizeCache = newMarketplaceRepoSizeCache()

func clearMarketplaceLocalInferenceCache() {
	marketplaceLocalInferenceArchCache.clear()
	marketplaceLocalInferenceRepoSizeCache.clear()
}

func marketplaceArchCacheTTL(status marketplaceArchStatus) time.Duration {
	switch status {
	case marketplaceArchStatusHit:
		return marketplaceLocalInferenceCacheHitTTL
	case marketplaceArchStatusTransient:
		return marketplaceLocalInferenceCacheTransientTTL
	default:
		return marketplaceLocalInferenceCacheMissTTL
	}
}

func (c *marketplaceArchCache) get(key string, now time.Time) (marketplaceArchEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return marketplaceArchEntry{}, false
	}
	if now.Sub(entry.writtenAt) > marketplaceArchCacheTTL(entry.status) {
		delete(c.entries, key)
		return marketplaceArchEntry{}, false
	}
	return entry, true
}

func (c *marketplaceArchCache) set(key string, entry marketplaceArchEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= marketplaceLocalInferenceCacheLimit {
		c.evictOldestLocked()
	}
	c.entries[key] = entry
}

// evictOldestLocked drops the entry with the oldest write time. This is a FIFO
// approximation, not LRU: reads do not refresh writtenAt.
func (c *marketplaceArchCache) evictOldestLocked() {
	oldestKey := ""
	var oldest time.Time
	for key, entry := range c.entries {
		if oldestKey == "" || entry.writtenAt.Before(oldest) {
			oldestKey = key
			oldest = entry.writtenAt
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func (c *marketplaceRepoSizeCache) get(key string, now time.Time) (marketplaceRepoSizeEntry, bool) {
	if key == "" {
		return marketplaceRepoSizeEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return marketplaceRepoSizeEntry{}, false
	}
	ttl := marketplaceLocalInferenceCacheHitTTL
	if entry.unknown {
		ttl = marketplaceLocalInferenceSizeUnknownTTL
	}
	if now.Sub(entry.writtenAt) >= ttl {
		delete(c.entries, key)
		return marketplaceRepoSizeEntry{}, false
	}
	return entry, true
}

// setUnknown records a failed size lookup so the next requests skip the same
// slow repository for a short while.
func (c *marketplaceRepoSizeCache) setUnknown(key string, now time.Time) {
	if key == "" {
		return
	}
	c.set(key, marketplaceRepoSizeEntry{unknown: true}, now)
}

func (c *marketplaceRepoSizeCache) set(key string, entry marketplaceRepoSizeEntry, now time.Time) {
	if key == "" || (!entry.unknown && entry.size <= 0 && entry.modelParams <= 0) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= marketplaceLocalInferenceCacheLimit {
		var oldestKey string
		var oldest time.Time
		for entryKey, entry := range c.entries {
			if oldestKey == "" || entry.writtenAt.Before(oldest) {
				oldestKey, oldest = entryKey, entry.writtenAt
			}
		}
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	entry.writtenAt = now
	c.entries[key] = entry
}

func (c *marketplaceRepoSizeCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]marketplaceRepoSizeEntry)
}

func (c *marketplaceArchCache) clear() {
	c.mu.Lock()
	c.entries = make(map[string]marketplaceArchEntry)
	c.mu.Unlock()
}

type marketplaceLocalInferenceResult struct {
	Models     []csghub.Model
	Total      int
	TotalExact bool
	// Partial asks the UI to say the page is incomplete. It is set for a
	// truncated candidate scan or a degraded probe, not for a candidate timeout:
	// a timeout leaves the confirmed page intact, so the user must not see an
	// "incomplete result" banner.
	Partial bool
	// Uncacheable marks a response that must not be reused by the short-lived
	// list cache (a candidate timeout would otherwise be pinned for two
	// minutes). It is independent from Partial, which is user-visible.
	Uncacheable bool
	Degraded    bool
	// PageLimitExceeded reports that the hub refused to page past its offset
	// limit; the UI asks the user to narrow the filters instead of showing an
	// empty page as "no more results".
	PageLimitExceeded bool

	// HasMore is computed by the hub endpoint for the local-model-filter path and
	// by the handler (page fullness and total) for the scan path.
	HasMore bool
	// ExcludePaths carries the candidate cursor back to the caller when the hub
	// endpoint served the page.
	ExcludePaths []string
}

type marketplaceLocalInferenceFilter struct {
	registry            modelregistry.Registry
	cache               *marketplaceArchCache
	now                 func() time.Time
	budgetLimit         int
	budget              int
	sizeBudgetLimit     int
	sizeBudget          int
	fileSizeBudgetLimit int
	fileSizeBudget      int
	concurrency         int
	probeTimeout        time.Duration
	scanPageLimit       int
	scanTimeout         time.Duration
	sizeTimeout         time.Duration
	limits              marketplaceLocalInferenceLimits
	degraded            atomic.Bool
	authFailures        atomic.Int32
	authSuccesses       atomic.Int32

	// scanArch memoises probe results for the current scan so the resolve pass
	// never repeats a read that was deliberately kept out of the shared cache
	// (cancelled reads, for example).
	scanMu   sync.Mutex
	scanArch map[string]marketplaceArchEntry

	// endpointUnavailable and markEndpointUnavailable share the per-process
	// "hub has no local_filter endpoint" memo with the Server.
	endpointUnavailable     func() bool
	markEndpointUnavailable func()

	// timeoutCooldown, markTimeout and clearTimeout share the per-process
	// "hub reported query_timeout" memo with the Server. While the cooldown is
	// active the scan fallback runs without paying for another timed-out round
	// trip.
	timeoutCooldown func() bool
	markTimeout     func()
	clearTimeout    func()
}

func newMarketplaceLocalInferenceFilter(registry modelregistry.Registry) *marketplaceLocalInferenceFilter {
	return &marketplaceLocalInferenceFilter{
		registry:            registry,
		cache:               marketplaceLocalInferenceArchCache,
		now:                 time.Now,
		budgetLimit:         marketplaceLocalInferenceProbeBudget,
		budget:              marketplaceLocalInferenceProbeBudget,
		sizeBudgetLimit:     marketplaceLocalInferenceSizeBudget,
		sizeBudget:          marketplaceLocalInferenceSizeBudget,
		fileSizeBudgetLimit: marketplaceLocalInferenceFileSizeBudget,
		fileSizeBudget:      marketplaceLocalInferenceFileSizeBudget,
		concurrency:         marketplaceLocalInferenceProbeConcurrency,
		probeTimeout:        marketplaceLocalInferenceProbeTimeout,
		scanPageLimit:       marketplaceLocalInferenceScanPageLimit,
		scanTimeout:         marketplaceLocalInferenceScanTimeout,
		sizeTimeout:         marketplaceLocalInferenceSizeTimeout,
	}
}

func (f *marketplaceLocalInferenceFilter) normalize() {
	if f.cache == nil {
		f.cache = marketplaceLocalInferenceArchCache
	}
	if f.now == nil {
		f.now = time.Now
	}
	if f.concurrency <= 0 {
		f.concurrency = marketplaceLocalInferenceProbeConcurrency
	}
	if f.probeTimeout <= 0 {
		f.probeTimeout = marketplaceLocalInferenceProbeTimeout
	}
	if f.scanPageLimit <= 0 {
		f.scanPageLimit = marketplaceLocalInferenceScanPageLimit
	}
	if f.scanTimeout <= 0 {
		f.scanTimeout = marketplaceLocalInferenceScanTimeout
	}
	if f.sizeTimeout <= 0 {
		f.sizeTimeout = marketplaceLocalInferenceSizeTimeout
	}
	if f.budgetLimit < 0 {
		f.budgetLimit = 0
	}
	if f.sizeBudgetLimit < 0 {
		f.sizeBudgetLimit = 0
	}
	if f.fileSizeBudgetLimit < 0 {
		f.fileSizeBudgetLimit = 0
	}
}

// marketplaceLocalInferenceScan holds the per-request scan accumulators so the
// scan loop and its helpers do not pass a dozen variables around.
type marketplaceLocalInferenceScan struct {
	per     int
	offset  int
	scanPer int

	items         []csghub.Model
	matched       int
	received      int
	observedPer   int
	upstreamTotal int
	exhausted     bool

	// deadline bounds the scan pages. Each page gets its own file-tree deadline
	// in scanBatch: one deadline for the whole scan let a slow tree on the first
	// page leave every later page with an unknown size, and an unknown size
	// stays allowed.
	deadline time.Time

	// probeCtx covers the probe phase only: it starts right before the first
	// batch of probes, so slow upstream listing does not eat into it.
	probeCtx     context.Context
	probeStarted bool
	probeCancel  context.CancelFunc
}

func (s *marketplaceLocalInferenceScan) startProbe(ctx context.Context, timeout time.Duration, probeModels []csghub.Model) {
	if len(probeModels) == 0 || s.probeStarted {
		return
	}
	s.probeCtx, s.probeCancel = context.WithTimeout(ctx, timeout)
	s.probeStarted = true
}

func (s *marketplaceLocalInferenceScan) stopProbe() {
	if s.probeCancel != nil {
		s.probeCancel()
	}
}

func (s *marketplaceLocalInferenceScan) appendMatches(candidates []marketplaceLocalInferenceCandidate, limits marketplaceLocalInferenceLimits) {
	for _, candidate := range candidates {
		if !marketplaceLocalInferenceSizeAllowed(candidate.support, candidate.model, limits) {
			continue
		}
		if s.matched >= s.offset && len(s.items) < s.per {
			s.items = append(s.items, candidate.model)
		}
		s.matched++
	}
}

func (s *marketplaceLocalInferenceScan) result(degraded bool) marketplaceLocalInferenceResult {
	exact := s.exhausted && !degraded
	result := marketplaceLocalInferenceResult{
		Models:     s.items,
		TotalExact: exact,
		Degraded:   degraded,
	}
	if exact {
		// Exhausted means every upstream model was scanned, so the number of
		// matches is exact even when the requested page is past the last match.
		result.Total = s.matched
	} else {
		// A degraded scan may have skipped runnable models, so its total is a
		// lower bound even when the upstream list was fully consumed.
		result.Total = s.offset + len(s.items)
		if len(s.items) >= s.per {
			result.Total++
		}
	}
	result.Partial = len(s.items) < s.per && (degraded || !s.exhausted)
	return result
}

// list scans the upstream list, filters it down to models this machine can run
// locally and pages the survivors in memory. The upstream always starts at page
// 1 because the local filter is applied after retrieval.
func (f *marketplaceLocalInferenceFilter) list(ctx context.Context, opts modelregistry.ListOptions) (marketplaceLocalInferenceResult, error) {
	f.resetScanState()
	scan := f.newScan(ctx, opts)
	defer scan.stopProbe()

	scan.deadline = time.Now().Add(f.scanTimeout)

	for scanPage := 1; ; scanPage++ {
		stop, err := f.scanBatch(ctx, scan, opts, scanPage)
		if err != nil {
			return marketplaceLocalInferenceResult{}, err
		}
		if stop {
			break
		}
	}
	return scan.result(f.degraded.Load()), nil
}

// resetScanState clears the per-scan state. The filter is created per request in
// the handler, but tests reuse one filter across calls, so each call must start
// from the same state.
func (f *marketplaceLocalInferenceFilter) resetScanState() {
	f.normalize()
	f.degraded.Store(false)
	f.authFailures.Store(0)
	f.authSuccesses.Store(0)
	f.budget = f.budgetLimit
	f.sizeBudget = f.sizeBudgetLimit
	f.fileSizeBudget = f.fileSizeBudgetLimit
	f.scanMu.Lock()
	f.scanArch = make(map[string]marketplaceArchEntry)
	f.scanMu.Unlock()
}

func (f *marketplaceLocalInferenceFilter) newScan(ctx context.Context, opts modelregistry.ListOptions) *marketplaceLocalInferenceScan {
	page := opts.Page
	if page <= 0 {
		page = 1
	}
	per := opts.PerPage
	if per <= 0 {
		per = 16
	}
	return &marketplaceLocalInferenceScan{
		per:      per,
		offset:   (page - 1) * per,
		scanPer:  marketplaceLocalInferenceScanPer(per),
		items:    make([]csghub.Model, 0, per),
		probeCtx: ctx,
	}
}

func marketplaceLocalInferenceScanPer(per int) int {
	scanPer := per * 4
	if scanPer < marketplaceLocalInferenceMinScanPer {
		scanPer = marketplaceLocalInferenceMinScanPer
	}
	if scanPer > marketplaceLocalInferenceMaxScanPer {
		scanPer = marketplaceLocalInferenceMaxScanPer
	}
	return scanPer
}

// scanBatch fetches one upstream page and keeps the models this machine can run.
// It reports whether the scan should stop; only the first page returns an error.
func (f *marketplaceLocalInferenceFilter) scanBatch(
	ctx context.Context,
	scan *marketplaceLocalInferenceScan,
	opts modelregistry.ListOptions,
	scanPage int,
) (bool, error) {
	if f.scanValveReached(scan, scanPage) {
		return true, nil
	}
	batch, total, err := f.registry.ListModels(ctx, modelregistry.ListOptions{
		Search:         opts.Search,
		Sort:           opts.Sort,
		Page:           scanPage,
		PerPage:        scan.scanPer,
		Framework:      opts.Framework,
		Task:           opts.Task,
		UpstreamSource: opts.UpstreamSource,
		ModelParamsMin: opts.ModelParamsMin,
		ModelParamsMax: opts.ModelParamsMax,
	})
	if err != nil {
		if scanPage == 1 {
			return true, err
		}
		f.degraded.Store(true)
		return true, nil
	}
	scan.upstreamTotal = total
	scan.received += len(batch)
	scan.observedPer = max(scan.observedPer, len(batch))
	if len(batch) == 0 {
		scan.exhausted = true
		return true, nil
	}

	planned, probeModels, budgetExhausted := f.planBatch(batch, opts.Framework)
	scan.startProbe(ctx, f.probeTimeout, probeModels)
	f.probe(scan.probeCtx, probeModels)

	candidates := f.candidatesFor(scan.probeCtx, planned)
	// Record the probe verdict before the size phase: size lookups are bounded
	// by their own deadline and must not be mistaken for a probe timeout when
	// they overrun the shared probe context.
	probeFailed := scan.probeCtx.Err() != nil
	// File-tree lookups get a deadline per page, capped by the scan deadline. A
	// shared scan-wide deadline meant a slow tree on the first page left later
	// pages with an unknown size, and an unknown size stays allowed, so the
	// oversized model reached the list.
	fileSizeDeadline := time.Now().Add(f.sizeTimeout)
	if fileSizeDeadline.After(scan.deadline) {
		fileSizeDeadline = scan.deadline
	}
	fileSizeCtx, fileSizeCancel := context.WithDeadline(ctx, fileSizeDeadline)
	defer fileSizeCancel()
	if err := f.fillCandidateSizes(ctx, fileSizeCtx, candidates); err != nil {
		log.Printf("MARKETPLACE LOCAL INFERENCE: repo sizes unavailable: %v", err)
	}
	scan.appendMatches(candidates, f.limits)

	return f.scanStopReason(scan, scanPage, batch, budgetExhausted, probeFailed), nil
}

// scanValveReached applies the page and time valves. The scan keeps filling the
// requested page until the upstream runs out, the page is full, or a valve
// trips. Both valves only mean "results may be incomplete" when the page is
// still short; a full page is a complete answer for this request.
func (f *marketplaceLocalInferenceFilter) scanValveReached(scan *marketplaceLocalInferenceScan, scanPage int) bool {
	if scanPage > f.scanPageLimit {
		if len(scan.items) < scan.per {
			f.degraded.Store(true)
			log.Printf("MARKETPLACE LOCAL INFERENCE: scan page limit reached (limit %d); returning partial results", f.scanPageLimit)
		}
		return true
	}
	if scanPage > 1 && time.Now().After(scan.deadline) {
		if len(scan.items) < scan.per {
			f.degraded.Store(true)
			log.Printf("MARKETPLACE LOCAL INFERENCE: scan time budget exhausted (limit %s); returning partial results", f.scanTimeout)
		}
		return true
	}
	return false
}

// planBatch reserves probe budget in batch order so the same request always
// probes the same models regardless of goroutine scheduling. The upstream
// framework filter is not reliable for every registry, so it is applied locally
// too: a model that does not match the user's framework must stay out of the
// budget and the result.
func (f *marketplaceLocalInferenceFilter) planBatch(batch []csghub.Model, framework string) (planned, probeModels []csghub.Model, budgetExhausted bool) {
	planned = make([]csghub.Model, 0, len(batch))
	probeModels = make([]csghub.Model, 0, len(batch))
	for _, m := range batch {
		if !marketplaceModelMatchesFrameworkFilter(m.Tags, framework) {
			continue
		}
		if f.needsProbe(m) {
			if _, ok := f.cachedEntry(m); !ok {
				if f.budget <= 0 {
					budgetExhausted = true
					break
				}
				f.budget--
				probeModels = append(probeModels, m)
			}
		}
		planned = append(planned, m)
	}
	if budgetExhausted {
		f.degraded.Store(true)
		log.Printf("MARKETPLACE LOCAL INFERENCE: probe budget exhausted (limit %d); returning partial results", f.budgetLimit)
	}
	return planned, probeModels, budgetExhausted
}

// candidatesFor resolves support for every planned model. Sizes only matter for
// supported non-GGUF models, so resolving support first keeps the size requests
// bounded to the candidates whose verdict can actually change.
func (f *marketplaceLocalInferenceFilter) candidatesFor(ctx context.Context, planned []csghub.Model) []marketplaceLocalInferenceCandidate {
	candidates := make([]marketplaceLocalInferenceCandidate, 0, len(planned))
	for _, m := range planned {
		support, ok, definitive := f.resolveSupport(ctx, m)
		if !definitive {
			f.degraded.Store(true)
			log.Printf("MARKETPLACE LOCAL INFERENCE: architecture probe unavailable for %s; returning partial results", m.Path)
		}
		if !ok {
			continue
		}
		candidates = append(candidates, marketplaceLocalInferenceCandidate{model: m, support: support})
	}
	return candidates
}

// scanStopReason applies the batch-boundary stop rules and reports whether the
// scan should end. Auth failures only stop the scan when nothing was readable at
// all: a single private repository can answer 401 while the rest of the listing
// is fine. Deciding at batch boundaries keeps the outcome deterministic instead
// of cancelling from whichever goroutine loses the race.
func (f *marketplaceLocalInferenceFilter) scanStopReason(scan *marketplaceLocalInferenceScan, scanPage int, batch []csghub.Model, budgetExhausted, probeFailed bool) bool {
	if budgetExhausted {
		return true
	}
	if probeFailed {
		f.degraded.Store(true)
		log.Printf("MARKETPLACE LOCAL INFERENCE: probe phase stopped on page %d (deadline %s); returning partial results", scanPage, f.probeTimeout)
		return true
	}
	if f.authFailures.Load() >= marketplaceLocalInferenceAuthFailureLimit && f.authSuccesses.Load() == 0 {
		f.degraded.Store(true)
		log.Printf("MARKETPLACE LOCAL INFERENCE: probe phase stopped on page %d after %d auth failures; returning partial results", scanPage, f.authFailures.Load())
		return true
	}
	// Prefer the cumulative count when the upstream reports a total: it tolerates
	// upstreams that clamp the page size. Without a total, only a batch smaller
	// than the largest observed batch means exhaustion, so the first page is
	// never mistaken for the end.
	scan.exhausted = (scan.upstreamTotal > 0 && scan.received >= scan.upstreamTotal) ||
		(scan.upstreamTotal <= 0 && scan.observedPer > 0 && len(batch) < scan.observedPer)
	return scan.exhausted || len(scan.items) >= scan.per
}

// marketplaceLocalInferenceDecision is the per-model verdict shared by the
// budget planner and the final filter, so both walk exactly the same rules.
type marketplaceLocalInferenceDecision struct {
	format    string
	task      string
	repo      string
	arch      string
	exclude   bool
	needsArch bool
}

func marketplaceDecideLocalInference(m csghub.Model) marketplaceLocalInferenceDecision {
	d := marketplaceLocalInferenceDecision{
		format: marketplaceModelListFormat(m.Tags),
		task:   marketplaceRoutedTaskTag(m.Tags),
		repo:   marketplaceModelRepoName(m),
		arch:   strings.TrimSpace(m.Metadata.Architecture),
	}
	if d.format == "" && marketplaceModelHasUnsupportedFormat(m.Tags) {
		d.exclude = true
		return d
	}
	if marketplaceVideoTask(d.task) {
		d.exclude = true
		return d
	}
	if d.format == string(model.FormatGGUF) && marketplaceLooksLikeNonTextGGUF(d.repo) {
		d.exclude = true
		return d
	}
	// GGUF repositories are exempt: a quantized repository name there describes
	// the quantization of the GGUF files, which llama.cpp can load directly.
	if d.format != string(model.FormatGGUF) && marketplaceLooksLikeQuantizedWeights(marketplaceModelFullName(m)) {
		d.exclude = true
		return d
	}
	if d.arch == "" {
		d.needsArch = marketplaceNeedsArchitecture(d.format, d.task, m, marketplaceIsTTSModel(d.task, d.repo))
	}
	return d
}

func (f *marketplaceLocalInferenceFilter) needsProbe(m csghub.Model) bool {
	d := marketplaceDecideLocalInference(m)
	return !d.exclude && d.needsArch
}

type marketplaceLocalInferenceCandidate struct {
	model   csghub.Model
	support api.LocalInferenceSupport
}

// resolveSupport applies every verdict rule except the resource gate, which
// runs after sizes are resolved for the candidates. The third result reports
// whether the verdict is definitive: a transient architecture probe failure
// answers "not supported" for now, but must not be treated as a final verdict
// (it must not consume a candidate cursor entry).
func (f *marketplaceLocalInferenceFilter) resolveSupport(ctx context.Context, m csghub.Model) (api.LocalInferenceSupport, bool, bool) {
	d := marketplaceDecideLocalInference(m)
	if d.exclude {
		return api.LocalInferenceSupport{}, false, true
	}
	arch := d.arch
	definitive := true
	if arch == "" && d.needsArch {
		var status marketplaceArchStatus
		arch, status = f.architecture(ctx, m)
		definitive = status != marketplaceArchStatusTransient
	}
	support := localinference.FromMarketplaceModel(
		d.format,
		arch,
		m.Metadata.ClassName,
		m.Path,
		marketplaceLibraryName(&m),
		d.task,
	)
	return support, support.Supported, definitive
}

// marketplaceLocalInferenceSizeAllowed reports whether a supported model fits
// this machine. GGUF ("direct") models are exempt: a repository usually ships
// several quantizations and only the selected files are downloaded, so the
// repository size is not the size that has to fit. An unknown size stays
// allowed so a failed probe never hides runnable models.
func marketplaceLocalInferenceSizeAllowed(support api.LocalInferenceSupport, m csghub.Model, limits marketplaceLocalInferenceLimits) bool {
	if !support.Supported {
		return false
	}
	if support.Mode == "direct" {
		return true
	}
	size := marketplaceLocalInferenceModelBytes(m)
	if size <= 0 {
		return true
	}
	if limits.maxMemoryBytes > 0 && size > limits.maxMemoryBytes {
		return false
	}
	if limits.maxDownloadBytes > 0 {
		needed := size
		if support.Mode == "convert" {
			// The snapshot is downloaded first and the converted GGUF is written
			// next to it, so both copies have to fit on disk.
			needed *= 2
		}
		if needed > limits.maxDownloadBytes {
			return false
		}
	}
	return true
}

func marketplaceLocalInferenceModelBytes(m csghub.Model) int64 {
	if m.RepoSize > 0 {
		return m.RepoSize
	}
	if params := m.Metadata.ValidModelParams(); params > 0 {
		// The list reports parameters instead of bytes for some models; two
		// bytes per parameter is the bf16 upper bound for the snapshot.
		return int64(params * 2e9)
	}
	return 0
}

// fillRepoSizes resolves sizes through the registry. OpenCSG answers in one
// batch call, so callers pass only the models whose verdict depends on the
// size.
func (f *marketplaceLocalInferenceFilter) fillRepoSizes(ctx context.Context, models []csghub.Model) error {
	filler, ok := f.registry.(modelregistry.RepoSizeFiller)
	if !ok || len(models) == 0 {
		return nil
	}
	return filler.FillRepoSizes(ctx, models)
}

// fillCandidateSizes resolves the repository size for supported non-GGUF
// candidates whose size is still unknown. Cached sizes and parameter estimates
// skip the request entirely; the per-scan budget bounds the worst case. ctx
// carries the request deadline for the batch lookup, fileSizeCtx bounds the
// expensive tree fallback for this page.
func (f *marketplaceLocalInferenceFilter) fillCandidateSizes(ctx, fileSizeCtx context.Context, candidates []marketplaceLocalInferenceCandidate) error {
	// Without a limit the sizes are not read, so do not spend upstream calls at
	// all (cluster mode always lands here).
	if f.limits.maxDownloadBytes == 0 && f.limits.maxMemoryBytes == 0 {
		return nil
	}
	now := f.now()
	indexes := make([]int, 0, len(candidates))
	models := make([]csghub.Model, 0, len(candidates))
	for i := range candidates {
		// GGUF is exempt from the gate, and a known size already lets the gate
		// decide. Parameter counts are only a fallback: they often cover one
		// component (or one precision) while the download contains variants, so
		// the actual repository size is what the gate must use.
		if candidates[i].support.Mode == "direct" || candidates[i].model.RepoSize > 0 {
			continue
		}
		indexes = append(indexes, i)
		models = append(models, candidates[i].model)
	}
	if len(models) == 0 {
		return nil
	}

	misses := models[:0]
	missIndexes := indexes[:0]
	for k, m := range models {
		key := marketplaceLocalInferenceCacheKey(f.registry.Source(), m)
		if entry, ok := marketplaceLocalInferenceRepoSizeCache.get(key, now); ok {
			if entry.unknown {
				continue
			}
			// A lower bound only answers the gate while it still exceeds the
			// current threshold; otherwise the tree walk has to continue.
			threshold := marketplaceLocalInferenceSizeThreshold(f.limits, candidates[indexes[k]].support.Mode)
			if !entry.lowerBound || entry.size > threshold {
				applyMarketplaceRepoSize(&candidates[indexes[k]].model, entry)
				continue
			}
		}
		misses = append(misses, m)
		missIndexes = append(missIndexes, indexes[k])
	}
	if len(misses) == 0 {
		return nil
	}
	if f.sizeBudget <= 0 {
		log.Printf("MARKETPLACE LOCAL INFERENCE: repo size budget exhausted (limit %d); keeping sizes unknown", f.sizeBudgetLimit)
		return nil
	}
	if len(misses) > f.sizeBudget {
		misses = misses[:f.sizeBudget]
		missIndexes = missIndexes[:f.sizeBudget]
		log.Printf("MARKETPLACE LOCAL INFERENCE: repo size budget exhausted (limit %d); keeping remaining sizes unknown", f.sizeBudgetLimit)
	}
	// The batch call is cheap: one request answers the whole page. It gets a
	// fresh deadline instead of the scan-wide file-tree one, because sharing
	// that deadline let a slow tree lookup on an earlier page skip the batch
	// here and leave later models with an unknown size, which the gate allows.
	extrasCtx, extrasCancel := context.WithTimeout(ctx, f.sizeTimeout)
	defer extrasCancel()
	if extrasCtx.Err() != nil {
		return nil
	}
	f.sizeBudget -= len(misses)
	err := f.fillRepoSizes(extrasCtx, misses)
	for k, m := range misses {
		params := m.Metadata.ValidModelParams()
		if m.RepoSize <= 0 && params <= 0 {
			continue
		}
		entry := marketplaceRepoSizeEntry{size: m.RepoSize, modelParams: params}
		applyMarketplaceRepoSize(&candidates[missIndexes[k]].model, entry)
		marketplaceLocalInferenceRepoSizeCache.set(marketplaceLocalInferenceCacheKey(f.registry.Source(), m), entry, now)
	}
	// A deadline hit here means the batch call never resolved these sizes.
	// Remember them briefly so the next request does not pay the same wait.
	if extrasCtx.Err() != nil {
		f.markRepoSizesUnknown(misses, now)
		return err
	}
	f.fillCandidateFileSizes(fileSizeCtx, candidates, misses, missIndexes, now)
	return err
}

// marketplaceLocalInferenceSizeThreshold returns the smallest repository size
// the resource gate rejects for a model with this mode. Converted models need
// room for the snapshot and the converted copy, and the memory limit applies to
// the snapshot alone; a size above every threshold is rejected whichever limit
// binds.
func marketplaceLocalInferenceSizeThreshold(limits marketplaceLocalInferenceLimits, mode string) int64 {
	var threshold int64
	if limits.maxMemoryBytes > 0 {
		threshold = limits.maxMemoryBytes
	}
	if limits.maxDownloadBytes > 0 {
		disk := limits.maxDownloadBytes
		if mode == "convert" {
			// The snapshot and the converted copy both have to fit.
			disk /= 2
		}
		if threshold == 0 || disk < threshold {
			threshold = disk
		}
	}
	return threshold
}

// fillCandidateFileSizes resolves the sizes that the batch metadata could not
// answer. Large OpenCSG mirrors often report neither a size nor a parameter
// count, and the detail page sizes them from the file tree, so the list has to
// use the same source or it keeps promising models that do not fit. The budget
// bounds the extra tree requests; models beyond it stay unknown and are allowed,
// which matches the fallback for a failed probe.
func (f *marketplaceLocalInferenceFilter) fillCandidateFileSizes(ctx context.Context, candidates []marketplaceLocalInferenceCandidate, models []csghub.Model, indexes []int, now time.Time) {
	filler, ok := f.registry.(modelregistry.FileSizeFiller)
	if !ok || f.fileSizeBudget <= 0 {
		return
	}
	misses := make([]csghub.Model, 0, len(models))
	missIndexes := make([]int, 0, len(indexes))
	for k, m := range models {
		if m.RepoSize > 0 || m.Metadata.ValidModelParams() > 0 {
			continue
		}
		misses = append(misses, m)
		missIndexes = append(missIndexes, indexes[k])
	}
	if len(misses) == 0 {
		return
	}
	if len(misses) > f.fileSizeBudget {
		misses = misses[:f.fileSizeBudget]
		missIndexes = missIndexes[:f.fileSizeBudget]
		log.Printf("MARKETPLACE LOCAL INFERENCE: file size budget exhausted (limit %d); keeping remaining sizes unknown", f.fileSizeBudgetLimit)
	}
	f.fileSizeBudget -= len(misses)

	// The gate rejects anything above the threshold for the model's mode, so a
	// walk that passes it can stop early; the size it returns is then a lower
	// bound that still fails the gate. Models of different modes have different
	// thresholds, so they are walked in groups.
	thresholds := make([]int64, len(misses))
	groups := make(map[int64][]int, 2)
	order := make([]int64, 0, 2)
	for k := range misses {
		threshold := marketplaceLocalInferenceSizeThreshold(f.limits, candidates[missIndexes[k]].support.Mode)
		thresholds[k] = threshold
		if _, seen := groups[threshold]; !seen {
			order = append(order, threshold)
		}
		groups[threshold] = append(groups[threshold], k)
	}
	for _, threshold := range order {
		members := groups[threshold]
		batch := make([]csghub.Model, len(members))
		for i, k := range members {
			batch[i] = misses[k]
		}
		if err := filler.FillFileSizes(ctx, batch, threshold); err != nil {
			log.Printf("MARKETPLACE LOCAL INFERENCE: file sizes unavailable: %v", err)
		}
		for i, k := range members {
			misses[k] = batch[i]
		}
	}

	for k, m := range misses {
		if m.RepoSize <= 0 {
			if m.Metadata.ValidModelParams() <= 0 {
				f.markRepoSizeUnknown(m, now)
			}
			continue
		}
		entry := marketplaceRepoSizeEntry{size: m.RepoSize, lowerBound: thresholds[k] > 0 && m.RepoSize > thresholds[k]}
		applyMarketplaceRepoSize(&candidates[missIndexes[k]].model, entry)
		marketplaceLocalInferenceRepoSizeCache.set(marketplaceLocalInferenceCacheKey(f.registry.Source(), m), entry, now)
	}
}

// markRepoSizesUnknown remembers models whose size could not be resolved, so
// the next requests do not repeat the same slow lookups.
func (f *marketplaceLocalInferenceFilter) markRepoSizesUnknown(models []csghub.Model, now time.Time) {
	for _, m := range models {
		if m.RepoSize > 0 || m.Metadata.ValidModelParams() > 0 {
			continue
		}
		f.markRepoSizeUnknown(m, now)
	}
}

func (f *marketplaceLocalInferenceFilter) markRepoSizeUnknown(m csghub.Model, now time.Time) {
	marketplaceLocalInferenceRepoSizeCache.setUnknown(marketplaceLocalInferenceCacheKey(f.registry.Source(), m), now)
}

// applyMarketplaceRepoSize copies a cached size or parameter fallback onto a
// model without overwriting information the list already provided.
func applyMarketplaceRepoSize(m *csghub.Model, entry marketplaceRepoSizeEntry) {
	if entry.size > 0 {
		m.RepoSize = entry.size
	}
	if entry.modelParams > 0 && m.Metadata.ValidModelParams() <= 0 {
		m.Metadata.ModelParams = entry.modelParams
		valid := true
		m.Metadata.ModelParamsValid = &valid
	}
}

func (f *marketplaceLocalInferenceFilter) probe(ctx context.Context, models []csghub.Model) {
	if len(models) == 0 {
		return
	}
	sem := make(chan struct{}, f.concurrency)
	var wg sync.WaitGroup
	for _, m := range models {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(m csghub.Model) {
			defer wg.Done()
			defer func() { <-sem }()
			f.architecture(ctx, m)
		}(m)
	}
	wg.Wait()
}

func (f *marketplaceLocalInferenceFilter) architecture(ctx context.Context, m csghub.Model) (string, marketplaceArchStatus) {
	// An empty repo id cannot be memoised: every such model would share one key.
	repo := strings.TrimSpace(m.Path)
	if repo != "" {
		if entry, ok := f.scanLookup(repo); ok {
			return entry.architecture, entry.status
		}
	}
	key := marketplaceLocalInferenceCacheKey(f.registry.Source(), m)
	if key != "" {
		if entry, ok := f.cache.get(key, f.now()); ok {
			if entry.status == marketplaceArchStatusTransient {
				f.degraded.Store(true)
			}
			return entry.architecture, entry.status
		}
	}
	if ctx.Err() != nil {
		f.degraded.Store(true)
		return "", marketplaceArchStatusTransient
	}

	raw, err := f.registry.ReadFile(ctx, m.Path, "", marketplaceLocalInferenceConfigFileName)
	arch := ""
	status := marketplaceArchStatusHit
	// A cancelled read means the caller went away (or the probe deadline fired),
	// not that the repository has no architecture: caching it as transient would
	// degrade every other request for the next 30 seconds.
	cacheable := key != "" && !errors.Is(err, context.Canceled)
	if err != nil {
		var isAuth bool
		status, isAuth = marketplaceLocalInferenceErrorStatus(err)
		if status == marketplaceArchStatusTransient {
			f.degraded.Store(true)
			if isAuth {
				f.authFailures.Add(1)
			}
		}
	} else {
		f.authSuccesses.Add(1)
		arch = marketplaceMetadataFromConfig(raw).Architecture
		if arch == "" {
			status = marketplaceArchStatusMiss
		}
	}

	entry := marketplaceArchEntry{
		architecture: arch,
		writtenAt:    f.now(),
		status:       status,
	}
	if repo != "" {
		f.scanStore(repo, entry)
	}
	if cacheable {
		f.cache.set(key, entry)
	}
	return arch, status
}

func (f *marketplaceLocalInferenceFilter) scanLookup(repo string) (marketplaceArchEntry, bool) {
	f.scanMu.Lock()
	defer f.scanMu.Unlock()
	entry, ok := f.scanArch[repo]
	return entry, ok
}

func (f *marketplaceLocalInferenceFilter) scanStore(repo string, entry marketplaceArchEntry) {
	f.scanMu.Lock()
	defer f.scanMu.Unlock()
	if f.scanArch == nil {
		f.scanArch = make(map[string]marketplaceArchEntry)
	}
	f.scanArch[repo] = entry
}

func (f *marketplaceLocalInferenceFilter) cachedEntry(m csghub.Model) (marketplaceArchEntry, bool) {
	key := marketplaceLocalInferenceCacheKey(f.registry.Source(), m)
	if key == "" {
		return marketplaceArchEntry{}, false
	}
	return f.cache.get(key, f.now())
}

func marketplaceLocalInferenceCacheKey(source modelregistry.Source, m csghub.Model) string {
	repo := strings.Trim(strings.TrimSpace(m.Path), "/")
	if repo == "" {
		return ""
	}
	return string(source) + "|" + repo + "|" + strconv.FormatInt(m.UpdatedAt.Unix(), 10)
}

// marketplaceLocalInferenceErrorStatus classifies a config.json read error.
// The order matters: a context or network failure has no HTTP status and must
// not be cached as a deterministic miss.
func marketplaceLocalInferenceErrorStatus(err error) (marketplaceArchStatus, bool) {
	if status, ok := modelregistry.HTTPStatusCode(err); ok {
		switch {
		case status == http.StatusUnauthorized:
			return marketplaceArchStatusTransient, true
		case status == http.StatusRequestTimeout,
			status == http.StatusTooManyRequests,
			status >= http.StatusInternalServerError:
			return marketplaceArchStatusTransient, false
		default:
			return marketplaceArchStatusMiss, false
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return marketplaceArchStatusTransient, false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return marketplaceArchStatusTransient, false
	}
	return marketplaceArchStatusMiss, false
}

func marketplaceModelInferenceFormat(tags []csghub.Tag) string {
	best := ""
	bestRank := 0
	for _, tag := range tags {
		category := strings.ToLower(strings.TrimSpace(tag.Category))
		if category != "framework" && category != "runtime_framework" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(tag.Name))
		rank := 0
		switch name {
		case string(model.FormatGGUF):
			rank = 3
		case string(model.FormatSafeTensors):
			rank = 2
		case string(model.FormatPyTorch):
			rank = 1
		}
		if rank > bestRank {
			bestRank = rank
			best = name
		}
	}
	return best
}

// marketplaceModelListFormat is the list-side counterpart of the detail page's
// format inference. Some upstream list payloads omit the framework tag and only
// expose "gguf" as a task tag, while the detail payload (and the file tree)
// still resolves to GGUF. Without this fallback the list would treat a GGUF
// image repository as an unknown-format diffusers model and keep a model the
// detail page rejects.
func marketplaceModelListFormat(tags []csghub.Tag) string {
	if format := marketplaceModelInferenceFormat(tags); format != "" {
		return format
	}
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag.Name), string(model.FormatGGUF)) {
			return string(model.FormatGGUF)
		}
	}
	return ""
}

var marketplaceUnsupportedFrameworkTags = map[string]struct{}{
	"onnx":         {},
	"jax":          {},
	"tensorflow":   {},
	"paddle":       {},
	"paddlepaddle": {},
	"joblib":       {},
}

var marketplaceUnsupportedRuntimeTags = map[string]struct{}{
	"paddle":       {},
	"paddlepaddle": {},
	"onnx":         {},
	"tensorflow":   {},
	"flax":         {},
	"tflite":       {},
}

func marketplaceModelHasUnsupportedFormat(tags []csghub.Tag) bool {
	for _, tag := range tags {
		name := strings.ToLower(strings.TrimSpace(tag.Name))
		switch strings.ToLower(strings.TrimSpace(tag.Category)) {
		case "framework":
			if _, ok := marketplaceUnsupportedFrameworkTags[name]; ok {
				return true
			}
		case "runtime_framework":
			if _, ok := marketplaceUnsupportedRuntimeTags[name]; ok {
				return true
			}
		}
	}
	return false
}

// marketplaceModelMatchesFrameworkFilter keeps a model when any framework or
// runtime_framework tag names the requested format. Some upstreams only expose
// the runtime hint, so a framework-only check would silently drop models the
// user asked for.
func marketplaceModelMatchesFrameworkFilter(tags []csghub.Tag, framework string) bool {
	framework = normalizeMarketplaceFramework(framework)
	if framework == "" {
		return true
	}
	for _, tag := range tags {
		category := strings.ToLower(strings.TrimSpace(tag.Category))
		if category != "framework" && category != "runtime_framework" {
			continue
		}
		if normalizeMarketplaceFramework(tag.Name) == framework {
			return true
		}
	}
	return false
}

// marketplaceTaskAliases maps legacy pipeline tags onto the task tag the local
// runtimes route by. "stable-diffusion" predates "text-to-image" but describes
// the same models; leaving it unmapped sends them through the text probe path
// and hides them.
//
// internal/localinference/support.go keeps the same mapping (isImageTask and
// normalizePipelineTag). Both layers are needed: this one sees list tags, that
// one sees the tags of a downloaded directory, so update them together.
var marketplaceTaskAliases = map[string]string{
	"stable-diffusion": "text-to-image",
}

func marketplaceRoutedTaskTag(tags []csghub.Tag) string {
	first := ""
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if strings.ToLower(strings.TrimSpace(tag.Category)) != "task" {
			continue
		}
		task := strings.ToLower(strings.TrimSpace(tag.Name))
		if task == "" {
			continue
		}
		if alias, ok := marketplaceTaskAliases[task]; ok {
			task = alias
		}
		if first == "" {
			first = task
		}
		seen[task] = struct{}{}
	}
	// Video is checked first so a model that also claims image support is
	// excluded conservatively: no shipped local runtime handles video.
	for _, group := range [][]string{
		{"image-to-video", "text-to-video", "video-text-to-text"},
		{"text-to-image", "image-to-image"},
		{"automatic-speech-recognition"},
		{"text-to-speech"},
	} {
		for _, task := range group {
			if _, ok := seen[task]; ok {
				return task
			}
		}
	}
	return first
}

func marketplaceVideoTask(task string) bool {
	switch task {
	case "image-to-video", "text-to-video", "video-text-to-text":
		return true
	default:
		return false
	}
}

func marketplaceIsTTSModel(task, repo string) bool {
	return task == "text-to-speech" || model.IsTTSModelFamily(repo) || model.IsTTSModelName(repo)
}

func marketplaceNeedsArchitecture(format, task string, m csghub.Model, isTTS bool) bool {
	if isTTS {
		return true
	}
	if format == "" {
		return false
	}
	if format == string(model.FormatGGUF) {
		return false
	}
	switch task {
	case "automatic-speech-recognition", "text-to-image", "image-to-image":
		return false
	}
	return !model.IsASRModelFamily(m.Path)
}

func marketplaceModelRepoName(m csghub.Model) string {
	if name := strings.Trim(strings.TrimSpace(m.Name), "/"); name != "" {
		// Some registries may return a full path in Name; keyword matching must
		// only look at the repository segment, never the namespace.
		return path.Base(name)
	}
	repoPath := strings.TrimSpace(m.Path)
	if repoPath == "" {
		return ""
	}
	return path.Base(repoPath)
}

// marketplaceModelFullName keeps the namespace: quantized repositories are
// identified by tokens such as "mlx-community" that live in the namespace.
// Model.Name is only the repository segment, so Path comes first here.
func marketplaceModelFullName(m csghub.Model) string {
	if repoPath := strings.Trim(strings.TrimSpace(m.Path), "/"); repoPath != "" {
		return repoPath
	}
	return strings.Trim(strings.TrimSpace(m.Name), "/")
}

var marketplaceNonTextGGUFKeywords = []string{
	// Image diffusion architectures.
	"flux", "sdxl", "sd1", "sd-1.5", "sd3", "sd-3",
	"stable-diffusion", "stable_diffusion", "stable-cascade", "stable_cascade",
	"qwen-image", "qwen_image", "hidream", "pixart", "kolors", "wuerstchen",
	"sana", "cogview", "zimage", "z-image", "z_image", "kandinsky", "deepfloyd",
	"latentconsistency", "latent-consistency", "latent_consistency",
	"glmimage", "glm-image", "glm_image", "ovisimage", "ovis-image", "ovis_image",
	// Video diffusion architectures.
	"wan2", "hunyuanvideo", "hunyuan-video", "hunyuan_video",
	"ltx-video", "ltx_video", "ltxv", "cogvideox", "mochi", "animatediff",
}

// marketplaceQuantizedRepoSubstrings match anywhere in the path: "mxfp4" and
// "nvfp4" must hit "fp4", and "mlx-community" must hit "mlx".
var marketplaceQuantizedRepoSubstrings = []string{
	"awq", "gptq", "fp4", "fp8", "mlx", "w4a16", "w8a8",
}

// marketplaceQuantizedRepoTokens only match a whole path segment: "print4"
// contains "int4" and "24bit" contains "4bit", but neither is a quantized
// repository name.
var marketplaceQuantizedRepoTokens = []string{
	"int4", "int8", "4bit", "8bit", "4bits", "8bits",
}

func marketplaceLooksLikeQuantizedWeights(repo string) bool {
	repo = strings.ToLower(strings.TrimSpace(repo))
	if repo == "" {
		return false
	}
	for _, keyword := range marketplaceQuantizedRepoSubstrings {
		if strings.Contains(repo, keyword) {
			return true
		}
	}
	for _, token := range marketplaceQuantizedRepoTokens {
		if marketplaceRepoHasToken(repo, token) {
			return true
		}
	}
	return false
}

// marketplaceRepoHasToken reports whether token appears as one whole segment of
// a repository path such as "Qwen/Qwen2.5-72B-Instruct-Int4".
func marketplaceRepoHasToken(repo, token string) bool {
	segments := strings.FieldsFunc(repo, func(r rune) bool {
		switch r {
		case '-', '_', '.', '/', ' ', '\t':
			return true
		}
		return false
	})
	for _, segment := range segments {
		if segment == token {
			return true
		}
	}
	return false
}

func marketplaceLooksLikeNonTextGGUF(repo string) bool {
	repo = strings.ToLower(strings.TrimSpace(repo))
	if repo == "" {
		return false
	}
	for _, keyword := range marketplaceNonTextGGUFKeywords {
		if strings.Contains(repo, keyword) {
			return true
		}
	}
	return false
}

func marketplaceLocalInferenceEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1":
		return true
	default:
		return false
	}
}

// The hub local-model-filter endpoint classifies the whole catalog and returns the
// models it cannot decide as candidates. The client judges those candidates with
// the same rules the detail page uses, so the list and the detail page agree.
const (
	marketplaceLocalModelFilterCandidateBatch   = 5
	marketplaceLocalModelFilterCandidateBatches = 3
	marketplaceLocalModelFilterExcludeLimit     = 250
	marketplaceLocalModelFilterDiskMultiplier   = 2
)

// marketplaceLocalModelFilterCapabilities mirrors the client's local inference
// rules. Every table is read from the package that implements the rule, so the
// declared vocabulary cannot drift from the runtime.
func marketplaceLocalModelFilterCapabilities() csghub.LocalModelFilterCapabilities {
	convertEnabled := true
	allowEmptyImageClass := true
	taskAliases := make(map[string]string, len(marketplaceTaskAliases))
	for from, to := range marketplaceTaskAliases {
		taskAliases[from] = to
	}
	return csghub.LocalModelFilterCapabilities{
		Formats: []string{
			string(model.FormatGGUF),
			string(model.FormatSafeTensors),
			string(model.FormatPyTorch),
		},
		NativeFormats: []string{},
		Tasks: []string{
			"text-generation",
			"text-to-image",
			"image-to-image",
			"automatic-speech-recognition",
			"text-to-speech",
		},
		Convert: csghub.LocalModelFilterConvert{
			Enabled:        &convertEnabled,
			Architectures:  convert.SupportedHFArchitectures(),
			DiskMultiplier: marketplaceLocalModelFilterDiskMultiplier,
		},
		TaskAliases:                     taskAliases,
		ImagePipelineSubstrings:         localinference.ImagePipelineSubstrings(),
		ImageClassFallbackSubstrings:    localinference.ImageClassFallbackSubstrings(),
		ImageUnsupportedClassSubstrings: localinference.ImageUnsupportedClassSubstrings(),
		ASRNameFamilies:                 model.ASRModelFamilies(),
		ASRArchSubstrings:               model.ASRArchitectures(),
		TTSNameFamilies:                 model.TTSModelFamilies(),
		TTSNameTokens:                   []string{"tts"},
		TTSTaskArchSubstrings:           model.TTSArchitectures(),
		TTSBackendArchSubstrings:        append([]string{"Qwen3TTS"}, model.TTSArchitectures()...),
		TTSBackendNameSubstrings:        []string{"kokoro"},
		DiffusionGGUFArchitectures:      model.DiffusionGGUFArchitectures(),
		AllowEmptyImageClass:            &allowEmptyImageClass,
		UnsupportedFormatTags:           marketplaceUnsupportedFrameworkTagNames(),
		UnsupportedRuntimeTags:          marketplaceUnsupportedRuntimeTagNames(),
		QuantSubstrings:                 append([]string(nil), marketplaceQuantizedRepoSubstrings...),
		QuantTokens:                     append([]string(nil), marketplaceQuantizedRepoTokens...),
		GGUFNonTextKeywords:             append([]string(nil), marketplaceNonTextGGUFKeywords...),
	}
}

func marketplaceUnsupportedFrameworkTagNames() []string {
	names := make([]string, 0, len(marketplaceUnsupportedFrameworkTags))
	for name := range marketplaceUnsupportedFrameworkTags {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func marketplaceUnsupportedRuntimeTagNames() []string {
	names := make([]string, 0, len(marketplaceUnsupportedRuntimeTags))
	for name := range marketplaceUnsupportedRuntimeTags {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// listLocalModelFilter asks the hub to classify the catalog. It returns
// fallback=true when the caller should use the scan filter instead: the hub does
// not have the endpoint (404), rejects the request (400), the token (401, after
// one anonymous retry), fails (5xx / network / timeout), or answers
// query_timeout. A rate limit (429) is
// surfaced as an error because the scan fallback would multiply the upstream
// load.
func (f *marketplaceLocalInferenceFilter) listLocalModelFilter(ctx context.Context, req csghub.LocalModelFilterRequest) (marketplaceLocalInferenceResult, bool, error) {
	lister, ok := f.registry.(modelregistry.LocalModelFilterLister)
	if !ok {
		return marketplaceLocalInferenceResult{}, true, nil
	}
	if f.endpointUnavailable != nil && f.endpointUnavailable() {
		// The hub is known to lack the endpoint; skip the doomed round trip.
		return marketplaceLocalInferenceResult{}, true, nil
	}
	if f.timeoutCooldown != nil && f.timeoutCooldown() {
		// The hub recently answered query_timeout; the scan is the only way to
		// render this page until the cooldown expires.
		return marketplaceLocalInferenceResult{}, true, nil
	}
	f.resetScanState()

	resp, err := lister.ListLocalModelFilter(ctx, req)
	if err != nil {
		if status, ok := modelregistry.HTTPStatusCode(err); ok && status == http.StatusUnauthorized {
			// The saved token may have expired. Retry anonymously for this
			// request only; the token itself stays untouched.
			resp, err = lister.ListLocalModelFilterAnonymous(ctx, req)
		}
	}
	if err != nil {
		if status, ok := modelregistry.HTTPStatusCode(err); ok && status == http.StatusNotFound {
			if f.markEndpointUnavailable != nil {
				f.markEndpointUnavailable()
			}
		}
		if marketplaceLocalModelFilterFallback(err) {
			log.Printf("MARKETPLACE LOCAL INFERENCE: hub local-model-filter unavailable (%v); falling back to scan", err)
			return marketplaceLocalInferenceResult{}, true, nil
		}
		return marketplaceLocalInferenceResult{}, false, err
	}
	if resp.QueryTimeout {
		// A timed-out hub answer means its database cannot serve this query
		// right now. The scan still produces a page, so park the endpoint
		// instead of surfacing a retryable error the user cannot act on.
		log.Printf("MARKETPLACE LOCAL INFERENCE: hub local-model-filter timed out; falling back to scan")
		if f.markTimeout != nil {
			f.markTimeout()
		}
		return marketplaceLocalInferenceResult{}, true, nil
	}
	if f.clearTimeout != nil {
		f.clearTimeout()
	}

	result := marketplaceLocalInferenceResult{
		Models:     resp.Data,
		Total:      resp.TotalConfirmed,
		TotalExact: resp.TotalExact,
		HasMore:    resp.HasMore,
		// A truncated candidate scan makes the page incomplete. A candidate
		// timeout only skips supplementation: the confirmed page still renders,
		// so it stays out of Partial but must not be cached.
		Partial:           resp.LocalCheckTruncated,
		Uncacheable:       resp.LocalCheckTimeout,
		PageLimitExceeded: resp.PageLimitExceeded,
		ExcludePaths:      append([]string(nil), req.ExcludePaths...),
	}
	if resp.CandidatesQueried && !resp.LocalCheckTimeout && len(result.Models) < req.Per {
		f.supplementLocalModelFilterCandidates(ctx, &result, resp.LocalCheck, req.Per)
	}
	if f.degraded.Load() {
		// A failed config.json read dropped a candidate that may be runnable.
		result.Partial = true
	}
	return result, false, nil
}

// marketplaceLocalModelFilterFallback reports whether the hub error means "this
// endpoint cannot serve the request here" rather than "the upstream is busy".
func marketplaceLocalModelFilterFallback(err error) bool {
	if status, ok := modelregistry.HTTPStatusCode(err); ok {
		switch status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest, http.StatusNotFound:
			return true
		case http.StatusTooManyRequests:
			return false
		}
		return status >= http.StatusInternalServerError
	}
	// No HTTP status: a transport failure, a timeout, or the request context
	// ending. The scan filter is the safe fallback for all of them.
	return true
}

// supplementLocalModelFilterCandidates fills the page with candidates this machine
// can run. It reuses the detail page verdict (resolveSupport) and size gate, so
// a model that appears here also appears as runnable on the detail page.
//
// Only rejected candidates and candidates added to the page are consumed. A
// candidate that would fit but did not make it into the page stays unconsumed,
// so it comes back with the next page instead of disappearing.
func (f *marketplaceLocalInferenceFilter) supplementLocalModelFilterCandidates(ctx context.Context, result *marketplaceLocalInferenceResult, candidates []csghub.LocalModelFilterCandidate, per int) {
	// Each probe can wait on a 30 s HTTP client timeout and this loop is
	// serial, so without a shared deadline one page could block for minutes.
	// A probe cut off by this deadline is a transient failure: the candidate
	// stays unconsumed and the page is marked partial.
	probeCtx, cancelProbe := context.WithTimeout(ctx, f.probeTimeout)
	defer cancelProbe()
	consumed := make(map[string]struct{})
	batches := 0
	offset := 0
	// unevaluated tracks candidates this page did not look at. It is what keeps
	// them reachable: the cursor must carry them to the next page even when the
	// batch cursor already reached the end of the candidate list.
	unevaluated := false
	for offset < len(candidates) && len(result.Models) < per && batches < marketplaceLocalModelFilterCandidateBatches {
		end := offset + marketplaceLocalModelFilterCandidateBatch
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[offset:end]
		offset = end
		batches++

		for _, candidate := range batch {
			if len(result.Models) >= per {
				// The rest of this batch was not evaluated.
				unevaluated = true
				break
			}
			path := strings.Trim(strings.TrimSpace(candidate.Model.Path), "/")
			if path == "" {
				// The cursor is path-based, so a candidate without a path cannot be
				// consumed. Repository paths are never empty; skip defensively.
				continue
			}
			support, ok, definitive := f.resolveSupport(probeCtx, candidate.Model)
			if !ok {
				if definitive {
					consumed[path] = struct{}{}
				} else {
					// A transient probe failure is not a verdict: keep the candidate
					// reachable and mark the page incomplete.
					result.Partial = true
				}
				continue
			}
			if !marketplaceLocalInferenceSizeAllowed(support, candidate.Model, f.limits) {
				consumed[path] = struct{}{}
				continue
			}
			result.Models = append(result.Models, candidate.Model)
			consumed[path] = struct{}{}
		}
	}
	if offset < len(candidates) {
		unevaluated = true
	}
	if len(consumed) == 0 {
		// Nothing could be consumed or added. has_more is never lowered here: it
		// also covers candidates the hub could not enumerate (truncation) and
		// data pages after this one, and forcing it false would make those
		// permanently unreachable. The page is still incomplete, so mark it as
		// partial.
		if len(candidates) > 0 && len(result.Models) < per {
			result.Partial = true
		}
		if unevaluated {
			// The cursor did not move, so the next page retries the same
			// candidates; without has_more the ones after this page's batch
			// budget would never be evaluated.
			result.HasMore = true
		}
		return
	}
	// The hub may report exhaustion while this page evaluated only the first
	// batches. Unevaluated candidates must stay reachable: the cursor carries
	// them to the next page, so keep has_more true. A page that is still short
	// is incomplete and must not be cached.
	if unevaluated {
		result.HasMore = true
		if len(result.Models) < per {
			result.Partial = true
		}
	}
	seen := make(map[string]struct{}, len(result.ExcludePaths)+len(consumed))
	merged := make([]string, 0, len(result.ExcludePaths)+len(consumed))
	for _, path := range result.ExcludePaths {
		path = strings.Trim(strings.TrimSpace(path), "/")
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		merged = append(merged, path)
	}
	for path := range consumed {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		merged = append(merged, path)
	}
	if len(merged) > marketplaceLocalModelFilterExcludeLimit {
		// The hub rejects a larger cursor, and dropping it would restart the same
		// candidates on every page (an endless "next" loop once the data pages are
		// exhausted). Stop pagination and say the result is incomplete; the user
		// can narrow the filters to start a new cursor.
		result.ExcludePaths = nil
		result.HasMore = false
		result.Partial = true
		return
	}
	result.ExcludePaths = merged
}

// localFilterEndpointUnavailable reports whether the hub is still within the
// window in which it is known to lack the local-model-filter endpoint.
func (s *Server) localFilterEndpointUnavailable() bool {
	s.localFilterEndpointMu.Lock()
	defer s.localFilterEndpointMu.Unlock()
	if s.localFilterEndpointUnavailableAt.IsZero() {
		return false
	}
	if time.Since(s.localFilterEndpointUnavailableAt) < marketplaceLocalFilterEndpointTTL {
		return true
	}
	s.localFilterEndpointUnavailableAt = time.Time{}
	return false
}

// markLocalFilterEndpointUnavailable records a 404 from the hub endpoint so
// subsequent pages skip the probe until the TTL expires.
func (s *Server) markLocalFilterEndpointUnavailable() {
	s.localFilterEndpointMu.Lock()
	s.localFilterEndpointUnavailableAt = time.Now()
	s.localFilterEndpointMu.Unlock()
}

// localFilterTimeoutCooldown reports whether a recent query_timeout still parks
// the hub endpoint. Expiring the cooldown clears it so the next page probes the
// endpoint again.
func (s *Server) localFilterTimeoutCooldown() bool {
	s.localFilterEndpointMu.Lock()
	defer s.localFilterEndpointMu.Unlock()
	if s.localFilterTimeoutUntil.IsZero() {
		return false
	}
	if time.Now().Before(s.localFilterTimeoutUntil) {
		return true
	}
	s.localFilterTimeoutUntil = time.Time{}
	return false
}

// markLocalFilterTimeout starts the cooldown after the hub reports
// query_timeout. The first timeout parks the endpoint for the short cooldown; a
// timeout after that cooldown expired escalates to the long one.
func (s *Server) markLocalFilterTimeout() {
	s.localFilterEndpointMu.Lock()
	defer s.localFilterEndpointMu.Unlock()
	cooldown := marketplaceLocalFilterTimeoutFirstCooldown
	if s.localFilterTimeoutEscalated {
		cooldown = marketplaceLocalFilterTimeoutCooldown
	}
	s.localFilterTimeoutEscalated = true
	s.localFilterTimeoutUntil = time.Now().Add(cooldown)
}

// clearLocalFilterTimeout resets the cooldown after the hub answered a request,
// so a later timeout starts at the short cooldown again.
func (s *Server) clearLocalFilterTimeout() {
	s.localFilterEndpointMu.Lock()
	defer s.localFilterEndpointMu.Unlock()
	s.localFilterTimeoutUntil = time.Time{}
	s.localFilterTimeoutEscalated = false
}
