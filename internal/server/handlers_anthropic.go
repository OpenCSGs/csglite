package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

// POST /v1/messages -- Anthropic-compatible messages API
func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	var req api.AnthropicMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Model == "" {
		writeAnthropicError(w, http.StatusBadRequest, "model is required")
		return
	}

	opts := inference.DefaultOptions()
	if req.Temperature != nil {
		opts.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		opts.TopP = *req.TopP
	}
	if req.MaxTokens > 0 {
		opts.MaxTokens = req.MaxTokens
	}
	if len(req.StopSequences) > 0 {
		opts.Stop = req.StopSequences
	}

	eng, err := s.getChatEngine(r.Context(), req.Model, "", s.anthropicPreferredNumCtx(req.Model), 0, -1, "", "", "")
	if err != nil {
		writeAnthropicInferenceError(w, err)
		return
	}
	defer s.touchEngine(req.Model)

	inputTokens := countAnthropicRequestTokens(req)
	id := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	if anthropicRequestHasToolFeatures(req) {
		s.handleAnthropicMessagesWithTools(w, r, req, eng, opts, inputTokens, id)
		return
	}
	if proxy, ok := eng.(inference.ChatCompletionProxier); ok {
		s.handleAnthropicMessagesProxy(w, r, req, proxy, opts, inputTokens, id)
		return
	}

	messages := anthropicMessagesToInference(req)

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		writeAnthropicMessageStart(w, id, req.Model, inputTokens)
		writeAnthropicSSE(w, "content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]interface{}{
				"type": "text",
				"text": "",
			},
		})

		var full strings.Builder
		onToken := func(token string) {
			full.WriteString(token)
			writeAnthropicSSE(w, "content_block_delta", map[string]interface{}{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]interface{}{
					"type": "text_delta",
					"text": token,
				},
			})
		}

		if _, err := eng.Chat(r.Context(), messages, opts, onToken); err != nil {
			writeAnthropicSSE(w, "error", anthropicErrorPayload(inference.HTTPErrorMessage(err)))
			return
		}

		outputTokens := estimateAnthropicTokens(full.String())
		writeAnthropicSSE(w, "content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": 0,
		})
		writeAnthropicSSE(w, "message_delta", map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason":   "end_turn",
				"stop_sequence": nil,
			},
			"usage": map[string]interface{}{
				"output_tokens": outputTokens,
			},
		})
		writeAnthropicSSE(w, "message_stop", map[string]interface{}{
			"type": "message_stop",
		})
		return
	}

	response, err := eng.Chat(r.Context(), messages, opts, nil)
	if err != nil {
		writeAnthropicInferenceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, buildAnthropicMessageResponse(id, req.Model, response, inputTokens))
}

