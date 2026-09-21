// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/inference"
)

type clusterEngine struct {
	m        *Manager
	model    string
	pinned   string
	opts     EngineOptions
	affinity string
	mu       sync.Mutex
	last     Explain
}

func (e *clusterEngine) ModelName() string { return e.model }

func (e *clusterEngine) Close() error { return nil }

func (e *clusterEngine) PrefersNativeAnthropicMessages() bool {
	return true
}

// SupportsNativeToolStreaming is true when the likely target does; the
// server decides raw proxy vs. aggregation before the call, so we answer for
// the first-ranked candidate.
func (e *clusterEngine) SupportsNativeToolStreaming() bool {
	ranked, _ := e.rank(0, 0)
	if len(ranked) == 0 {
		return false
	}
	c := e.m.candidateStatus(ranked[0].UUID)
	if m, ok := c.Model(e.model); ok {
		return m.NativeToolStreaming
	}
	return false
}

// LastExplain returns the most recent scheduling decision.
func (e *clusterEngine) LastExplain() Explain {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.last
}

func (e *clusterEngine) rank(promptTokens, maxTokens int) ([]Ranked, Explain) {
	settings := e.m.store.Settings()
	req := RankRequest{
		Model:            e.model,
		PromptTokens:     promptTokens,
		MaxTokens:        maxTokens,
		PreferLocal:      settings.PreferLocal,
		AffinityMaxQueue: settings.AffinityMaxQueue,
		PinnedUUID:       e.pinned,
	}
	cands := e.m.candidates(context.Background(), e.model)
	if e.affinity != "" && e.pinned == "" {
		if uuid, ok := e.m.affinity.Lookup(e.affinity); ok {
			req.AffinityUUID = uuid
		} else {
			var holders []string
			for _, c := range cands {
				if c.Status != nil && (c.Health == HealthHealthy || c.Health == HealthSuspect) {
					if _, ok := c.Status.Model(e.model); ok {
						holders = append(holders, c.UUID)
					}
				}
			}
			req.AffinityUUID = Rendezvous(e.affinity, holders)
		}
	}
	ranked, ex := Rank(req, cands)
	e.mu.Lock()
	e.last = ex
	e.mu.Unlock()
	return ranked, ex
}

