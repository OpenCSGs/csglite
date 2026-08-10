package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
)

const providerPoolSourcePrefix = "pool:"
const providerPoolMemberSourceHeader = "X-CSGLite-Pool-Member-Source"
const providerPoolMemberModelHeader = "X-CSGLite-Pool-Member-Model"
const providerPoolFallbackCountHeader = "X-CSGLite-Pool-Fallback-Count"
const providerPoolLimitedCountHeader = "X-CSGLite-Pool-Limited-Count"

func poolSource(id string) string {
	return providerPoolSourcePrefix + strings.TrimSpace(id)
}

func poolIDFromSource(source string) string {
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(source, providerPoolSourcePrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(source, providerPoolSourcePrefix))
}

func providerPoolForRequest(modelID, source string) (config.ProviderPool, bool) {
	if poolID := poolIDFromSource(source); poolID != "" {
		for _, pool := range config.GetProviderPools() {
			if pool.ID == poolID && pool.Enabled {
				return pool, true
			}
		}
		return config.ProviderPool{}, false
	}
	if strings.TrimSpace(source) != "" {
		return config.ProviderPool{}, false
	}
	for _, pool := range config.GetProviderPools() {
		if pool.Enabled && pool.Model == strings.TrimSpace(modelID) {
			return pool, true
		}
	}
	return config.ProviderPool{}, false
}

type providerPoolEngine struct {
	poolID   string
	poolName string
	modelID  string
	members  []providerPoolEngineMember
	mu       *sync.Mutex
	current  map[string]int
	runtime  map[string]*providerPoolMemberRuntime
	now      func() time.Time
	usage    *providerPoolUsageCapture
}

type providerPoolEngineMember struct {
	member config.ProviderPoolMember
	new    func() (inference.Engine, error)
}

type providerPoolUsageEvent struct {
	id     uint64
	at     time.Time
	tokens int
}

type providerPoolMemberRuntime struct {
	requests   []time.Time
	tokens     []providerPoolUsageEvent
	nextToken  uint64
	concurrent int
	cooldown   time.Time
}

type providerPoolAdmission struct {
	key       string
	tokenID   uint64
	estimated int
}

type providerPoolUsageCapture struct {
	mu       sync.Mutex
	source   string
	metadata *apiUsagePoolMetadata
}

type providerPoolUsageContextKey struct{}

func providerPoolUsageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture := &providerPoolUsageCapture{}
		ctx := context.WithValue(r.Context(), providerPoolUsageContextKey{}, capture)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func providerPoolUsageCaptureFromContext(ctx context.Context) *providerPoolUsageCapture {
	capture, _ := ctx.Value(providerPoolUsageContextKey{}).(*providerPoolUsageCapture)
	return capture
}

func (c *providerPoolUsageCapture) set(source string, metadata apiUsagePoolMetadata) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.source = strings.TrimSpace(source)
	copy := metadata
	c.metadata = &copy
}

