// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/correlation"
	"github.com/opencsgs/csglite/internal/inference"
)

// Source values understood by the cluster router.
const (
	SourceCluster    = "cluster"
	SourceNodePrefix = "node:"
)

// NodeUUIDFromSource extracts the UUID from "node:<uuid>", or "".
func NodeUUIDFromSource(source string) string {
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(source, SourceNodePrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(source, SourceNodePrefix))
}

// IsClusterSource reports whether source is handled by the cluster router.
func IsClusterSource(source string) bool {
	source = strings.TrimSpace(source)
	return strings.EqualFold(source, SourceCluster) || NodeUUIDFromSource(source) != ""
}

// ErrNoNode is returned when no member can serve the model.
type ErrNoNode struct {
	Model string
	Tried []string
	Last  error
	Ex    Explain
}

func (e *ErrNoNode) Error() string {
	if len(e.Tried) == 0 {
		return fmt.Sprintf("no cluster node can serve model %q right now", e.Model)
	}
	msg := fmt.Sprintf("model %q failed on every eligible cluster node (%d tried)", e.Model, len(e.Tried))
	if e.Last != nil {
		msg += ": " + e.Last.Error()
	}
	return msg
}

// ErrNoNodeCode is the machine-readable code for ErrNoNode.
const ErrNoNodeCode = "cluster_no_available_node"

// ---- node engine: one remote member ----

type nodeEngine struct {
	m       *Manager
	mem     Member
	model   string
	toolStr bool
	// hop is the entry node's UUID sent in RoutedHeader.
	hop string
}

func (m *Manager) newNodeEngine(mem Member, model string, toolStreaming bool) *nodeEngine {
	return &nodeEngine{m: m, mem: mem, model: model, toolStr: toolStreaming, hop: m.identity.UUID}
}

func (e *nodeEngine) ModelName() string { return e.model }
func (e *nodeEngine) Close() error      { return nil }
func (e *nodeEngine) SupportsNativeToolStreaming() bool {
	return e.toolStr
}
func (e *nodeEngine) PrefersNativeAnthropicMessages() bool { return true }

// forward posts body to the member's forwarded-inference path and returns the
// raw response; non-2xx answers become errors carrying the status.
func (e *nodeEngine) forward(ctx context.Context, path string, body []byte, headers http.Header) (*http.Response, error) {
	addrs := e.m.dir.Candidates(e.mem.UUID)
	if len(addrs) == 0 {
		return nil, &routeError{status: 0, err: errors.New("node has no known address")}
	}
	client := peerClient(e.m.identity, e.mem.UUID, e.mem.CertFingerprint)
	var lastErr error
	for i, addr := range addrs {
		if i > 1 {
			break // two addresses is plenty for one request; polling repairs the rest
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+strings.TrimSuffix(peerPathInference, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set(RoutedHeader, e.hop)
		correlation.ApplyRequestHeaders(req)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = &routeError{status: 0, err: err}
			continue
		}
		if resp.StatusCode/100 != 2 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			return nil, &routeError{status: resp.StatusCode, body: string(raw), err: fmt.Errorf("node %s answered %d", e.mem.Name, resp.StatusCode)}
		}
		e.m.dir.RequestSucceeded(e.mem.UUID)
		e.m.store.RecordAddress(e.mem.UUID, addr)
		return resp, nil
	}
	return nil, lastErr
}

// routeError classifies a failed hop for failover decisions.
type routeError struct {
	status int
	body   string
	err    error
}

func (e *routeError) Error() string {
	if e.body != "" {
		return fmt.Sprintf("%v: %s", e.err, truncate(e.body, 300))
	}
	return e.err.Error()
}

func (e *routeError) Unwrap() error { return e.err }

