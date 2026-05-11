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

type openAIEngine struct {
	baseURL            string
	chatCompletionsURL string
	modelName          string
	token              string
	client             *http.Client
}

func NewOpenAIEngine(baseURL, modelName, token string) Engine {
	baseURL = strings.TrimRight(baseURL, "/")
	return &openAIEngine{
		baseURL:            baseURL,
		chatCompletionsURL: openAIChatCompletionsURL(baseURL),
		modelName:          modelName,
		token:              strings.TrimSpace(token),
		client:             &http.Client{Timeout: 0},
	}
}

func NewOpenAICompatibleEngine(baseURL, modelName, token string) Engine {
	baseURL = strings.TrimRight(baseURL, "/")
	return &openAIEngine{
		baseURL:            baseURL,
		chatCompletionsURL: openAICompatibleChatCompletionsURL(baseURL),
		modelName:          modelName,
		token:              strings.TrimSpace(token),
		client:             &http.Client{Timeout: 0},
	}
}

func openAIChatCompletionsURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
}

func openAICompatibleChatCompletionsURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

func (e *openAIEngine) chatCompletionsEndpoint() string {
	if strings.TrimSpace(e.chatCompletionsURL) != "" {
		return e.chatCompletionsURL
	}
	return openAIChatCompletionsURL(e.baseURL)
}

func (e *openAIEngine) ChatCompletion(ctx context.Context, reqBody map[string]interface{}) (*http.Response, error) {
	// Ensure model field uses the engine's actual model name
	if reqBody == nil {
		reqBody = map[string]interface{}{}
	}
	reqBody["model"] = e.modelName
	reqBody = sanitizeOpenAIRequestBody(e.modelName, reqBody)
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

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat completion request failed: %w", err)
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
	topK := opts.TopK
	if topK <= 0 || topK == DefaultOptions().TopK {
		topK = 10
	}
	reqBody := map[string]interface{}{
		"model":              e.modelName,
		"messages":           messagesToOpenAI(messages),
		"temperature":        opts.Temperature,
		"top_p":              opts.TopP,
		"top_k":              topK,
		"repetition_penalty": 1,
		"max_tokens":         opts.MaxTokens,
		"stream":             stream,
	}
	if opts.Seed >= 0 {
		reqBody["seed"] = opts.Seed
	}
	if len(opts.Stop) > 0 {
		reqBody["stop"] = opts.Stop
	}
	reqBody = sanitizeOpenAIRequestBody(e.modelName, reqBody)

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

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", decodeOpenAIHTTPError(resp)
	}

	if stream {
		return e.handleStream(resp.Body, onToken)
	}
	return e.handleJSONResponse(resp.Body)
}

func (e *openAIEngine) handleStream(body io.Reader, onToken TokenCallback) (string, error) {
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
	var chatResp api.OpenAIChatResponse
	if err := json.NewDecoder(body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message == nil {
		return "", fmt.Errorf("no message in response")
	}
	return openAIContentString(chatResp.Choices[0].Message.Content), nil
}

func (e *openAIEngine) Close() error {
	return nil
}

func (e *openAIEngine) ModelName() string {
	return e.modelName
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

func sanitizeOpenAIRequestBody(modelName string, reqBody map[string]interface{}) map[string]interface{} {
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
	if out == nil {
		return reqBody
	}
	return out
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