func (c *providerPoolUsageCapture) get() (string, *apiUsagePoolMetadata) {
	if c == nil {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.metadata == nil {
		return "", nil
	}
	copy := *c.metadata
	return c.source, &copy
}

type providerPoolResponseBody struct {
	io.ReadCloser
	buf      bytes.Buffer
	collect  bool
	complete func([]byte)
	once     sync.Once
}

func (b *providerPoolResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.collect {
		_, _ = b.buf.Write(p[:n])
	}
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *providerPoolResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.finish()
	return err
}

func (b *providerPoolResponseBody) finish() {
	b.once.Do(func() {
		b.complete(b.buf.Bytes())
	})
}

func (e *providerPoolEngine) ModelName() string { return e.modelID }

func (e *providerPoolEngine) Close() error { return nil }

func (e *providerPoolEngine) Generate(ctx context.Context, prompt string, opts inference.Options, onToken inference.TokenCallback) (string, error) {
	inputTokens := estimateProviderPoolTextTokens(prompt)
	estimated := inputTokens + positiveTokenLimit(opts.MaxTokens)
	return e.runChat(ctx, inputTokens, estimated, onToken, func(eng inference.Engine, callback inference.TokenCallback) (string, error) {
		return eng.Generate(ctx, prompt, opts, callback)
	})
}

func (e *providerPoolEngine) Chat(ctx context.Context, messages []inference.Message, opts inference.Options, onToken inference.TokenCallback) (string, error) {
	inputTokens := estimateProviderPoolMessagesTokens(messages)
	estimated := inputTokens + positiveTokenLimit(opts.MaxTokens)
	return e.runChat(ctx, inputTokens, estimated, onToken, func(eng inference.Engine, callback inference.TokenCallback) (string, error) {
		return eng.Chat(ctx, messages, opts, callback)
	})
}

func (e *providerPoolEngine) ChatCompletion(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	stream, _ := reqBody["stream"].(bool)
	return e.runProxy(ctx, estimateProviderPoolRequestTokens(reqBody), !stream, func(eng inference.Engine) (*http.Response, error) {
		proxy, ok := eng.(inference.ChatCompletionProxier)
		if !ok {
			return nil, fmt.Errorf("pool member does not support chat completions")
		}
		return proxy.ChatCompletion(ctx, reqBody)
	})
}

func (e *providerPoolEngine) Embeddings(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	return e.runProxy(ctx, estimateProviderPoolRequestTokens(reqBody), true, func(eng inference.Engine) (*http.Response, error) {
		proxy, ok := eng.(inference.EmbeddingsProxier)
		if !ok {
			return nil, fmt.Errorf("pool member does not support embeddings")
		}
		return proxy.Embeddings(ctx, reqBody)
	})
}

func (e *providerPoolEngine) runChat(ctx context.Context, inputTokens, estimatedTokens int, onToken inference.TokenCallback, call func(inference.Engine, inference.TokenCallback) (string, error)) (string, error) {
	var lastErr error
	fallbackCount := int64(0)
	limitedCount := int64(0)
	for _, member := range e.orderedMembers() {
		admission, ok := e.admit(member.member, estimatedTokens)
		if !ok {
			fallbackCount++
			limitedCount++
			continue
		}
		eng, err := member.new()
		if err == nil {
			var result string
			streamStarted := false
			callback := onToken
			if onToken != nil {
				callback = func(token string) {
					streamStarted = true
					onToken(token)
				}
			}
			result, err = call(eng, callback)
			actualTokens := estimatedTokens
			if err == nil {
				actualTokens = inputTokens + estimateProviderPoolTextTokens(result)
				if actualTokens <= inputTokens {
					actualTokens = estimatedTokens
				}
			}
			e.complete(admission, actualTokens)
			if err == nil {
				e.captureUsage(member.member, fallbackCount, limitedCount)
				return result, nil
			}
			lastErr = err
			if streamStarted {
				return "", lastErr
			}
		} else {
			e.complete(admission, estimatedTokens)
			lastErr = err
		}
		e.coolOnRateLimit(admission.key, lastErr, "")
		if !providerPoolRetryable(lastErr) {
			return "", lastErr
		}
		fallbackCount++
		if inference.HTTPStatusCode(lastErr) == http.StatusTooManyRequests {
			limitedCount++
		}
	}
	if lastErr == nil {
		if len(e.members) > 0 {
			return "", inference.NewHTTPStatusError(http.StatusTooManyRequests, "provider pool capacity is exhausted")
		}
		lastErr = fmt.Errorf("provider pool has no available members")
	}
	return "", lastErr
}

func (e *providerPoolEngine) runProxy(ctx context.Context, estimatedTokens int, collectActual bool, call func(inference.Engine) (*http.Response, error)) (*http.Response, error) {
	var lastErr error
	fallbackCount := 0
	limitedCount := 0
	for _, member := range e.orderedMembers() {
		admission, ok := e.admit(member.member, estimatedTokens)
		if !ok {
			fallbackCount++
			limitedCount++
			continue
		}
		eng, err := member.new()
		retryAfter := ""
		if err == nil {
			var response *http.Response
			response, err = call(eng)
			if err == nil && response != nil && response.StatusCode >= http.StatusBadRequest {
				retryAfter = response.Header.Get("Retry-After")
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
				_ = response.Body.Close()
				if readErr != nil {
					err = fmt.Errorf("reading provider pool error response: %w", readErr)
				} else {
					err = inference.NewHTTPStatusError(response.StatusCode, string(body))
				}
			}
			if err == nil && response != nil {
				response.Header.Set(providerPoolMemberSourceHeader, member.member.Source)
				response.Header.Set(providerPoolMemberModelHeader, member.member.Model)
				response.Header.Set(providerPoolFallbackCountHeader, strconv.Itoa(fallbackCount))
				response.Header.Set(providerPoolLimitedCountHeader, strconv.Itoa(limitedCount))
				response.Body = &providerPoolResponseBody{
					ReadCloser: response.Body,
					collect:    collectActual,
					complete: func(body []byte) {
						e.complete(admission, providerPoolActualTokens(body, estimatedTokens))
					},
				}
				e.captureUsage(member.member, int64(fallbackCount), int64(limitedCount))
				return response, nil
			}
			e.complete(admission, estimatedTokens)
			lastErr = err
		} else {
			e.complete(admission, estimatedTokens)
			lastErr = err
		}
		e.coolOnRateLimit(admission.key, lastErr, retryAfter)
		if !providerPoolRetryable(lastErr) {
			return nil, lastErr
		}
		fallbackCount++
		if inference.HTTPStatusCode(lastErr) == http.StatusTooManyRequests {
			limitedCount++
		}
	}
	if lastErr == nil {
		if len(e.members) > 0 {
			return nil, inference.NewHTTPStatusError(http.StatusTooManyRequests, "provider pool capacity is exhausted")
		}
		lastErr = fmt.Errorf("provider pool has no available members")
	}
	return nil, lastErr
}

func (e *providerPoolEngine) captureUsage(member config.ProviderPoolMember, fallbackCount, limitedCount int64) {
	e.usage.set(member.Source, apiUsagePoolMetadata{
		PoolID:        e.poolID,
		PoolName:      e.poolName,
		PoolModel:     e.modelID,
		MemberModel:   member.Model,
		FallbackCount: fallbackCount,
		LimitedCount:  limitedCount,
	})
}

func providerPoolRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if strings.Contains(strings.ToLower(inference.HTTPErrorMessage(err)), "insufficient balance") {
		return true
	}
	status := inference.HTTPStatusCode(err)
	return status == 0 ||
		status == http.StatusPaymentRequired ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

func requestPoolUsageSource(requested string, response *http.Response) string {
	if response == nil {
		return requested
	}
	if source := strings.TrimSpace(response.Header.Get(providerPoolMemberSourceHeader)); source != "" {
		return source
	}
	return requested
}

func requestPoolUsageMetadata(model, requested string, response *http.Response) *apiUsagePoolMetadata {
	if response == nil {
		return nil
	}
	pool, ok := providerPoolForRequest(model, requested)
	if !ok {
		return nil
	}
	memberSource := strings.TrimSpace(response.Header.Get(providerPoolMemberSourceHeader))
	if memberSource == "" {
		return nil
	}
	fallbackCount, _ := strconv.ParseInt(strings.TrimSpace(response.Header.Get(providerPoolFallbackCountHeader)), 10, 64)
	limitedCount, _ := strconv.ParseInt(strings.TrimSpace(response.Header.Get(providerPoolLimitedCountHeader)), 10, 64)
	return &apiUsagePoolMetadata{
		PoolID:        pool.ID,
		PoolName:      pool.Name,
		PoolModel:     pool.Model,
		MemberModel:   strings.TrimSpace(response.Header.Get(providerPoolMemberModelHeader)),
		FallbackCount: fallbackCount,
		LimitedCount:  limitedCount,
	}
}

func (e *providerPoolEngine) runtimeKey(memberID string) string {
	return e.poolID + "\x00" + memberID
}

func (e *providerPoolEngine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// admit atomically reserves request, token, and concurrency capacity. It never
// waits: unavailable members are skipped so routing can immediately fall back.
func (e *providerPoolEngine) admit(member config.ProviderPoolMember, estimatedTokens int) (providerPoolAdmission, bool) {
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	now := e.clock()
	cutoff := now.Add(-time.Minute)
	key := e.runtimeKey(member.ID)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		e.runtime = make(map[string]*providerPoolMemberRuntime)
	}
	state := e.runtime[key]
	if state == nil {
		state = &providerPoolMemberRuntime{}
		e.runtime[key] = state
	}
	state.requests = pruneProviderPoolRequests(state.requests, cutoff)
	state.tokens = pruneProviderPoolTokens(state.tokens, cutoff)
	if now.Before(state.cooldown) ||
		(member.MaxConcurrent > 0 && state.concurrent >= member.MaxConcurrent) ||
		(member.RequestsPM > 0 && len(state.requests) >= member.RequestsPM) ||
		(member.TokensPM > 0 && providerPoolTokenTotal(state.tokens)+estimatedTokens > member.TokensPM) {
		return providerPoolAdmission{}, false
	}
	state.nextToken++
	state.requests = append(state.requests, now)
	state.tokens = append(state.tokens, providerPoolUsageEvent{id: state.nextToken, at: now, tokens: estimatedTokens})
	state.concurrent++
	return providerPoolAdmission{key: key, tokenID: state.nextToken, estimated: estimatedTokens}, true
}

// complete replaces the admission estimate with actual total usage when known
// and releases concurrency. SSE responses retain their admission estimate.
func (e *providerPoolEngine) complete(admission providerPoolAdmission, actualTokens int) {
	if admission.key == "" {
		return
	}
	if actualTokens < 1 {
		actualTokens = admission.estimated
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.runtime[admission.key]
	if state == nil {
		return
	}
	if state.concurrent > 0 {
		state.concurrent--
	}
	for i := range state.tokens {
		if state.tokens[i].id == admission.tokenID {
			state.tokens[i].tokens = actualTokens
			break
		}
	}
}

func (e *providerPoolEngine) coolOnRateLimit(key string, err error, retryAfter string) {
	if key == "" || inference.HTTPStatusCode(err) != http.StatusTooManyRequests {
		return
	}
	now := e.clock()
	until := now.Add(time.Minute)
	if parsed, ok := parseProviderPoolRetryAfter(retryAfter, now); ok {
		until = parsed
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if state := e.runtime[key]; state != nil && until.After(state.cooldown) {
		state.cooldown = until
	}
}

func parseProviderPoolRetryAfter(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); value != "" && err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	if at, err := http.ParseTime(value); value != "" && err == nil {
		if at.Before(now) {
			at = now
		}
		return at, true
	}
	return time.Time{}, false
}

func pruneProviderPoolRequests(events []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(events) && !events[i].After(cutoff) {
		i++
	}
	return events[i:]
}

func pruneProviderPoolTokens(events []providerPoolUsageEvent, cutoff time.Time) []providerPoolUsageEvent {
	i := 0
	for i < len(events) && !events[i].at.After(cutoff) {
		i++
	}
	return events[i:]
}

func providerPoolTokenTotal(events []providerPoolUsageEvent) int {
	total := 0
	for _, event := range events {
		total += event.tokens
	}
	return total
}

func positiveTokenLimit(limit int) int {
	if limit > 0 {
		return limit
	}
	return 0
}

func estimateProviderPoolTextTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 1
	}
	return max(1, (len([]byte(text))+3)/4)
}