// dispatch tries candidates in order until one accepts the request before
// its first byte. attempt runs one hop and returns the raw response.
func (e *clusterEngine) dispatch(ctx context.Context, promptTokens, maxTokens int, attempt func(target Ranked) (*http.Response, error)) (*http.Response, error) {
	ranked, ex := e.rank(promptTokens, maxTokens)
	if len(ranked) == 0 {
		return nil, inference.NewHTTPStatusError(http.StatusServiceUnavailable, (&ErrNoNode{Model: e.model, Ex: ex}).Error())
	}
	var tried []string
	var lastErr error
	for _, target := range ranked {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		release := e.m.dir.Reserve(target.UUID)
		started := time.Now()
		resp, err := attempt(target)
		if errors.Is(err, ErrServeLocally) {
			// The caller serves it here; the reservation stays until it
			// reports completion so concurrent requests see this one.
			return nil, &LocalChoice{release: release}
		}
		if err != nil {
			release()
			tried = append(tried, target.UUID)
			lastErr = err
			var re *routeError
			if errors.As(err, &re) {
				switch {
				case re.status == 0 || re.status == http.StatusBadGateway || re.status == http.StatusServiceUnavailable:
					e.m.dir.RequestFailed(target.UUID, e.model, false)
				case re.status == http.StatusTooManyRequests:
					// Busy, not broken: hold it out for as long as it asked.
					e.m.dir.CoolDown(target.UUID, retryAfterUntil(re.retryAfter, time.Now()))
				case re.modelSpecific():
					e.m.dir.RequestFailed(target.UUID, e.model, true)
				}
				if !re.retryable() {
					return nil, inference.NewHTTPStatusError(re.status, re.body)
				}
				e.m.logf("cluster: %s failed on %s (%v); trying next node", e.model, target.Name, err)
				continue
			}
			// Local engine errors: a load failure on this node is model
			// specific; keep going with peers.
			if target.Local {
				e.m.dir.RequestFailed(target.UUID, e.model, true)
				e.m.logf("cluster: %s failed locally (%v); trying peers", e.model, err)
				continue
			}
			continue
		}
		e.m.affinity.Record(e.affinity, target.UUID)
		resp.Header.Set(NodeHeader, target.UUID)
		resp.Header.Set(NodeNameHeader, target.Name)
		resp.Body = &measuredBody{
			ReadCloser: resp.Body,
			onClose: func(sample perfSample) {
				release()
				e.m.recordPerf(target.UUID, e.model, started, sample)
			},
			collectUsage: true,
		}
		return resp, nil
	}
	err := &ErrNoNode{Model: e.model, Tried: tried, Last: lastErr, Ex: ex}
	status := http.StatusServiceUnavailable
	var re *routeError
	if errors.As(lastErr, &re) {
		if re.status == http.StatusNotFound && strings.Contains(re.body, "not a forwardable inference endpoint") {
			// The peer runs an older CSGLite that cannot execute this kind of
			// request for others; that is a deployment problem, not a
			// missing model.
			return nil, inference.NewHTTPStatusError(http.StatusBadGateway, fmt.Sprintf("model %q: the node that holds it runs an older CSGLite version without cluster support for this endpoint; upgrade that node", e.model))
		}
		if re.status >= 400 && re.status != http.StatusServiceUnavailable && re.status != http.StatusBadGateway {
			status = re.status
		}
	}
	return nil, inference.NewHTTPStatusError(status, err.Error())
}

func (e *clusterEngine) hopEngine(ctx context.Context, target Ranked) (inference.Engine, error) {
	if target.Local {
		return e.m.opts.Host.LocalEngine(ctx, e.model, e.opts)
	}
	mem, ok := e.m.store.Member(target.UUID)
	if !ok {
		return nil, &routeError{status: 0, err: fmt.Errorf("node %s is no longer a member", shortUUID(target.UUID))}
	}
	toolStreaming := false
	if st := e.m.candidateStatus(target.UUID); st != nil {
		if ms, ok := st.Model(e.model); ok {
			toolStreaming = ms.NativeToolStreaming
		}
	}
	return e.m.newNodeEngine(mem, e.model, toolStreaming), nil
}

func (e *clusterEngine) ChatCompletion(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	prompt, maxTokens := estimateBodyTokens(reqBody)
	return e.dispatch(ctx, prompt, maxTokens, func(target Ranked) (*http.Response, error) {
		eng, err := e.hopEngine(ctx, target)
		if err != nil {
			return nil, err
		}
		proxier, ok := eng.(inference.ChatCompletionProxier)
		if !ok {
			return nil, fmt.Errorf("node %s cannot proxy chat completions", target.Name)
		}
		return proxier.ChatCompletion(ctx, cloneBody(reqBody))
	})
}

func (e *clusterEngine) Embeddings(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	return e.dispatch(ctx, 0, 1, func(target Ranked) (*http.Response, error) {
		eng, err := e.hopEngine(ctx, target)
		if err != nil {
			return nil, err
		}
		proxier, ok := eng.(inference.EmbeddingsProxier)
		if !ok {
			return nil, fmt.Errorf("node %s cannot proxy embeddings", target.Name)
		}
		return proxier.Embeddings(ctx, cloneBody(reqBody))
	})
}