func (s *Server) handleAnthropicMessagesProxy(
	w http.ResponseWriter,
	r *http.Request,
	req api.AnthropicMessageRequest,
	proxy inference.ChatCompletionProxier,
	opts inference.Options,
	inputTokens int,
	id string,
) {
	reqBody, err := anthropicRequestToProxyBody(req, opts, false)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := proxy.ChatCompletion(r.Context(), reqBody)
	if err != nil {
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeAnthropicSSE(w, "error", anthropicErrorPayloadFromInferenceError(err))
			return
		}
		writeAnthropicInferenceError(w, err)
		return
	}
	defer resp.Body.Close()

	var openAIResp api.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		message := "decoding response: " + err.Error()
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeAnthropicSSE(w, "error", anthropicErrorPayloadWithType("api_error", message))
			return
		}
		writeAnthropicErrorWithType(w, http.StatusInternalServerError, "api_error", message)
		return
	}
	if openAIResp.Model == "" {
		openAIResp.Model = req.Model
	}

	anthropicResp, err := anthropicMessageResponseFromOpenAI(id, req.Model, openAIResp, inputTokens)
	if err != nil {
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeAnthropicSSE(w, "error", anthropicErrorPayloadWithType("api_error", err.Error()))
			return
		}
		writeAnthropicErrorWithType(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	if !req.Stream {
		writeJSON(w, http.StatusOK, anthropicResp)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	writeAnthropicStreamedMessage(w, anthropicResp)
}

func (s *Server) handleAnthropicMessagesWithTools(
	w http.ResponseWriter,
	r *http.Request,
	req api.AnthropicMessageRequest,
	eng inference.Engine,
	opts inference.Options,
	inputTokens int,
	id string,
) {
	proxy, ok := eng.(inference.ChatCompletionProxier)
	if !ok {
		writeAnthropicErrorWithType(w, http.StatusBadRequest, "invalid_request_error", "selected model backend does not support tool calling")
		return
	}

	reqBody, err := anthropicRequestToProxyBody(req, opts, false)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := proxy.ChatCompletion(r.Context(), reqBody)
	if err != nil {
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeAnthropicSSE(w, "error", anthropicErrorPayloadFromInferenceError(err))
			return
		}
		writeAnthropicInferenceError(w, err)
		return
	}
	defer resp.Body.Close()

	var openAIResp api.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		message := "decoding tool response: " + err.Error()
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeAnthropicSSE(w, "error", anthropicErrorPayloadWithType("api_error", message))
			return
		}
		writeAnthropicErrorWithType(w, http.StatusInternalServerError, "api_error", message)
		return
	}

	openAIResp = normalizeOpenAIToolResponse(openAIResp, anthropicToolsToOpenAI(req.Tools))
	if openAIResp.Model == "" {
		openAIResp.Model = req.Model
	}

	anthropicResp, err := anthropicMessageResponseFromOpenAI(id, req.Model, openAIResp, inputTokens)
	if err != nil {
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeAnthropicSSE(w, "error", anthropicErrorPayloadWithType("api_error", err.Error()))
			return
		}
		writeAnthropicErrorWithType(w, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	if !req.Stream {
		writeJSON(w, http.StatusOK, anthropicResp)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	writeAnthropicStreamedMessage(w, anthropicResp)
}

// POST /v1/messages/count_tokens -- Anthropic-compatible token counting
func (s *Server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	var req api.AnthropicMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	writeJSON(w, http.StatusOK, api.AnthropicCountTokensResponse{
		InputTokens: countAnthropicRequestTokens(req),
	})
}

func buildAnthropicMessageResponse(id, modelID, text string, inputTokens int) api.AnthropicMessageResponse {
	return api.AnthropicMessageResponse{
		ID:   id,
		Type: "message",
		Role: "assistant",
		Content: []api.AnthropicContentBlock{{
			Type: "text",
			Text: text,
		}},
		Model:        modelID,
		StopReason:   "end_turn",
		StopSequence: nil,
		Usage: api.AnthropicUsage{
			InputTokens:  inputTokens,
			OutputTokens: estimateAnthropicTokens(text),
		},
	}
}

func anthropicMessageResponseFromOpenAI(id, modelID string, openAIResp api.OpenAIChatResponse, fallbackInputTokens int) (api.AnthropicMessageResponse, error) {
	if len(openAIResp.Choices) == 0 || openAIResp.Choices[0].Message == nil {
		return api.AnthropicMessageResponse{}, fmt.Errorf("no choices in tool response")
	}

	choice := openAIResp.Choices[0]
	blocks := anthropicContentBlocksFromOpenAIMessage(choice.Message)
	inputTokens := fallbackInputTokens
	if openAIResp.Usage.PromptTokens > 0 {
		inputTokens = openAIResp.Usage.PromptTokens
	}
	outputTokens := openAIResp.Usage.CompletionTokens
	if outputTokens == 0 {
		outputTokens = estimateAnthropicTokens(anthropicContentBlocksText(blocks))
	}

	return api.AnthropicMessageResponse{
		ID:           id,
		Type:         "message",
		Role:         "assistant",
		Content:      blocks,
		Model:        modelID,
		StopReason:   anthropicStopReason(choice),
		StopSequence: nil,
		Usage: api.AnthropicUsage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		},
	}, nil
}

