package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
)

type openAIEngine struct {
	baseURL              string
	chatCompletionsURL   string
	anthropicMessagesURL string
	embeddingsURL        string
	modelName            string
	token                string
	disableThinking      bool
	preferAnthropic      bool
	forwardHeaders       []ForwardHeader
	// extendedSamplingParams controls whether non-standard OpenAI sampling
	// parameters (top_k, repetition_penalty) are sent. Only the CSGHub cloud
	// gateway accepts them; strict third-party OpenAI providers reject
	// requests containing unknown arguments (issue #68).
	extendedSamplingParams bool
	client                 *http.Client
}

func NewOpenAIEngine(baseURL, modelName, token string) Engine {
	baseURL = strings.TrimRight(baseURL, "/")
	return &openAIEngine{
		baseURL:                baseURL,
		chatCompletionsURL:     openAIChatCompletionsURL(baseURL),
		anthropicMessagesURL:   openAIAnthropicMessagesURL(baseURL),
		embeddingsURL:          openAIEmbeddingsURL(baseURL),
		modelName:              modelName,
		token:                  strings.TrimSpace(token),
		disableThinking:        true,
		extendedSamplingParams: true,
		client:                 &http.Client{Timeout: 0},
	}
}

func NewOpenAICompatibleEngine(baseURL, modelName, token string) Engine {
	return NewOpenAICompatibleEngineWithHeaders(baseURL, modelName, token, nil)
}

func NewOpenAICompatibleEngineWithHeaders(baseURL, modelName, token string, headers []ForwardHeader) Engine {
	baseURL = strings.TrimRight(baseURL, "/")
	return &openAIEngine{
		baseURL:              baseURL,
		chatCompletionsURL:   openAICompatibleChatCompletionsURL(baseURL),
		anthropicMessagesURL: openAICompatibleAnthropicMessagesURL(baseURL),
		embeddingsURL:        openAICompatibleEmbeddingsURL(baseURL),
		modelName:            modelName,
		token:                strings.TrimSpace(token),
		disableThinking:      false,
		preferAnthropic:      true,
		forwardHeaders:       append([]ForwardHeader(nil), headers...),
		client:               &http.Client{Timeout: 0},
	}
}

func openAIChatCompletionsURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
}

func openAIEmbeddingsURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/embeddings"
}

func openAIAnthropicMessagesURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/messages"
}

func openAICompatibleChatCompletionsURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

func openAICompatibleAnthropicMessagesURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/messages"
}

func openAICompatibleEmbeddingsURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/embeddings"
}

func (e *openAIEngine) chatCompletionsEndpoint() string {
	if strings.TrimSpace(e.chatCompletionsURL) != "" {
		return e.chatCompletionsURL
	}
	return openAIChatCompletionsURL(e.baseURL)
}

func (e *openAIEngine) embeddingsEndpoint() string {
	if strings.TrimSpace(e.embeddingsURL) != "" {
		return e.embeddingsURL
	}
	return openAIEmbeddingsURL(e.baseURL)
}

func (e *openAIEngine) anthropicMessagesEndpoint() string {
	if strings.TrimSpace(e.anthropicMessagesURL) != "" {
		return e.anthropicMessagesURL
	}
	return openAIAnthropicMessagesURL(e.baseURL)
}

func (e *openAIEngine) ChatCompletion(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	if reqBody == nil {
		reqBody = map[string]interface{}{}
	}
	reqBody["model"] = e.modelName
	reqBody = sanitizeOpenAIRequestBody(e.modelName, e.disableThinking, reqBody)
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.chatCompletionsEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if stream, _ := reqBody["stream"].(bool); stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	e.applyOpenAIForwardHeaders(req)

	resp, err := e.client.Do(req)
	if err != nil {
		logUpstreamInference("openai_chat_completions", e.chatCompletionsEndpoint(), 0)
		return nil, fmt.Errorf("chat completion request failed: %w", err)
	}
	logUpstreamInference("openai_chat_completions", e.chatCompletionsEndpoint(), resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, decodeOpenAIHTTPError(resp)
	}
	return resp, nil
}