// modelSpecific reports whether the failure is about this model on this
// node (rather than the node itself), so only the (node, model) pair is
// excluded.
func (e *routeError) modelSpecific() bool {
	switch e.status {
	case http.StatusNotFound, http.StatusInternalServerError, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// retryable reports whether another node should be tried.
func (e *routeError) retryable() bool {
	switch {
	case e.status == 0:
		return true // transport
	case e.status == http.StatusBadRequest, e.status == http.StatusRequestEntityTooLarge:
		return false // the request itself is wrong; every node will say so
	case e.status == http.StatusUnauthorized:
		return false
	default:
		return true
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (e *nodeEngine) ChatCompletion(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	if reqBody == nil {
		reqBody = map[string]interface{}{}
	}
	body := cloneBody(reqBody)
	body["model"] = e.model
	body["source"] = "local"
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	if stream, _ := body["stream"].(bool); stream {
		h.Set("Accept", "text/event-stream")
	} else {
		h.Set("Accept", "application/json")
	}
	return e.forward(ctx, "/v1/chat/completions", raw, h)
}

func (e *nodeEngine) Embeddings(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	body := cloneBody(reqBody)
	body["model"] = e.model
	body["source"] = "local"
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	h.Set("Accept", "application/json")
	return e.forward(ctx, "/v1/embeddings", raw, h)
}

func (e *nodeEngine) AnthropicMessages(ctx context.Context, reqBody map[string]interface{}, headers http.Header) (*http.Response, error) {
	body := cloneBody(reqBody)
	body["model"] = e.model
	body["source"] = "local"
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	for _, k := range []string{"anthropic-version", "anthropic-beta", "Accept"} {
		if v := headers.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	if h.Get("Accept") == "" {
		h.Set("Accept", "application/json")
	}
	return e.forward(ctx, "/v1/messages", raw, h)
}

func (e *nodeEngine) Generate(ctx context.Context, prompt string, opts inference.Options, onToken inference.TokenCallback) (string, error) {
	return e.Chat(ctx, []inference.Message{{Role: "user", Content: prompt}}, opts, onToken)
}

func (e *nodeEngine) Chat(ctx context.Context, messages []inference.Message, opts inference.Options, onToken inference.TokenCallback) (string, error) {
	stream := onToken != nil
	body := map[string]interface{}{
		"messages":    messages,
		"temperature": opts.Temperature,
		"top_p":       opts.TopP,
		"stream":      stream,
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	if opts.Seed >= 0 {
		body["seed"] = opts.Seed
	}
	if len(opts.Stop) > 0 {
		body["stop"] = opts.Stop
	}
	if opts.NumCtx > 0 {
		body["num_ctx"] = opts.NumCtx
	}
	resp, err := e.ChatCompletion(ctx, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if !stream {
		var parsed struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			return "", err
		}
		if len(parsed.Choices) == 0 {
			return "", nil
		}
		return parsed.Choices[0].Message.Content, nil
	}
	return readOpenAISSE(resp.Body, onToken)
}

// readOpenAISSE collects delta content from a chat completions stream.
func readOpenAISSE(body io.Reader, onToken inference.TokenCallback) (string, error) {
	var full strings.Builder
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				full.WriteString(c.Delta.Content)
				if onToken != nil {
					onToken(c.Delta.Content)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), err
	}
	return full.String(), nil
}

func cloneBody(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ---- cluster engine: schedule, forward, fail over ----

type clusterEngine struct {
	m        *Manager
	model    string
	pinned   string
	opts     EngineOptions
	affinity string
	mu       sync.Mutex
	last     Explain
}

// ChatEngine returns an engine that places each call on the best member.
// source is "cluster" or "node:<uuid>". A pin on this node itself yields the
// local engine directly.
func (m *Manager) ChatEngine(ctx context.Context, modelID, source string, opts EngineOptions) (inference.Engine, error) {
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

func (e *clusterEngine) ModelName() string { return e.model }
func (e *clusterEngine) Close() error      { return nil }
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

// candidates builds the scheduler input from the directory and local status.
func (m *Manager) candidates(ctx context.Context, model string) []Candidate {
	var out []Candidate
	local := m.cachedLocalStatus(ctx)
	out = append(out, Candidate{UUID: m.identity.UUID, Name: m.identity.Name, Local: true, Status: local, Health: HealthHealthy, Breaker: m.dir.ModelBroken(m.identity.UUID, model)})
	now := time.Now()
	for _, rt := range m.dir.Snapshot() {
		if _, ok := m.store.Member(rt.UUID); !ok {
			continue
		}
		name := rt.UUID
		if rt.Status != nil && rt.Status.Name != "" {
			name = rt.Status.Name
		} else if mem, ok := m.store.Member(rt.UUID); ok && mem.Name != "" {
			name = mem.Name
		}
		st := rt.Status
		if st != nil {
			// Entry-side perf samples override what the node reports when
			// we have measured this node ourselves.
			st = m.overlayPerf(rt.UUID, st)
		}
		out = append(out, Candidate{UUID: rt.UUID, Name: name, Status: st, Health: rt.Health, Reserved: int(rt.reserveSeq), Breaker: m.dir.ModelBroken(rt.UUID, model)})
	}
	_ = now
	return out
}

func (m *Manager) overlayPerf(nodeUUID string, st *Status) *Status {
	changed := false
	models := make([]ModelStatus, len(st.Models))
	copy(models, st.Models)
	for i := range models {
		if p := m.perf.get(nodeUUID, models[i].ID); p != nil && p.Samples > 0 {
			merged := *p
			if models[i].Perf != nil && models[i].Perf.LoadSeconds > 0 && merged.LoadSeconds == 0 {
				merged.LoadSeconds = models[i].Perf.LoadSeconds
			}
			models[i].Perf = &merged
			changed = true
		}
	}
	if !changed {
		return st
	}
	c := *st
	c.Models = models
	return &c
}

func (m *Manager) candidateStatus(nodeUUID string) *Status {
	if nodeUUID == m.identity.UUID {
		return m.cachedLocalStatus(context.Background())
	}
	rt, ok := m.dir.Get(nodeUUID)
	if !ok {
		return nil
	}
	return rt.Status
}

// cachedLocalStatus reuses the local status for two seconds: building it
// shells out to nvidia-smi and the like, which must not run per request.
func (m *Manager) cachedLocalStatus(ctx context.Context) *Status {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.statusCache != nil && time.Since(m.statusAt) < 2*time.Second {
		return m.statusCache
	}
	st := m.LocalStatus(ctx)
	m.statusCache = &st
	m.statusAt = time.Now()
	return m.statusCache
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
		if err != nil {
			release()
			tried = append(tried, target.UUID)
			lastErr = err
			var re *routeError
			if errors.As(err, &re) {
				if re.status == 0 || re.status == http.StatusBadGateway || re.status == http.StatusServiceUnavailable {
					e.m.dir.RequestFailed(target.UUID, e.model, false)
				} else if re.modelSpecific() {
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
	if errors.As(lastErr, &re) && re.status >= 400 && re.status != http.StatusServiceUnavailable && re.status != http.StatusBadGateway {
		status = re.status
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
	stream := onToken != nil
	body := map[string]interface{}{
		"messages":    messages,
		"temperature": opts.Temperature,
		"top_p":       opts.TopP,
		"stream":      stream,
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	if opts.Seed >= 0 {
		body["seed"] = opts.Seed
	}
	if len(opts.Stop) > 0 {
		body["stop"] = opts.Stop
	}
	resp, err := e.ChatCompletion(ctx, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if !stream {
		var parsed struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			return "", err
		}
		if len(parsed.Choices) == 0 {
			return "", nil
		}
		return parsed.Choices[0].Message.Content, nil
	}
	return readOpenAISSE(resp.Body, onToken)
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

// ---- perf measurement on the response body ----

type perfSample struct {
	firstByte        time.Time
	end              time.Time
	promptTokens     int
	completionTokens int
	ok               bool
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
	if !s.ok || s.firstByte.IsZero() || s.end.IsZero() || s.completionTokens <= 0 {
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

// ---- model inventory across the cluster ----

// ModelNode says where a model lives.
type ModelNode struct {
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Loaded bool   `json:"loaded"`
	Online bool   `json:"online"`
	Local  bool   `json:"local"`
}

// ClusterModel is one model with the nodes holding it.
type ClusterModel struct {
	ID          string      `json:"id"`
	Size        int64       `json:"size"`
	Format      string      `json:"format,omitempty"`
	PipelineTag string      `json:"pipeline_tag,omitempty"`
	Category    string      `json:"category,omitempty"`
	Nodes       []ModelNode `json:"nodes"`
}

// Models returns the union of models on every member, this node included.
func (m *Manager) Models(ctx context.Context) []ClusterModel {
	byID := map[string]*ClusterModel{}
	add := func(uuid, name string, local, online bool, st *Status) {
		if st == nil {
			return
		}
		for _, ms := range st.Models {
			cm, ok := byID[ms.ID]
			if !ok {
				cm = &ClusterModel{ID: ms.ID, Size: ms.Size, Format: ms.Format, PipelineTag: ms.PipelineTag, Category: ms.Category}
				byID[ms.ID] = cm
			}
			cm.Nodes = append(cm.Nodes, ModelNode{UUID: uuid, Name: name, Loaded: ms.Loaded, Online: online, Local: local})
		}
	}
	add(m.identity.UUID, m.identity.Name, true, true, m.cachedLocalStatus(ctx))
	for _, rt := range m.dir.Snapshot() {
		mem, ok := m.store.Member(rt.UUID)
		if !ok {
			continue
		}
		name := mem.Name
		if rt.Status != nil && rt.Status.Name != "" {
			name = rt.Status.Name
		}
		add(rt.UUID, name, false, rt.Online(), rt.Status)
	}
	out := make([]ClusterModel, 0, len(byID))
	for _, cm := range byID {
		sort.Slice(cm.Nodes, func(i, j int) bool { return cm.Nodes[i].Name+cm.Nodes[i].UUID < cm.Nodes[j].Name+cm.Nodes[j].UUID })
		out = append(out, *cm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RemoteHolders lists online members (not this node) that hold a model and
// currently accept work.
func (m *Manager) RemoteHolders(model string) []string {
	var out []string
	for _, rt := range m.dir.Snapshot() {
		if !rt.Online() || rt.Status == nil || !rt.Status.AcceptWork || !rt.Status.Licensed {
			continue
		}
		if _, ok := m.store.Member(rt.UUID); !ok {
			continue
		}
		if _, ok := rt.Status.Model(model); ok && !m.dir.ModelBroken(rt.UUID, model) {
			out = append(out, rt.UUID)
		}
	}
	sort.Strings(out)
	return out
}

// Explain runs the scheduler for a hypothetical request.
func (m *Manager) Explain(ctx context.Context, model string, promptTokens, maxTokens int, affinityKey string) Explain {
	e := &clusterEngine{m: m, model: model, affinity: affinityKey}
	_, ex := e.rank(promptTokens, maxTokens)
	return ex
}

// StatusCodeForError maps a cluster routing failure onto the HTTP status the
// server should answer with.
func StatusCodeForError(err error) int {
	if code := inference.HTTPStatusCode(err); code != 0 {
		return code
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}