func anthropicRequestHasToolFeatures(req api.AnthropicMessageRequest) bool {
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return true
	}
	for _, item := range req.Messages {
		if anthropicMessageHasToolBlocks(item.Content) {
			return true
		}
	}
	return false
}

func anthropicMessageHasToolBlocks(content interface{}) bool {
	switch value := content.(type) {
	case []interface{}:
		for _, item := range value {
			if anthropicMessageHasToolBlocks(item) {
				return true
			}
		}
	case map[string]interface{}:
		switch stringValue(value["type"]) {
		case "tool_use", "tool_result":
			return true
		}
	}
	return false
}

func anthropicMessagesToInference(req api.AnthropicMessageRequest) []inference.Message {
	messages := make([]inference.Message, 0, len(req.Messages)+1)
	if system := anthropicContentText(req.System); system != "" {
		messages = append(messages, inference.Message{Role: "system", Content: system})
	}
	for _, item := range req.Messages {
		text, reasoning := anthropicMessageTextAndReasoning(item.Role, item.Content)
		messages = append(messages, inference.Message{
			Role:             item.Role,
			Content:          text,
			ReasoningContent: reasoning,
		})
	}
	return messages
}

func anthropicRequestToProxyBody(req api.AnthropicMessageRequest, opts inference.Options, stream bool) (map[string]interface{}, error) {
	messages, err := anthropicMessagesToOpenAI(req)
	if err != nil {
		return nil, err
	}

	body := map[string]interface{}{
		"model":       req.Model,
		"messages":    messages,
		"temperature": opts.Temperature,
		"top_p":       opts.TopP,
		"max_tokens":  opts.MaxTokens,
		"stream":      stream,
	}
	if len(opts.Stop) > 0 {
		body["stop"] = opts.Stop
	}
	if tools := anthropicToolsToOpenAI(req.Tools); len(tools) > 0 {
		body["tools"] = tools
	}
	if toolChoice := anthropicToolChoiceToOpenAI(req.ToolChoice); toolChoice != nil {
		body["tool_choice"] = toolChoice
	}
	return body, nil
}

func anthropicMessagesToOpenAI(req api.AnthropicMessageRequest) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(req.Messages)+1)
	if system := anthropicContentText(req.System); system != "" {
		out = append(out, map[string]interface{}{
			"role":    "system",
			"content": system,
		})
	}

	pendingToolNames := make(map[string]string)
	for i, item := range req.Messages {
		entries, err := anthropicMessageToOpenAIEntries(item, i, pendingToolNames)
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
	return out, nil
}

func anthropicMessageToOpenAIEntries(message api.AnthropicMessage, messageIndex int, pendingToolNames map[string]string) ([]map[string]interface{}, error) {
	switch value := message.Content.(type) {
	case nil, string:
		text := anthropicContentText(value)
		if text == "" {
			return nil, nil
		}
		return []map[string]interface{}{{
			"role":    message.Role,
			"content": text,
		}}, nil
	case []interface{}:
		return anthropicContentBlockEntriesToOpenAI(message.Role, value, messageIndex, pendingToolNames)
	case map[string]interface{}:
		return anthropicContentBlockEntriesToOpenAI(message.Role, []interface{}{value}, messageIndex, pendingToolNames)
	default:
		text := anthropicContentText(value)
		if text == "" {
			return nil, nil
		}
		return []map[string]interface{}{{
			"role":    message.Role,
			"content": text,
		}}, nil
	}
}