func (e *clusterEngine) AnthropicMessages(ctx context.Context, reqBody map[string]interface{}, headers http.Header) (*http.Response, error) {
	prompt, maxTokens := estimateBodyTokens(reqBody)
	return e.dispatch(ctx, prompt, maxTokens, func(target Ranked) (*http.Response, error) {
		eng, err := e.hopEngine(ctx, target)
		if err != nil {
			return nil, err
		}
		proxier, ok := eng.(inference.AnthropicMessagesProxier)
		if !ok {
			return nil, &routeError{status: http.StatusNotImplemented, err: fmt.Errorf("node %s cannot proxy Anthropic messages", target.Name)}
		}
		return proxier.AnthropicMessages(ctx, cloneBody(reqBody), headers)
	})
}

func (e *clusterEngine) Generate(ctx context.Context, prompt string, opts inference.Options, onToken inference.TokenCallback) (string, error) {
	return e.Chat(ctx, []inference.Message{{Role: "user", Content: prompt}}, opts, onToken)
}

// Chat serves the Ollama-style path by streaming a chat completion from the
// chosen node.
func (e *clusterEngine) Chat(ctx context.Context, messages []inference.Message, opts inference.Options, onToken inference.TokenCallback) (string, error) {
	return chatOverCompletions(ctx, e, messages, opts, onToken)
}

// RoutedEngine returns an engine that places each call on the best member.
// source is "cluster" or "node:<uuid>". A pin on this node itself yields the
// local engine directly. opts.Kind selects chat or embedding; it reaches both
// the local engine here and, through the forwarded request path, the engine
// the receiving node loads.
func (m *Manager) RoutedEngine(ctx context.Context, modelID, source string, opts EngineOptions) (inference.Engine, error) {
	pinned := NodeUUIDFromSource(source)
	if pinned != "" && pinned == m.identity.UUID {
		return m.opts.Host.LocalEngine(ctx, modelID, opts)
	}
	if pinned != "" {
		if _, ok := m.store.Member(pinned); !ok {
			return nil, inference.NewHTTPStatusError(http.StatusNotFound, fmt.Sprintf("node %s is not a member of this cluster", shortUUID(pinned)))
		}
	}
	if !m.store.InCluster() && pinned == "" {
		return nil, inference.NewHTTPStatusError(http.StatusNotFound, "this node is not part of a cluster")
	}
	return &clusterEngine{m: m, model: modelID, pinned: pinned, opts: opts, affinity: AffinityKeyFromContext(ctx)}, nil
}

// measuredBody wraps a response body to time first byte and completion and to
// pick the usage block out of the stream (final SSE chunk or JSON body).
type measuredBody struct {
	io.ReadCloser
	onClose      func(perfSample)
	collectUsage bool
	sample       perfSample
	tail         bytes.Buffer
	once         sync.Once
	sawFirst     bool
}

func (b *measuredBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if !b.sawFirst {
			b.sawFirst = true
			b.sample.firstByte = time.Now()
		}
		if b.collectUsage {
			b.tail.Write(p[:n])
			if b.tail.Len() > 64<<10 {
				// Keep only the end of the stream; usage arrives last.
				data := b.tail.Bytes()
				b.tail = *bytes.NewBuffer(append([]byte(nil), data[len(data)-32<<10:]...))
			}
		}
	}
	if err == io.EOF {
		b.sample.ok = true
	}
	return n, err
}

func (b *measuredBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() {
		b.sample.end = time.Now()
		b.sample.promptTokens, b.sample.completionTokens = usageFromTail(b.tail.Bytes())
		if b.onClose != nil {
			b.onClose(b.sample)
		}
	})
	return err
}

type perfSample struct {
	firstByte        time.Time
	end              time.Time
	promptTokens     int
	completionTokens int
	ok               bool
}

// estimateBodyTokens guesses prompt size (4 chars per token) and reads
// max_tokens for the scheduler.
func estimateBodyTokens(body map[string]interface{}) (prompt int, maxTokens int) {
	if body == nil {
		return 0, 0
	}
	if raw, err := json.Marshal(body["messages"]); err == nil {
		prompt = len(raw) / 4
	}
	if v, ok := body["max_tokens"].(float64); ok {
		maxTokens = int(v)
	} else if v, ok := body["max_tokens"].(int); ok {
		maxTokens = v
	} else if v, ok := body["max_completion_tokens"].(float64); ok {
		maxTokens = int(v)
	}
	return prompt, maxTokens
}

