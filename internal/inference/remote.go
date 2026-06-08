package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

// remoteEngine implements Engine by forwarding requests to a csghub-lite
// API server over HTTP. The server manages the actual llama-server subprocess
// and its lifecycle (keep-alive, eviction).
type remoteEngine struct {
	baseURL     string
	modelName   string
	client      *http.Client
	numCtx      int
	numParallel int
	nGPULayers  int
	cacheTypeK  string
	cacheTypeV  string
	dtype       string
}

// NewRemoteEngine creates an Engine that delegates to a running csghub-lite server.
func NewRemoteEngine(baseURL, modelName string, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) Engine {
	return &remoteEngine{
		baseURL:     strings.TrimRight(baseURL, "/"),
		modelName:   modelName,
		client:      &http.Client{Timeout: 0},
		numCtx:      numCtx,
		numParallel: numParallel,
		nGPULayers:  nGPULayers,
		cacheTypeK:  cacheTypeK,
		cacheTypeV:  cacheTypeV,
		dtype:       dtype,
	}
}

func (e *remoteEngine) ChatCompletion(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat completion request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(errBody))
	}
	return resp, nil
}

func (e *remoteEngine) Embeddings(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	if reqBody == nil {
		reqBody = map[string]interface{}{}
	}
	reqBody["model"] = e.modelName
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(errBody))
	}
	return resp, nil
}

func (e *remoteEngine) Generate(ctx context.Context, prompt string, opts Options, onToken TokenCallback) (string, error) {
	messages := []Message{
		{Role: "user", Content: prompt},
	}
	return e.Chat(ctx, messages, opts, onToken)
}

func (e *remoteEngine) Chat(ctx context.Context, messages []Message, opts Options, onToken TokenCallback) (string, error) {
	stream := onToken != nil

	apiMessages := make([]api.Message, len(messages))
	for i, m := range messages {
		apiMessages[i] = api.Message{Role: m.Role, Content: m.Content}
	}

	reqBody := api.ChatRequest{
		Model:    e.modelName,
		Messages: apiMessages,
		Stream:   &stream,
		Options: &api.ModelOptions{
			Temperature: opts.Temperature,
			TopP:        opts.TopP,
			TopK:        opts.TopK,
		},
	}
	if opts.MaxTokens > 0 {
		reqBody.Options.MaxTokens = opts.MaxTokens
	}
	if e.numCtx > 0 {
		reqBody.Options.NumCtx = e.numCtx
	}
	if e.numParallel > 0 {
		reqBody.Options.NumParallel = e.numParallel
	}
	if e.nGPULayers >= 0 {
		reqBody.Options.NGPULayers = &e.nGPULayers
	}
	if e.cacheTypeK != "" {
		reqBody.Options.CacheTypeK = e.cacheTypeK
	}
	if e.cacheTypeV != "" {
		reqBody.Options.CacheTypeV = e.cacheTypeV
	}
	if e.dtype != "" {
		reqBody.Options.DType = e.dtype
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-CSGHUB-Stream", "sse")

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("server error %d: %s", resp.StatusCode, string(errBody))
	}

	if stream {
		return e.handleSSEStream(resp.Body, onToken)
	}
	return e.handleJSONResponse(resp.Body)
}

func (e *remoteEngine) handleSSEStream(body io.Reader, onToken TokenCallback) (string, error) {
	scanner := bufio.NewScanner(body)
	var full strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chatResp api.ChatResponse
		if err := json.Unmarshal([]byte(data), &chatResp); err != nil {
			continue
		}
		if chatResp.Done {
			break
		}
		if chatResp.Message != nil {
			if s, ok := chatResp.Message.Content.(string); ok && s != "" {
				full.WriteString(s)
				onToken(s)
			}
		}
	}

	return full.String(), scanner.Err()
}

func (e *remoteEngine) handleJSONResponse(body io.Reader) (string, error) {
	var chatResp api.ChatResponse
	if err := json.NewDecoder(body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	if chatResp.Message == nil {
		return "", fmt.Errorf("no message in response")
	}
	if s, ok := chatResp.Message.Content.(string); ok {
		return s, nil
	}
	return "", nil
}

func (e *remoteEngine) Close() error {
	return nil
}

func (e *remoteEngine) ModelName() string {
	return e.modelName
}