func anthropicContentBlockEntriesToOpenAI(role string, blocks []interface{}, messageIndex int, pendingToolNames map[string]string) ([]map[string]interface{}, error) {
	entries := make([]map[string]interface{}, 0, len(blocks))
	textParts := make([]string, 0, len(blocks))
	reasoningParts := make([]string, 0)
	assistantToolCalls := make([]map[string]interface{}, 0)
	assistantToolIndex := 0

	flushText := func() {
		if len(textParts) == 0 {
			return
		}
		entries = append(entries, map[string]interface{}{
			"role":    role,
			"content": strings.Join(textParts, "\n"),
		})
		textParts = nil
	}

	for _, raw := range blocks {
		switch block := raw.(type) {
		case string:
			if strings.TrimSpace(block) != "" {
				textParts = append(textParts, block)
			}
		case map[string]interface{}:
			switch stringValue(block["type"]) {
			case "", "text":
				if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
					textParts = append(textParts, text)
				}
			case "thinking":
				if thinking, _ := block["thinking"].(string); strings.TrimSpace(thinking) != "" {
					if role == "assistant" {
						reasoningParts = append(reasoningParts, thinking)
					} else {
						textParts = append(textParts, thinking)
					}
				}
			case "tool_use":
				if role != "assistant" {
					return nil, fmt.Errorf("tool_use blocks must be sent in assistant messages")
				}
				name := stringValue(block["name"])
				if name == "" {
					return nil, fmt.Errorf("tool_use block is missing name")
				}
				callID := anthropicToolUseID(stringValue(block["id"]), messageIndex, assistantToolIndex)
				assistantToolIndex++
				pendingToolNames[callID] = name
				input := parseToolArguments(block["input"])
				if input == nil {
					input = map[string]interface{}{}
				}
				assistantToolCalls = append(assistantToolCalls, map[string]interface{}{
					"id":   callID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": toolArgumentsJSONString(input),
					},
				})
			case "tool_result":
				flushText()
				toolMsg := map[string]interface{}{
					"role":    "tool",
					"content": anthropicContentText(block["content"]),
				}
				if toolCallID := stringValue(block["tool_use_id"]); toolCallID != "" {
					toolMsg["tool_call_id"] = toolCallID
					if name := pendingToolNames[toolCallID]; name != "" {
						toolMsg["name"] = name
					}
				}
				if name := stringValue(block["name"]); name != "" {
					toolMsg["name"] = name
				}
				entries = append(entries, toolMsg)
			default:
				if text := anthropicContentText(block); text != "" {
					textParts = append(textParts, text)
				}
			}
		default:
			if text := anthropicContentText(block); text != "" {
				textParts = append(textParts, text)
			}
		}
	}

	if role == "assistant" {
		if len(textParts) == 0 && len(reasoningParts) == 0 && len(assistantToolCalls) == 0 {
			return entries, nil
		}
		assistant := map[string]interface{}{"role": "assistant"}
		if len(textParts) > 0 {
			assistant["content"] = strings.Join(textParts, "\n")
		} else {
			assistant["content"] = nil
		}
		if len(assistantToolCalls) > 0 {
			assistant["tool_calls"] = assistantToolCalls
		}
		if len(reasoningParts) > 0 {
			assistant["reasoning_content"] = strings.Join(reasoningParts, "\n")
		}
		entries = append(entries, assistant)
		return entries, nil
	}

	flushText()
	return entries, nil
}

func anthropicToolsToOpenAI(tools []api.AnthropicTool) []api.Tool {
	out := make([]api.Tool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	return out
}

func anthropicToolChoiceToOpenAI(choice interface{}) interface{} {
	switch value := choice.(type) {
	case nil:
		return nil
	case string:
		return anthropicToolChoiceTypeToOpenAI(strings.TrimSpace(value), "")
	case map[string]interface{}:
		return anthropicToolChoiceTypeToOpenAI(stringValue(value["type"]), stringValue(value["name"]))
	default:
		return choice
	}
}

func anthropicToolChoiceTypeToOpenAI(kind, name string) interface{} {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "auto":
		return "auto"
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		if name == "" {
			return "required"
		}
		return map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": name,
			},
		}
	default:
		return nil
	}
}

func countAnthropicRequestTokens(req api.AnthropicMessageRequest) int {
	total := estimateAnthropicTokens(anthropicContentText(req.System))
	for _, item := range req.Messages {
		total += estimateAnthropicTokens(anthropicContentText(item.Content))
	}
	if total == 0 {
		return 1
	}
	return total
}

func estimateAnthropicTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	count := utf8.RuneCountInString(text) / 4
	if count < 1 {
		count = 1
	}
	return count
}

func anthropicContentText(content interface{}) string {
	switch value := content.(type) {
	case nil:
		return ""
	case string:
		return value
	case []interface{}:
		var parts []string
		for _, raw := range value {
			if part := anthropicContentText(raw); part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]interface{}:
		kind, _ := value["type"].(string)
		switch kind {
		case "text":
			text, _ := value["text"].(string)
			return text
		case "thinking":
			thinking, _ := value["thinking"].(string)
			return thinking
		case "tool_use":
			name, _ := value["name"].(string)
			input := toolArgumentsJSONString(parseToolArguments(value["input"]))
			if strings.TrimSpace(name) == "" {
				return input
			}
			if strings.TrimSpace(input) == "" || input == "{}" {
				return name
			}
			return name + " " + input
		case "tool_result":
			return anthropicContentText(value["content"])
		default:
			if text, ok := value["text"].(string); ok {
				return text
			}
			return anthropicContentText(value["content"])
		}
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func anthropicContentBlocksFromOpenAIMessage(msg *api.Message) []api.AnthropicContentBlock {
	blocks := make([]api.AnthropicContentBlock, 0, 2+len(msg.ToolCalls))
	if reasoning := strings.TrimSpace(msg.ReasoningContent); reasoning != "" {
		blocks = append(blocks, api.AnthropicContentBlock{
			Type:     "thinking",
			Thinking: reasoning,
		})
	}
	if text := contentAsString(msg.Content); strings.TrimSpace(text) != "" {
		blocks = append(blocks, api.AnthropicContentBlock{
			Type: "text",
			Text: text,
		})
	}
	for i, call := range msg.ToolCalls {
		callID := call.ID
		if strings.TrimSpace(callID) == "" {
			callID = syntheticToolCallID("", i)
		}
		input := parseToolArguments(call.Function.Arguments)
		if input == nil {
			input = map[string]interface{}{}
		}
		blocks = append(blocks, api.AnthropicContentBlock{
			Type:  "tool_use",
			ID:    callID,
			Name:  call.Function.Name,
			Input: input,
		})
	}
	return blocks
}

func anthropicMessageTextAndReasoning(role string, content interface{}) (string, string) {
	if role != "assistant" {
		return anthropicContentText(content), ""
	}
	switch value := content.(type) {
	case nil:
		return "", ""
	case string:
		return value, ""
	case []interface{}:
		textParts := make([]string, 0, len(value))
		reasoningParts := make([]string, 0)
		for _, raw := range value {
			text, reasoning := anthropicContentPartTextAndReasoning(raw)
			if text != "" {
				textParts = append(textParts, text)
			}
			if reasoning != "" {
				reasoningParts = append(reasoningParts, reasoning)
			}
		}
		return strings.Join(textParts, "\n"), strings.Join(reasoningParts, "\n")
	case map[string]interface{}:
		return anthropicContentPartTextAndReasoning(value)
	default:
		return anthropicContentText(value), ""
	}
}

func anthropicContentPartTextAndReasoning(content interface{}) (string, string) {
	switch value := content.(type) {
	case map[string]interface{}:
		if stringValue(value["type"]) == "thinking" {
			thinking, _ := value["thinking"].(string)
			return "", thinking
		}
		return anthropicContentText(value), ""
	default:
		return anthropicContentText(value), ""
	}
}

func anthropicContentBlocksText(blocks []api.AnthropicContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		case "tool_use":
			input := toolArgumentsJSONString(block.Input)
			if strings.TrimSpace(block.Name) != "" {
				parts = append(parts, block.Name)
			}
			if strings.TrimSpace(input) != "" && input != "{}" {
				parts = append(parts, input)
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				parts = append(parts, block.Thinking)
			}
		default:
			data, err := json.Marshal(block)
			if err == nil && len(data) > 0 {
				parts = append(parts, string(data))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func anthropicStopReason(choice api.OpenAIChoice) string {
	finishReason := openAIChoiceFinishReason(choice)
	switch finishReason {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

func anthropicToolUseID(existing string, messageIndex, toolIndex int) string {
	if strings.TrimSpace(existing) != "" {
		return existing
	}
	return fmt.Sprintf("toolu_%d_%d", messageIndex, toolIndex)
}

func writeAnthropicMessageStart(w http.ResponseWriter, id, modelID string, inputTokens int) {
	writeAnthropicSSE(w, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            id,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         modelID,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  inputTokens,
				"output_tokens": 0,
			},
		},
	})
}

func writeAnthropicStreamedMessage(w http.ResponseWriter, resp api.AnthropicMessageResponse) {
	writeAnthropicMessageStart(w, resp.ID, resp.Model, resp.Usage.InputTokens)
	for i, block := range resp.Content {
		writeAnthropicSSE(w, "content_block_start", map[string]interface{}{
			"type":          "content_block_start",
			"index":         i,
			"content_block": anthropicStreamStartBlock(block),
		})
		switch block.Type {
		case "text":
			if block.Text != "" {
				writeAnthropicSSE(w, "content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": i,
					"delta": map[string]interface{}{
						"type": "text_delta",
						"text": block.Text,
					},
				})
			}
		case "tool_use":
			partialJSON := toolArgumentsJSONString(block.Input)
			if strings.TrimSpace(partialJSON) != "" && partialJSON != "{}" {
				writeAnthropicSSE(w, "content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": i,
					"delta": map[string]interface{}{
						"type":         "input_json_delta",
						"partial_json": partialJSON,
					},
				})
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				writeAnthropicSSE(w, "content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": i,
					"delta": map[string]interface{}{
						"type":     "thinking_delta",
						"thinking": block.Thinking,
					},
				})
			}
		}
		writeAnthropicSSE(w, "content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": i,
		})
	}
	writeAnthropicSSE(w, "message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   resp.StopReason,
			"stop_sequence": resp.StopSequence,
		},
		"usage": map[string]interface{}{
			"output_tokens": resp.Usage.OutputTokens,
		},
	})
	writeAnthropicSSE(w, "message_stop", map[string]interface{}{
		"type": "message_stop",
	})
}

