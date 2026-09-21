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
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/correlation"
	"github.com/opencsgs/csglite/internal/inference"
)

type nodeEngine struct {
	m       *Manager
	mem     Member
	model   string
	toolStr bool
	// hop is the entry node's UUID sent in RoutedHeader.
	hop string
}

func (e *nodeEngine) ModelName() string { return e.model }

func (e *nodeEngine) Close() error { return nil }

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
	client := e.m.peers.get(e.mem.UUID, e.mem.CertFingerprint)
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
			return nil, &routeError{
				status:     resp.StatusCode,
				body:       string(raw),
				err:        fmt.Errorf("node %s answered %d", e.mem.Name, resp.StatusCode),
				retryAfter: resp.Header.Get("Retry-After"),
			}
		}
		e.m.dir.RequestSucceeded(e.mem.UUID)
		e.m.store.RecordAddress(e.mem.UUID, addr)
		return resp, nil
	}
	return nil, lastErr
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
	return chatOverCompletions(ctx, e, messages, opts, onToken)
}

func (m *Manager) newNodeEngine(mem Member, model string, toolStreaming bool) *nodeEngine {
	return &nodeEngine{m: m, mem: mem, model: model, toolStr: toolStreaming, hop: m.identity.UUID}
}

// routeError classifies a failed hop for failover decisions.
type routeError struct {
	status int
	body   string
	err    error
	// retryAfter is the peer's Retry-After header on a 429, so the caller can
	// hold that node out for as long as it asked rather than trying it again
	// on the very next request.
	retryAfter string
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

// chatOverCompletions answers the Ollama-style Chat call on top of an
// OpenAI-compatible /v1/chat/completions proxier. The single-node engine and
// the cluster router both work this way, so the body building, the non-stream
// decode and the stream reader live here once.
//
// num_ctx travels with the request: whichever node ends up serving it may be
// the one that loads the model, and it can only honour a requested context
// size if it is told. The cluster router used to leave it out, so an
// Ollama-style chat routed to a peer silently lost it.
func chatOverCompletions(ctx context.Context, proxier inference.ChatCompletionProxier, messages []inference.Message, opts inference.Options, onToken inference.TokenCallback) (string, error) {
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
	resp, err := proxier.ChatCompletion(ctx, body)
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