func (e *openAIEngine) AnthropicMessages(ctx context.Context, reqBody map[string]interface{}, headers http.Header) (*http.Response, error) {
	bodyMap := cloneOpenAIRequestBody(reqBody)
	bodyMap["model"] = e.modelName
	delete(bodyMap, "source")
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("marshaling Anthropic messages request: %w", err)
	}

	endpoint := e.anthropicMessagesEndpoint()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating Anthropic messages request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyAnthropicForwardHeaders(req.Header, headers)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
		req.Header.Set("X-Api-Key", e.token)
	}
	e.applyOpenAIForwardHeaders(req)

	resp, err := e.client.Do(req)
	if err != nil {
		logUpstreamInference("anthropic_messages", endpoint, 0)
		return nil, fmt.Errorf("Anthropic messages request failed: %w", err)
	}
	logUpstreamInference("anthropic_messages", endpoint, resp.StatusCode)
	return resp, nil
}

func (e *openAIEngine) PrefersNativeAnthropicMessages() bool {
	return e.preferAnthropic
}

func applyAnthropicForwardHeaders(dst, src http.Header) {
	for name, values := range src {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower != "accept" && lower != "user-agent" &&
			!strings.HasPrefix(lower, "anthropic-") &&
			!strings.HasPrefix(lower, "x-stainless-") {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				dst.Add(name, value)
			}
		}
	}
}

func logUpstreamInference(protocol, endpoint string, status int) {
	log.Printf("INFERENCE upstream protocol=%s url=%q status=%d", protocol, sanitizedUpstreamURL(endpoint), status)
}

func sanitizedUpstreamURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "<invalid>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func (e *openAIEngine) Embeddings(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	if reqBody == nil {
		reqBody = map[string]interface{}{}
	}
	reqBody["model"] = e.modelName
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.embeddingsEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	e.applyOpenAIForwardHeaders(req)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, decodeOpenAIHTTPError(resp)
	}
	return resp, nil
}

func (e *openAIEngine) Generate(ctx context.Context, prompt string, opts Options, onToken TokenCallback) (string, error) {
	messages := []Message{{Role: "user", Content: prompt}}
	return e.Chat(ctx, messages, opts, onToken)
}

func (e *openAIEngine) Chat(ctx context.Context, messages []Message, opts Options, onToken TokenCallback) (string, error) {
	stream := onToken != nil
	reqBody := map[string]interface{}{
		"model":       e.modelName,
		"messages":    messagesToOpenAI(messages),
		"temperature": opts.Temperature,
		"top_p":       opts.TopP,
		"stream":      stream,
	}
	if e.extendedSamplingParams {
		topK := opts.TopK
		if topK <= 0 || topK == DefaultOptions().TopK {
			topK = 10
		}
		reqBody["top_k"] = topK
		reqBody["repetition_penalty"] = 1
	}
	if opts.MaxTokens > 0 {
		reqBody["max_tokens"] = opts.MaxTokens
	}
	if opts.Seed >= 0 {
		reqBody["seed"] = opts.Seed
	}
	if len(opts.Stop) > 0 {
		reqBody["stop"] = opts.Stop
	}
	if stream {
		reqBody["stream_options"] = map[string]interface{}{"include_usage": true}
	}
	disableThinking := e.disableThinking || opts.DisableThinking
	reqBody = sanitizeOpenAIRequestBody(e.modelName, disableThinking, reqBody)
	if opts.DisableThinking {
		reqBody = applyExplicitThinkingDisableControls(e.modelName, reqBody)
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.chatCompletionsEndpoint(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	e.applyOpenAIForwardHeaders(req)

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", decodeOpenAIHTTPError(resp)
	}

	collector := cacheUsageCollectorFromContext(ctx)
	if stream {
		return e.handleStreamWithCacheUsage(resp.Body, onToken, collector)
	}
	return e.handleJSONResponseWithCacheUsage(resp.Body, collector)
}

func (e *openAIEngine) handleStream(body io.Reader, onToken TokenCallback) (string, error) {
	return e.handleStreamWithCacheUsage(body, onToken, nil)
}

func (e *openAIEngine) handleStreamWithCacheUsage(body io.Reader, onToken TokenCallback, collector *CacheUsageCollector) (string, error) {
	scanner := bufio.NewScanner(body)
	var full strings.Builder
	reasoningOpen := false

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			if reasoningOpen {
				full.WriteString("</think>")
				onToken("</think>")
			}
			break
		}

		var chatResp api.OpenAIChatResponse
		if err := json.Unmarshal([]byte(data), &chatResp); err != nil {
			continue
		}
		recordOpenAICacheUsage(collector, chatResp.Usage)
		if len(chatResp.Choices) == 0 || chatResp.Choices[0].Delta == nil {
			continue
		}

		delta := chatResp.Choices[0].Delta
		if reasoning := openAIContentString(delta.ReasoningContent); reasoning != "" {
			if !reasoningOpen {
				reasoningOpen = true
				full.WriteString("<think>")
				onToken("<think>")
			}
			full.WriteString(reasoning)
			onToken(reasoning)
		}

		if token := openAIContentString(delta.Content); token != "" {
			if reasoningOpen {
				reasoningOpen = false
				full.WriteString("</think>")
				onToken("</think>")
			}
			full.WriteString(token)
			onToken(token)
		}
	}

	return full.String(), scanner.Err()
}