func anthropicStreamStartBlock(block api.AnthropicContentBlock) map[string]interface{} {
	switch block.Type {
	case "tool_use":
		return map[string]interface{}{
			"type":  "tool_use",
			"id":    block.ID,
			"name":  block.Name,
			"input": map[string]interface{}{},
		}
	case "thinking":
		return map[string]interface{}{
			"type":      "thinking",
			"thinking":  "",
			"signature": block.Signature,
		}
	default:
		return map[string]interface{}{
			"type": "text",
			"text": "",
		}
	}
}

func writeAnthropicSSE(w http.ResponseWriter, event string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\n", event)
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeAnthropicError(w http.ResponseWriter, status int, msg string) {
	writeAnthropicErrorWithType(w, status, "invalid_request_error", msg)
}

func writeAnthropicInferenceError(w http.ResponseWriter, err error) {
	status := inference.HTTPStatusCode(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	writeAnthropicErrorWithType(w, status, anthropicErrorTypeForStatus(status), inference.HTTPErrorMessage(err))
}

func writeAnthropicErrorWithType(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(anthropicErrorPayloadWithType(errType, msg))
}

func anthropicErrorPayload(msg string) map[string]interface{} {
	return anthropicErrorPayloadWithType("invalid_request_error", msg)
}

func anthropicErrorPayloadFromInferenceError(err error) map[string]interface{} {
	status := inference.HTTPStatusCode(err)
	return anthropicErrorPayloadWithType(anthropicErrorTypeForStatus(status), inference.HTTPErrorMessage(err))
}

func anthropicErrorTypeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

func anthropicErrorPayloadWithType(errType, msg string) map[string]interface{} {
	return map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errType,
			"message": msg,
		},
	}
}