func estimateProviderPoolMessagesTokens(messages []inference.Message) int {
	total := 0
	for _, message := range messages {
		total += estimateProviderPoolValueTokens(message.Content)
		if message.ReasoningContent != "" {
			total += estimateProviderPoolTextTokens(message.ReasoningContent)
		}
	}
	return max(1, total)
}

func estimateProviderPoolValueTokens(value interface{}) int {
	switch value := value.(type) {
	case nil:
		return 0
	case string:
		return estimateProviderPoolTextTokens(value)
	default:
		body, err := json.Marshal(value)
		if err != nil {
			return estimateProviderPoolTextTokens(fmt.Sprint(value))
		}
		return estimateProviderPoolTextTokens(string(body))
	}
}

func estimateProviderPoolRequestTokens(body map[string]interface{}) int {
	input := 0
	for _, key := range []string{"messages", "input", "prompt"} {
		if value, ok := body[key]; ok {
			input += estimateProviderPoolValueTokens(value)
		}
	}
	if input == 0 {
		input = 1
	}
	limit := providerPoolInt(body["max_completion_tokens"])
	if limit == 0 {
		limit = providerPoolInt(body["max_tokens"])
	}
	return input + positiveTokenLimit(limit)
}

func providerPoolInt(value interface{}) int {
	switch value := value.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

func providerPoolActualTokens(body []byte, fallback int) int {
	var payload struct {
		Usage struct {
			TotalTokens      int `json:"total_tokens"`
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return fallback
	}
	if payload.Usage.TotalTokens > 0 {
		return payload.Usage.TotalTokens
	}
	if total := payload.Usage.PromptTokens + payload.Usage.CompletionTokens; total > 0 {
		return total
	}
	return fallback
}

func (e *providerPoolEngine) orderedMembers() []providerPoolEngineMember {
	members := append([]providerPoolEngineMember{}, e.members...)
	sort.SliceStable(members, func(i, j int) bool {
		return members[i].member.Priority < members[j].member.Priority
	})
	if len(members) < 2 {
		return members
	}
	firstPriority := members[0].member.Priority
	end := 0
	for end < len(members) && members[end].member.Priority == firstPriority {
		end++
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.current == nil {
		e.current = map[string]int{}
	}
	total := 0
	best := 0
	for i := 0; i < end; i++ {
		weight := members[i].member.Weight
		if weight < 1 {
			weight = 1
		}
		key := e.runtimeKey(members[i].member.ID)
		e.current[key] += weight
		total += weight
		if e.current[key] > e.current[e.runtimeKey(members[best].member.ID)] {
			best = i
		}
	}
	e.current[e.runtimeKey(members[best].member.ID)] -= total
	members[0], members[best] = members[best], members[0]
	return members
}

func (s *Server) newProviderPoolChatEngine(ctx context.Context, pool config.ProviderPool, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (inference.Engine, error) {
	members := make([]providerPoolEngineMember, 0, len(pool.Members))
	for _, member := range pool.Members {
		member := member
		members = append(members, providerPoolEngineMember{
			member: member,
			new: func() (inference.Engine, error) {
				return s.getChatEngine(ctx, member.Model, member.Source, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
			},
		})
	}
	return &providerPoolEngine{
		poolID: pool.ID, poolName: pool.Name, modelID: pool.Model, members: members,
		mu: &s.poolMu, current: s.poolCurrent, runtime: s.poolRuntime,
		usage: providerPoolUsageCaptureFromContext(ctx),
	}, nil
}

func (s *Server) newProviderPoolEmbeddingEngine(ctx context.Context, pool config.ProviderPool, numCtx, nGPULayers int, dtype string) (inference.Engine, error) {
	members := make([]providerPoolEngineMember, 0, len(pool.Members))
	for _, member := range pool.Members {
		member := member
		members = append(members, providerPoolEngineMember{
			member: member,
			new: func() (inference.Engine, error) {
				return s.getEmbeddingEngine(ctx, member.Model, member.Source, numCtx, nGPULayers, dtype)
			},
		})
	}
	return &providerPoolEngine{
		poolID: pool.ID, poolName: pool.Name, modelID: pool.Model, members: members,
		mu: &s.poolMu, current: s.poolCurrent, runtime: s.poolRuntime,
		usage: providerPoolUsageCaptureFromContext(ctx),
	}, nil
}