func (e *openAIEngine) handleJSONResponse(body io.Reader) (string, error) {
	return e.handleJSONResponseWithCacheUsage(body, nil)
}

func (e *openAIEngine) handleJSONResponseWithCacheUsage(body io.Reader, collector *CacheUsageCollector) (string, error) {
	var chatResp api.OpenAIChatResponse
	if err := json.NewDecoder(body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	recordOpenAICacheUsage(collector, chatResp.Usage)
	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message == nil {
		return "", fmt.Errorf("no message in response")
	}
	return openAIContentString(chatResp.Choices[0].Message.Content), nil
}

func recordOpenAICacheUsage(collector *CacheUsageCollector, usage api.OpenAIUsage) {
	if collector == nil {
		return
	}
	read, write := int64(usage.CachedTokens), int64(0)
	reported := usage.CachedTokens != 0
	if usage.PromptTokensDetails != nil {
		reported = true
		read = int64(usage.PromptTokensDetails.CachedTokens)
		write = int64(usage.PromptTokensDetails.WriteCachedTokens)
	}
	if !reported {
		return
	}
	prompt := int64(usage.PromptTokens)
	switch {
	case prompt < 0 && read > 0:
		prompt += 2 * read
	case prompt >= 0 && read > 0 && prompt < read:
		prompt += read
	}
	collector.add(read, write, prompt)
}

// SupportsNativeToolStreaming reports that cloud and third-party
// OpenAI-compatible backends return standard streaming tool-call deltas,
// so tool requests do not need local aggregation and normalization.
func (e *openAIEngine) SupportsNativeToolStreaming() bool {
	return true
}

func (e *openAIEngine) Close() error {
	return nil
}

func (e *openAIEngine) ModelName() string {
	return e.modelName
}

type ForwardHeader struct {
	Name  string
	Value string
}

func (e *openAIEngine) applyOpenAIForwardHeaders(req *http.Request) {
	if req == nil {
		return
	}
	ApplyOpenAIForwardHeaders(req, e.forwardHeaders)
}

// ApplyOpenAIForwardHeaders adds configured compatibility headers to an
// upstream request. Host is handled through Request.Host because net/http
// does not send Header["Host"] as the authority.
func ApplyOpenAIForwardHeaders(req *http.Request, headers []ForwardHeader) {
	if req == nil {
		return
	}
	for _, header := range headers {
		name := strings.TrimSpace(header.Name)
		value := strings.TrimSpace(header.Value)
		if name == "" || value == "" {
			continue
		}
		if strings.EqualFold(name, "Host") {
			// net/http treats Host specially; setting Header["Host"] does not
			// change the authority sent on the wire.
			req.Host = value
			continue
		}
		addHeaderPreservingName(req.Header, name, value)
	}
}

func addHeaderPreservingName(headers http.Header, name, value string) {
	var existingValues []string
	for existingName := range headers {
		if strings.EqualFold(existingName, name) {
			existingValues = append(existingValues, headers[existingName]...)
			if existingName != name {
				delete(headers, existingName)
			}
		}
	}
	headers[name] = append(existingValues, value)
}

func decodeOpenAIHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	message := strings.TrimSpace(string(body))
	if len(body) > 0 {
		var payload struct {
			Error interface{} `json:"error"`
			Msg   string      `json:"msg"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			switch v := payload.Error.(type) {
			case string:
				if strings.TrimSpace(v) != "" {
					message = strings.TrimSpace(v)
				}
			case map[string]interface{}:
				if msg, ok := v["message"].(string); ok && strings.TrimSpace(msg) != "" {
					message = strings.TrimSpace(msg)
				}
			}
			if strings.TrimSpace(payload.Msg) != "" {
				message = strings.TrimSpace(payload.Msg)
			}
		}
	}
	if message == "" {
		message = resp.Status
	}
	return NewHTTPStatusError(resp.StatusCode, message)
}

func sanitizeOpenAIRequestBody(modelName string, disableThinking bool, reqBody map[string]interface{}) map[string]interface{} {
	if len(reqBody) == 0 {
		return reqBody
	}
	var out map[string]interface{}
	if openAIModelRequiresSingleSamplingParam(modelName) {
		if _, hasTemp := reqBody["temperature"]; hasTemp {
			if _, hasTopP := reqBody["top_p"]; hasTopP {
				out = cloneOpenAIRequestBody(reqBody)
				delete(out, "top_p")
			}
		}
	}
	if openAIModelRequiresTemperatureOne(modelName) {
		if _, hasTemp := reqBody["temperature"]; hasTemp {
			if out == nil {
				out = cloneOpenAIRequestBody(reqBody)
			}
			out["temperature"] = 1
		}
		if _, hasTopP := reqBody["top_p"]; hasTopP {
			if out == nil {
				out = cloneOpenAIRequestBody(reqBody)
			}
			out["top_p"] = 0.95
		}
	}
	if openAIModelRequiresToolCallReasoningContent(modelName) {
		messages := reqBody["messages"]
		if out != nil {
			messages = out["messages"]
		}
		if normalized, changed := normalizeToolCallReasoningContentMessages(messages); changed {
			if out == nil {
				out = cloneOpenAIRequestBody(reqBody)
			}
			out["messages"] = normalized
		}
	}
	if disableThinking && openAIModelSupportsDisableThinking(modelName) {
		target := reqBody
		if out != nil {
			target = out
		}
		if shouldSetDisableThinkingFlag(target) {
			if out == nil {
				out = cloneOpenAIRequestBody(reqBody)
			}
			out["enable_thinking"] = false
		}
	}
	if out == nil {
		return reqBody
	}
	return out
}

func applyExplicitThinkingDisableControls(modelName string, reqBody map[string]interface{}) map[string]interface{} {
	if len(reqBody) == 0 {
		return reqBody
	}
	out := cloneOpenAIRequestBody(reqBody)
	changed := applyThinkingDisableControls(modelName, out)
	if openAIModelRequiresTemperaturePointSixWhenThinkingDisabled(modelName) {
		out["temperature"] = 0.6
		changed = true
	}
	if !changed {
		return reqBody
	}
	return out
}

func applyThinkingDisableControls(modelName string, reqBody map[string]interface{}) bool {
	if reqBody == nil {
		return false
	}
	changed := false
	if openAIModelUsesThinkingTypeDisabled(modelName) && !hasThinkingTypeDisabled(reqBody) {
		reqBody["thinking"] = map[string]interface{}{"type": "disabled"}
		changed = true
	}
	if openAIModelSupportsEnableThinkingFalse(modelName) && shouldSetDisableThinkingFlag(reqBody) {
		reqBody["enable_thinking"] = false
		changed = true
	}
	return changed
}

func hasThinkingTypeDisabled(reqBody map[string]interface{}) bool {
	thinking, ok := reqBody["thinking"].(map[string]interface{})
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(thinking["type"])), "disabled")
}

func openAIModelUsesThinkingTypeDisabled(modelName string) bool {
	modelName = strings.TrimSpace(strings.ToLower(modelName))
	return strings.HasPrefix(modelName, "glm-") ||
		strings.HasPrefix(modelName, "kimi-") ||
		strings.HasPrefix(modelName, "moonshot-") ||
		strings.Contains(modelName, "deepseek-v4") ||
		strings.HasPrefix(modelName, "mimo-")
}

func openAIModelSupportsEnableThinkingFalse(modelName string) bool {
	modelName = strings.TrimSpace(strings.ToLower(modelName))
	return strings.HasPrefix(modelName, "qwen3") ||
		strings.HasPrefix(modelName, "qwen/qwen3") ||
		strings.HasPrefix(modelName, "qwq") ||
		strings.HasPrefix(modelName, "qwen/qwq")
}

func openAIModelSupportsDisableThinking(modelName string) bool {
	return openAIModelSupportsEnableThinkingFalse(modelName)
}

func openAIModelRequiresTemperaturePointSixWhenThinkingDisabled(modelName string) bool {
	modelName = strings.TrimSpace(strings.ToLower(modelName))
	return strings.HasPrefix(modelName, "glm-")
}

func cloneOpenAIRequestBody(reqBody map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(reqBody))
	for key, value := range reqBody {
		out[key] = value
	}
	return out
}

func openAIModelRequiresSingleSamplingParam(modelName string) bool {
	modelName = strings.TrimSpace(strings.ToLower(modelName))
	return strings.HasPrefix(modelName, "claude")
}

func openAIModelRequiresTemperatureOne(modelName string) bool {
	modelName = strings.TrimSpace(strings.ToLower(modelName))
	return strings.HasPrefix(modelName, "kimi-") || strings.HasPrefix(modelName, "moonshot-")
}

func openAIModelRequiresToolCallReasoningContent(modelName string) bool {
	modelName = strings.TrimSpace(strings.ToLower(modelName))
	return strings.HasPrefix(modelName, "kimi-") ||
		strings.HasPrefix(modelName, "moonshot-") ||
		strings.Contains(modelName, "deepseek-v4")
}

func shouldSetDisableThinkingFlag(reqBody map[string]interface{}) bool {
	if reqBody == nil {
		return false
	}
	if value, exists := reqBody["enable_thinking"]; exists {
		if enabled, ok := value.(bool); ok {
			return enabled
		}
		return false
	}
	return true
}

func normalizeToolCallReasoningContentMessages(messages interface{}) (interface{}, bool) {
	switch v := messages.(type) {
	case []map[string]interface{}:
		out := make([]map[string]interface{}, len(v))
		changed := false
		for i, msg := range v {
			next, msgChanged := normalizeToolCallReasoningContentMessageMap(msg)
			out[i] = next
			changed = changed || msgChanged
		}
		if changed {
			return out, true
		}
	case []interface{}:
		out := make([]interface{}, len(v))
		changed := false
		for i, item := range v {
			msg, ok := item.(map[string]interface{})
			if !ok {
				out[i] = item
				continue
			}
			next, msgChanged := normalizeToolCallReasoningContentMessageMap(msg)
			out[i] = next
			changed = changed || msgChanged
		}
		if changed {
			return out, true
		}
	case []api.Message:
		out := make([]map[string]interface{}, len(v))
		changed := false
		for i, msg := range v {
			next := map[string]interface{}{
				"role":    msg.Role,
				"content": msg.Content,
			}
			if len(msg.ToolCalls) > 0 {
				next["tool_calls"] = msg.ToolCalls
			}
			if msg.ToolCallID != "" {
				next["tool_call_id"] = msg.ToolCallID
			}
			if msg.ToolName != "" {
				next["name"] = msg.ToolName
			}
			if msg.ReasoningContent != "" {
				next["reasoning_content"] = msg.ReasoningContent
			}
			normalized, msgChanged := normalizeToolCallReasoningContentMessageMap(next)
			out[i] = normalized
			changed = changed || msgChanged
		}
		if changed {
			return out, true
		}
	}
	return messages, false
}

func normalizeToolCallReasoningContentMessageMap(msg map[string]interface{}) (map[string]interface{}, bool) {
	if strings.TrimSpace(fmt.Sprint(msg["role"])) != "assistant" {
		return msg, false
	}
	if _, ok := msg["tool_calls"]; !ok {
		return msg, false
	}
	if _, ok := msg["reasoning_content"]; ok {
		return msg, false
	}
	out := make(map[string]interface{}, len(msg)+1)
	for key, value := range msg {
		out[key] = value
	}
	out["reasoning_content"] = ""
	return out, true
}

func messagesToOpenAI(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		next := map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		// Preserve reasoning_content for thinking models (e.g., deepseek-v4-pro)
		if msg.ReasoningContent != "" {
			next["reasoning_content"] = msg.ReasoningContent
		}
		out = append(out, next)
	}
	return out
}

func openAIContentString(content interface{}) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		buf, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(buf)
	}
}