// usageFromTail finds the last "usage" object in a body or SSE tail.
func usageFromTail(tail []byte) (prompt, completion int) {
	idx := bytes.LastIndex(tail, []byte(`"usage"`))
	if idx < 0 {
		return 0, 0
	}
	rest := tail[idx:]
	start := bytes.IndexByte(rest, '{')
	if start < 0 {
		return 0, 0
	}
	depth := 0
	end := -1
	for i := start; i < len(rest); i++ {
		switch rest[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return 0, 0
	}
	var usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		InputTokens      int `json:"input_tokens"`
		OutputTokens     int `json:"output_tokens"`
	}
	if json.Unmarshal(rest[start:end+1], &usage) != nil {
		return 0, 0
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	return usage.PromptTokens, usage.CompletionTokens
}

// recordPerf turns a finished request into throughput samples for the node.
// Streaming answers separate prompt processing (time to first byte) from
// generation; a non-streaming answer arrives whole, so only a combined rate
// over the whole request can be derived from it and it is attributed to
// decoding, the dominant cost.
func (m *Manager) recordPerf(nodeUUID, model string, started time.Time, s perfSample) {
	decodeTPS, promptTPS := perfRates(started, s)
	if decodeTPS == 0 && promptTPS == 0 {
		return
	}
	m.perf.observe(nodeUUID, model, decodeTPS, promptTPS, 0)
}

func perfRates(started time.Time, s perfSample) (decodeTPS, promptTPS float64) {
	if !s.ok || s.firstByte.IsZero() || s.end.IsZero() {
		return 0, 0
	}
	// An embeddings request generates no tokens, so measuring it by decode
	// speed records nothing at all: every node stayed on the default guess and
	// the scheduler could not tell a fast machine from a slow one for any
	// embedding model. What such a request does show is how quickly a node
	// reads a prompt, which is the phase that decides the answer, so it is
	// recorded as prompt throughput instead.
	if s.completionTokens <= 0 {
		total := s.end.Sub(started).Seconds()
		if s.promptTokens >= 32 && total > 0.02 {
			return 0, float64(s.promptTokens) / total
		}
		return 0, 0
	}
	total := s.end.Sub(started).Seconds()
	gen := s.end.Sub(s.firstByte).Seconds()
	ttfb := s.firstByte.Sub(started).Seconds()
	if total <= 0 {
		return 0, 0
	}
	streamed := gen > 0.2 && gen > total*0.2
	if streamed {
		if s.completionTokens >= 4 {
			decodeTPS = float64(s.completionTokens) / gen
		}
		if s.promptTokens >= 32 && ttfb > 0.02 {
			promptTPS = float64(s.promptTokens) / ttfb
		}
		return decodeTPS, promptTPS
	}
	if s.completionTokens >= 4 {
		decodeTPS = float64(s.completionTokens) / total
	}
	return decodeTPS, 0
}

// defaultRateLimitCooldown is how long a node that answered 429 without a
// Retry-After header is held out. It matches the provider pool's default so a
// rate limit means the same thing wherever it is seen.
const defaultRateLimitCooldown = time.Minute

// retryAfterUntil reads a Retry-After header, in either of the two forms HTTP
// allows: a number of seconds, or an absolute date. Anything unparsable falls
// back to the default cooldown.
func retryAfterUntil(header string, now time.Time) time.Time {
	header = strings.TrimSpace(header)
	if seconds, err := strconv.Atoi(header); header != "" && err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if at, err := http.ParseTime(header); header != "" && err == nil {
		if at.After(now) {
			return at
		}
		return now
	}
	return now.Add(defaultRateLimitCooldown)
}
