package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

type openAIResponsesRequest struct {
	Model             string                   `json:"model"`
	Source            string                   `json:"source,omitempty"`
	Input             interface{}              `json:"input"`
	Instructions      string                   `json:"instructions,omitempty"`
	Stream            bool                     `json:"stream,omitempty"`
	MaxOutputTokens   *int                     `json:"max_output_tokens,omitempty"`
	Temperature       *float64                 `json:"temperature,omitempty"`
	TopP              *float64                 `json:"top_p,omitempty"`
	Tools             []map[string]interface{} `json:"tools,omitempty"`
	ToolChoice        interface{}              `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                    `json:"parallel_tool_calls,omitempty"`
}

// POST /v1/responses -- minimal OpenAI Responses API compatibility for Codex/OpenAI SDK clients.
func (s *Server) handleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	var req openAIResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	if req.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	opts := inference.DefaultOptions()
	if req.Temperature != nil {
		opts.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		opts.TopP = *req.TopP
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		opts.MaxTokens = *req.MaxOutputTokens
	}

	eng, err := s.getChatEngine(r.Context(), req.Model, req.Source, 0, 0, -1, "", "", "")
	if err != nil {
		if inference.HTTPStatusCode(err) != 0 {
			writeOpenAIInferenceError(w, err)
			return
		}
		writeOpenAIError(w, http.StatusBadRequest, "model_not_found", err.Error())
		return
	}
	defer s.touchEngine(req.Model)

	if openAIResponsesRequestHasToolFeatures(req) {
		s.handleOpenAIResponsesWithTools(w, r, req, eng, opts)
		return
	}
	if proxy, ok := eng.(inference.ChatCompletionProxier); ok {
		s.handleOpenAIResponsesProxy(w, r, req, proxy, opts)
		return
	}

	messages := responsesRequestMessages(req)
	inputTokens := countResponsesTokens(req)
	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	itemID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	created := time.Now().Unix()

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		writeResponsesSSE(w, "response.created", map[string]interface{}{
			"type":     "response.created",
			"response": buildResponsesResponse(id, itemID, req.Model, "", created, "in_progress", inputTokens),
		})
		writeResponsesSSE(w, "response.output_item.added", map[string]interface{}{
			"type":         "response.output_item.added",
			"output_index": 0,
			"item":         buildResponsesOutputItem(itemID, "", "in_progress"),
		})
		writeResponsesSSE(w, "response.content_part.added", map[string]interface{}{
			"type":          "response.content_part.added",
			"output_index":  0,
			"content_index": 0,
			"item_id":       itemID,
			"part": map[string]interface{}{
				"type":        "output_text",
				"text":        "",
				"annotations": []interface{}{},
			},
		})

		var full strings.Builder
		thinkFilter := newResponsesThinkTagFilter()
		onToken := func(token string) {
			full.WriteString(token)
			visible := thinkFilter.Push(token)
			if visible == "" {
				return
			}
			writeResponsesSSE(w, "response.output_text.delta", map[string]interface{}{
				"type":          "response.output_text.delta",
				"output_index":  0,
				"content_index": 0,
				"item_id":       itemID,
				"delta":         visible,
			})
		}

		if _, err := eng.Chat(r.Context(), messages, opts, onToken); err != nil {
			writeResponsesSSE(w, "error", map[string]interface{}{
				"type":    "error",
				"message": err.Error(),
			})
			fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}

		text := normalizeResponsesVisibleText(full.String())
		if visible := thinkFilter.Flush(); visible != "" {
			writeResponsesSSE(w, "response.output_text.delta", map[string]interface{}{
				"type":          "response.output_text.delta",
				"output_index":  0,
				"content_index": 0,
				"item_id":       itemID,
				"delta":         visible,
			})
		}
		writeResponsesSSE(w, "response.output_text.done", map[string]interface{}{
			"type":          "response.output_text.done",
			"output_index":  0,
			"content_index": 0,
			"item_id":       itemID,
			"text":          text,
		})
		writeResponsesSSE(w, "response.content_part.done", map[string]interface{}{
			"type":          "response.content_part.done",
			"output_index":  0,
			"content_index": 0,
			"item_id":       itemID,
			"part": map[string]interface{}{
				"type":        "output_text",
				"text":        text,
				"annotations": []interface{}{},
			},
		})
		writeResponsesSSE(w, "response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": 0,
			"item":         buildResponsesOutputItem(itemID, text, "completed"),
		})
		writeResponsesSSE(w, "response.completed", map[string]interface{}{
			"type":     "response.completed",
			"response": buildResponsesResponse(id, itemID, req.Model, text, created, "completed", inputTokens),
		})
		fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	text, err := eng.Chat(r.Context(), messages, opts, nil)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	text = normalizeResponsesVisibleText(text)

	writeJSON(w, http.StatusOK, buildResponsesResponse(id, itemID, req.Model, text, created, "completed", inputTokens))
}

func (s *Server) handleOpenAIResponsesProxy(
	w http.ResponseWriter,
	r *http.Request,
	req openAIResponsesRequest,
	proxy inference.ChatCompletionProxier,
	opts inference.Options,
) {
	chatReq, err := responsesRequestToOpenAIChatRequest(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	reqBody, err := openAIChatRequestToProxyBody(chatReq, opts, false)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	resp, err := proxy.ChatCompletion(r.Context(), reqBody)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	defer resp.Body.Close()

	var openAIResp api.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "decoding response: "+err.Error())
		return
	}

	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	created := time.Now().Unix()
	inputTokens := countResponsesTokens(req)
	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		writeResponsesChatStream(w, id, req.Model, created, openAIResp, inputTokens)
		fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	writeJSON(w, http.StatusOK, buildResponsesToolResponse(id, req.Model, created, openAIResp, inputTokens, "completed"))
}

// GET /v1/responses -- return a clear error instead of the web UI index for websocket probes.
func (s *Server) handleOpenAIResponsesUnsupported(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusUpgradeRequired, "invalid_request_error", "websocket transport is not supported on this endpoint")
}

func openAIResponsesRequestHasToolFeatures(req openAIResponsesRequest) bool {
	if len(responsesToolsToOpenAITools(req.Tools)) > 0 {
		return true
	}
	return responsesInputHasToolItems(req.Input)
}

func (s *Server) handleOpenAIResponsesWithTools(
	w http.ResponseWriter,
	r *http.Request,
	req openAIResponsesRequest,
	eng inference.Engine,
	opts inference.Options,
) {
	proxy, ok := eng.(inference.ChatCompletionProxier)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "selected model backend does not support tool calling")
		return
	}

	chatReq, err := responsesRequestToOpenAIChatRequest(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	reqBody, err := openAIChatRequestToProxyBody(chatReq, opts, false)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	resp, err := proxy.ChatCompletion(r.Context(), reqBody)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	defer resp.Body.Close()

	var openAIResp api.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "decoding tool response: "+err.Error())
		return
	}

	openAIResp = normalizeOpenAIToolResponse(openAIResp, chatReq.Tools)
	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	created := time.Now().Unix()
	inputTokens := countResponsesTokens(req)

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		writeResponsesToolStream(w, id, req.Model, created, openAIResp, inputTokens)
		fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	writeJSON(w, http.StatusOK, buildResponsesToolResponse(id, req.Model, created, openAIResp, inputTokens, "completed"))
}

func responsesRequestToOpenAIChatRequest(req openAIResponsesRequest) (api.OpenAIChatRequest, error) {
	messages, err := responsesRequestOpenAIMessages(req)
	if err != nil {
		return api.OpenAIChatRequest{}, err
	}

	chatReq := api.OpenAIChatRequest{
		Model:             req.Model,
		Messages:          messages,
		Tools:             responsesToolsToOpenAITools(req.Tools),
		ParallelToolCalls: req.ParallelToolCalls,
	}
	if toolChoice := responsesToolChoiceToOpenAI(req.ToolChoice); toolChoice != nil {
		chatReq.ToolChoice = toolChoice
	}
	return chatReq, nil
}

func responsesRequestOpenAIMessages(req openAIResponsesRequest) ([]api.Message, error) {
	rawMessages, err := responsesInputToOpenAIMessages(req.Input)
	if err != nil {
		return nil, err
	}

	systemParts := make([]string, 0, 2)
	messages := make([]api.Message, 0, len(rawMessages)+1)
	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		systemParts = append(systemParts, instructions)
	}

	for _, message := range rawMessages {
		switch message.Role {
		case "system", "developer":
			if text := strings.TrimSpace(contentAsString(message.Content)); text != "" {
				systemParts = append(systemParts, text)
			}
		default:
			if message.Role == "" {
				message.Role = "user"
			}
			messages = append(messages, message)
		}
	}

	if len(systemParts) > 0 {
		messages = append([]api.Message{{
			Role:    "system",
			Content: strings.Join(systemParts, "\n\n"),
		}}, messages...)
	}
	if len(messages) == 0 {
		messages = append(messages, api.Message{Role: "user", Content: ""})
	}
	return messages, nil
}

func responsesInputToOpenAIMessages(input interface{}) ([]api.Message, error) {
	switch value := input.(type) {
	case nil:
		return nil, nil
	case string:
		return []api.Message{{Role: "user", Content: value}}, nil
	case []interface{}:
		messages := make([]api.Message, 0, len(value))
		pendingToolCalls := make([]api.ToolCall, 0)
		pendingReasoning := ""
		flushPending := func() {
			if len(pendingToolCalls) == 0 {
				return
			}
			assistant := api.Message{
				Role:      "assistant",
				Content:   nil,
				ToolCalls: append([]api.ToolCall{}, pendingToolCalls...),
			}
			// Thinking models (e.g. DeepSeek) require reasoning_content on the same
			// assistant turn as tool_calls when Codex replays a preceding reasoning item.
			if strings.TrimSpace(pendingReasoning) != "" {
				assistant.ReasoningContent = pendingReasoning
				pendingReasoning = ""
			}
			messages = append(messages, assistant)
			pendingToolCalls = pendingToolCalls[:0]
		}
		for idx, item := range value {
			msg, toolCall, kind, err := responsesInputItemToOpenAIMessage(item, idx)
			if err != nil {
				return nil, err
			}
			if kind == "function_call" {
				pendingToolCalls = append(pendingToolCalls, toolCall)
				continue
			}
			if kind == "reasoning" {
				if msg != nil && strings.TrimSpace(msg.ReasoningContent) != "" {
					pendingReasoning = joinNonEmpty(pendingReasoning, msg.ReasoningContent, "\n")
				}
				continue
			}
			flushPending()
			if msg != nil {
				if pendingReasoning != "" && msg.Role == "assistant" && msg.ReasoningContent == "" {
					msg.ReasoningContent = pendingReasoning
					pendingReasoning = ""
				}
				messages = append(messages, *msg)
			}
		}
		flushPending()
		if pendingReasoning != "" {
			messages = append(messages, api.Message{Role: "assistant", Content: "", ReasoningContent: pendingReasoning})
		}
		return messages, nil
	case map[string]interface{}:
		msg, toolCall, kind, err := responsesInputItemToOpenAIMessage(value, 0)
		if err != nil {
			return nil, err
		}
		if kind == "function_call" {
			return []api.Message{{
				Role:      "assistant",
				Content:   nil,
				ToolCalls: []api.ToolCall{toolCall},
			}}, nil
		}
		if msg == nil {
			return nil, nil
		}
		return []api.Message{*msg}, nil
	default:
		return []api.Message{{Role: "user", Content: responsesContentText(value)}}, nil
	}
}

func responsesInputItemToOpenAIMessage(item interface{}, index int) (*api.Message, api.ToolCall, string, error) {
	switch value := item.(type) {
	case nil:
		return nil, api.ToolCall{}, "", nil
	case string:
		return &api.Message{Role: "user", Content: value}, api.ToolCall{}, "message", nil
	case map[string]interface{}:
		role := stringValue(value["role"])
		kind := stringValue(value["type"])
		switch kind {
		case "function_call":
			call := responsesFunctionCallToOpenAIToolCall(value, index)
			return nil, call, kind, nil
		case "function_call_output":
			return &api.Message{
				Role:       "tool",
				Content:    responsesContentText(value["output"]),
				ToolCallID: stringValue(value["call_id"]),
				ToolName:   stringValue(value["name"]),
			}, api.ToolCall{}, kind, nil
		case "message":
			if role == "" {
				role = "user"
			}
			return &api.Message{
				Role:    role,
				Content: responsesContentText(value["content"]),
			}, api.ToolCall{}, kind, nil
		case "reasoning":
			return &api.Message{
				Role:             "assistant",
				Content:          "",
				ReasoningContent: responsesReasoningItemText(value),
			}, api.ToolCall{}, kind, nil
		case "input_text", "output_text", "text":
			return &api.Message{
				Role:    "user",
				Content: responsesContentText(value),
			}, api.ToolCall{}, kind, nil
		}
		if role != "" {
			return &api.Message{
				Role:    role,
				Content: responsesContentText(value["content"]),
			}, api.ToolCall{}, "message", nil
		}
		text := responsesContentText(value)
		if text == "" {
			return nil, api.ToolCall{}, "", nil
		}
		return &api.Message{Role: "user", Content: text}, api.ToolCall{}, "message", nil
	default:
		return &api.Message{Role: "user", Content: responsesContentText(value)}, api.ToolCall{}, "message", nil
	}
}

func responsesFunctionCallToOpenAIToolCall(value map[string]interface{}, index int) api.ToolCall {
	callID := stringValue(value["call_id"])
	if callID == "" {
		callID = stringValue(value["id"])
	}
	return api.ToolCall{
		ID:   syntheticToolCallID(callID, index),
		Type: "function",
		Function: api.ToolFunction{
			Name:      stringValue(value["name"]),
			Arguments: toolArgumentsJSONString(value["arguments"]),
		},
	}
}

func joinNonEmpty(a, b, sep string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + sep + b
}

func responsesToolsToOpenAITools(rawTools []map[string]interface{}) []api.Tool {
	tools := make([]api.Tool, 0, len(rawTools))
	for _, raw := range rawTools {
		toolType := stringValue(raw["type"])
		if toolType != "function" {
			continue
		}

		name := stringValue(raw["name"])
		description := stringValue(raw["description"])
		parameters := raw["parameters"]

		if nested, ok := raw["function"].(map[string]interface{}); ok {
			if nestedName := stringValue(nested["name"]); nestedName != "" {
				name = nestedName
			}
			if nestedDescription := stringValue(nested["description"]); nestedDescription != "" {
				description = nestedDescription
			}
			if nestedParameters, ok := nested["parameters"]; ok {
				parameters = nestedParameters
			}
		}
		if name == "" {
			continue
		}

		tools = append(tools, api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        name,
				Description: description,
				Parameters:  parameters,
			},
		})
	}
	return tools
}

func responsesToolChoiceToOpenAI(choice interface{}) interface{} {
	switch value := choice.(type) {
	case nil:
		return nil
	case string:
		return strings.TrimSpace(value)
	case map[string]interface{}:
		if toolType := stringValue(value["type"]); toolType == "function" {
			name := stringValue(value["name"])
			if name == "" {
				if fn, ok := value["function"].(map[string]interface{}); ok {
					name = stringValue(fn["name"])
				}
			}
			if name != "" {
				return map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name": name,
					},
				}
			}
		}
		return value
	default:
		return choice
	}
}

func responsesInputHasToolItems(input interface{}) bool {
	switch value := input.(type) {
	case []interface{}:
		for _, item := range value {
			if responsesInputHasToolItems(item) {
				return true
			}
		}
	case map[string]interface{}:
		switch stringValue(value["type"]) {
		case "function_call", "function_call_output":
			return true
		}
		if nested, ok := value["content"]; ok {
			return responsesInputHasToolItems(nested)
		}
	}
	return false
}

func responsesRequestMessages(req openAIResponsesRequest) []inference.Message {
	rawMessages := responsesInputToMessages(req.Input)
	systemParts := make([]string, 0, 2)
	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		systemParts = append(systemParts, instructions)
	}

	messages := make([]inference.Message, 0, len(rawMessages)+1)
	for _, message := range rawMessages {
		text := strings.TrimSpace(fmt.Sprint(message.Content))
		switch message.Role {
		case "system", "developer":
			if text != "" {
				systemParts = append(systemParts, text)
			}
		default:
			if message.Role == "" {
				message.Role = "user"
			}
			messages = append(messages, message)
		}
	}

	if len(systemParts) > 0 {
		messages = append([]inference.Message{{
			Role:    "system",
			Content: strings.Join(systemParts, "\n\n"),
		}}, messages...)
	}
	if len(messages) == 0 {
		messages = append(messages, inference.Message{Role: "user", Content: ""})
	}
	return messages
}

func responsesInputToMessages(input interface{}) []inference.Message {
	switch value := input.(type) {
	case nil:
		return nil
	case string:
		return []inference.Message{{Role: "user", Content: value}}
	case []interface{}:
		var messages []inference.Message
		pendingReasoning := ""
		for _, item := range value {
			next := responsesInputToMessages(item)
			for _, msg := range next {
				if msg.Role == "assistant" && msg.Content == "" && msg.ReasoningContent != "" {
					pendingReasoning = joinNonEmpty(pendingReasoning, msg.ReasoningContent, "\n")
					continue
				}
				if pendingReasoning != "" && msg.Role == "assistant" && msg.ReasoningContent == "" {
					msg.ReasoningContent = pendingReasoning
					pendingReasoning = ""
				}
				messages = append(messages, msg)
			}
		}
		if pendingReasoning != "" {
			messages = append(messages, inference.Message{Role: "assistant", Content: "", ReasoningContent: pendingReasoning})
		}
		return messages
	case map[string]interface{}:
		role, _ := value["role"].(string)
		kind, _ := value["type"].(string)
		switch {
		case role != "":
			return []inference.Message{{
				Role:    role,
				Content: responsesContentText(value["content"]),
			}}
		case kind == "message":
			return []inference.Message{{
				Role:    "user",
				Content: responsesContentText(value["content"]),
			}}
		case kind == "reasoning":
			return []inference.Message{{
				Role:             "assistant",
				Content:          "",
				ReasoningContent: responsesReasoningItemText(value),
			}}
		case kind == "input_text" || kind == "output_text" || kind == "text":
			return []inference.Message{{
				Role:    "user",
				Content: responsesContentText(value),
			}}
		default:
			text := responsesContentText(value)
			if text == "" {
				return nil
			}
			return []inference.Message{{Role: "user", Content: text}}
		}
	default:
		return []inference.Message{{Role: "user", Content: responsesContentText(value)}}
	}
}

func responsesContentText(content interface{}) string {
	switch value := content.(type) {
	case nil:
		return ""
	case string:
		return value
	case []interface{}:
		var parts []string
		for _, item := range value {
			if text := responsesContentText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case []map[string]interface{}:
		var parts []string
		for _, item := range value {
			if text := responsesContentText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]interface{}:
		if text, ok := value["text"].(string); ok {
			return text
		}
		if inputText, ok := value["input_text"].(string); ok {
			return inputText
		}
		return responsesContentText(value["content"])
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func buildResponsesToolResponse(id, modelID string, created int64, resp api.OpenAIChatResponse, inputTokens int, status string) map[string]interface{} {
	output, outputText := responsesOutputFromOpenAIChatResponse(resp)
	usage := responsesUsageFromOpenAI(resp, inputTokens, outputText)
	return map[string]interface{}{
		"id":                  id,
		"object":              "response",
		"created_at":          created,
		"status":              status,
		"model":               modelID,
		"output":              output,
		"output_text":         outputText,
		"parallel_tool_calls": responsesParallelToolCalls(output),
		"usage":               usage,
	}
}

func responsesOutputFromOpenAIChatResponse(resp api.OpenAIChatResponse) ([]interface{}, string) {
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return []interface{}{buildResponsesOutputItem(fmt.Sprintf("msg_%d", time.Now().UnixNano()), "", "completed")}, ""
	}

	msg := resp.Choices[0].Message
	output := make([]interface{}, 0, 2+len(msg.ToolCalls))
	outputText := strings.TrimSpace(normalizeResponsesVisibleText(contentAsString(msg.Content)))
	if reasoning := strings.TrimSpace(msg.ReasoningContent); reasoning != "" {
		output = append(output, buildResponsesReasoningItem(fmt.Sprintf("rs_%d", time.Now().UnixNano()), reasoning, "completed"))
	}
	if outputText != "" || len(msg.ToolCalls) == 0 {
		output = append(output, buildResponsesOutputItem(fmt.Sprintf("msg_%d", time.Now().UnixNano()), outputText, "completed"))
	}
	for i, call := range msg.ToolCalls {
		output = append(output, buildResponsesFunctionCallItem(call, i, "completed"))
	}
	return output, outputText
}

func responsesReasoningItemText(item map[string]interface{}) string {
	if text := responsesContentText(item["content"]); text != "" {
		return text
	}
	if text := responsesContentText(item["summary"]); text != "" {
		return text
	}
	return stringValue(item["encrypted_content"])
}

func buildResponsesFunctionCallItem(call api.ToolCall, index int, status string) map[string]interface{} {
	callID := syntheticToolCallID(call.ID, index)
	return map[string]interface{}{
		"id":        callID,
		"type":      "function_call",
		"call_id":   callID,
		"name":      call.Function.Name,
		"arguments": toolArgumentsJSONString(call.Function.Arguments),
		"status":    status,
	}
}

func responsesUsageFromOpenAI(resp api.OpenAIChatResponse, inputTokens int, outputText string) map[string]interface{} {
	if resp.Usage.TotalTokens > 0 {
		return map[string]interface{}{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		}
	}

	outputTokens := estimateAnthropicTokens(outputText)
	return map[string]interface{}{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  inputTokens + outputTokens,
	}
}

func responsesParallelToolCalls(output []interface{}) bool {
	count := 0
	for _, item := range output {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if stringValue(itemMap["type"]) == "function_call" {
			count++
		}
	}
	return count > 1
}

func writeResponsesToolStream(w http.ResponseWriter, id, modelID string, created int64, resp api.OpenAIChatResponse, inputTokens int) {
	output, outputText := responsesOutputFromOpenAIChatResponse(resp)
	usage := responsesUsageFromOpenAI(resp, inputTokens, outputText)
	sequence := 0
	nextSequence := func() int {
		sequence++
		return sequence
	}

	writeResponsesSSE(w, "response.created", map[string]interface{}{
		"type":            "response.created",
		"sequence_number": nextSequence(),
		"response": map[string]interface{}{
			"id":                  id,
			"object":              "response",
			"created_at":          created,
			"status":              "in_progress",
			"model":               modelID,
			"output":              []interface{}{},
			"output_text":         "",
			"parallel_tool_calls": responsesParallelToolCalls(output),
			"usage":               usage,
		},
	})

	for index, raw := range output {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch stringValue(item["type"]) {
		case "reasoning":
			itemID := stringValue(item["id"])
			text := responsesReasoningItemText(item)
			writeResponsesSSE(w, "response.output_item.added", map[string]interface{}{
				"type":            "response.output_item.added",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"item":            buildResponsesReasoningItem(itemID, "", "in_progress"),
			})
			if text != "" {
				writeResponsesSSE(w, "response.reasoning_text.delta", map[string]interface{}{
					"type":            "response.reasoning_text.delta",
					"sequence_number": nextSequence(),
					"item_id":         itemID,
					"output_index":    index,
					"content_index":   0,
					"delta":           text,
				})
			}
			writeResponsesSSE(w, "response.reasoning_text.done", map[string]interface{}{
				"type":            "response.reasoning_text.done",
				"sequence_number": nextSequence(),
				"item_id":         itemID,
				"output_index":    index,
				"content_index":   0,
				"text":            text,
			})
			writeResponsesSSE(w, "response.output_item.done", map[string]interface{}{
				"type":            "response.output_item.done",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"item":            item,
			})
		case "function_call":
			itemID := stringValue(item["id"])
			name := stringValue(item["name"])
			args := stringValue(item["arguments"])
			callID := stringValue(item["call_id"])
			writeResponsesSSE(w, "response.output_item.added", map[string]interface{}{
				"type":            "response.output_item.added",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"item": map[string]interface{}{
					"id":        itemID,
					"type":      "function_call",
					"call_id":   callID,
					"name":      name,
					"arguments": "",
					"status":    "in_progress",
				},
			})
			if args != "" {
				writeResponsesSSE(w, "response.function_call_arguments.delta", map[string]interface{}{
					"type":            "response.function_call_arguments.delta",
					"sequence_number": nextSequence(),
					"item_id":         itemID,
					"output_index":    index,
					"delta":           args,
				})
			}
			writeResponsesSSE(w, "response.function_call_arguments.done", map[string]interface{}{
				"type":            "response.function_call_arguments.done",
				"sequence_number": nextSequence(),
				"item_id":         itemID,
				"output_index":    index,
				"name":            name,
				"arguments":       args,
			})
			writeResponsesSSE(w, "response.output_item.done", map[string]interface{}{
				"type":            "response.output_item.done",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"item":            item,
			})
		case "message":
			itemID := stringValue(item["id"])
			text := ""
			if content, ok := item["content"].([]map[string]interface{}); ok && len(content) > 0 {
				text = stringValue(content[0]["text"])
			} else if content, ok := item["content"].([]interface{}); ok && len(content) > 0 {
				if first, ok := content[0].(map[string]interface{}); ok {
					text = stringValue(first["text"])
				}
			}
			writeResponsesSSE(w, "response.output_item.added", map[string]interface{}{
				"type":            "response.output_item.added",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"item":            buildResponsesOutputItem(itemID, "", "in_progress"),
			})
			writeResponsesSSE(w, "response.content_part.added", map[string]interface{}{
				"type":            "response.content_part.added",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"content_index":   0,
				"item_id":         itemID,
				"part": map[string]interface{}{
					"type":        "output_text",
					"text":        "",
					"annotations": []interface{}{},
				},
			})
			if text != "" {
				writeResponsesSSE(w, "response.output_text.delta", map[string]interface{}{
					"type":            "response.output_text.delta",
					"sequence_number": nextSequence(),
					"output_index":    index,
					"content_index":   0,
					"item_id":         itemID,
					"delta":           text,
				})
			}
			writeResponsesSSE(w, "response.output_text.done", map[string]interface{}{
				"type":            "response.output_text.done",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"content_index":   0,
				"item_id":         itemID,
				"text":            text,
			})
			writeResponsesSSE(w, "response.content_part.done", map[string]interface{}{
				"type":            "response.content_part.done",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"content_index":   0,
				"item_id":         itemID,
				"part": map[string]interface{}{
					"type":        "output_text",
					"text":        text,
					"annotations": []interface{}{},
				},
			})
			writeResponsesSSE(w, "response.output_item.done", map[string]interface{}{
				"type":            "response.output_item.done",
				"sequence_number": nextSequence(),
				"output_index":    index,
				"item":            buildResponsesOutputItem(itemID, text, "completed"),
			})
		}
	}

	writeResponsesSSE(w, "response.completed", map[string]interface{}{
		"type":            "response.completed",
		"sequence_number": nextSequence(),
		"response": map[string]interface{}{
			"id":                  id,
			"object":              "response",
			"created_at":          created,
			"status":              "completed",
			"model":               modelID,
			"output":              output,
			"output_text":         outputText,
			"parallel_tool_calls": responsesParallelToolCalls(output),
			"usage":               usage,
		},
	})
}

func writeResponsesChatStream(w http.ResponseWriter, id, modelID string, created int64, resp api.OpenAIChatResponse, inputTokens int) {
	writeResponsesToolStream(w, id, modelID, created, resp, inputTokens)
}

const (
	responsesThinkOpenTag  = "<think>"
	responsesThinkCloseTag = "</think>"
)

type responsesThinkTagFilter struct {
	buffer  string
	inThink bool
	emitted bool
}

func newResponsesThinkTagFilter() *responsesThinkTagFilter {
	return &responsesThinkTagFilter{}
}

func normalizeResponsesVisibleText(text string) string {
	filter := newResponsesThinkTagFilter()
	return filter.Push(text) + filter.Flush()
}

func (f *responsesThinkTagFilter) Push(chunk string) string {
	if chunk == "" {
		return ""
	}

	f.buffer += chunk
	var out strings.Builder
	for {
		if f.inThink {
			idx := indexFold(f.buffer, responsesThinkCloseTag)
			if idx < 0 {
				f.buffer = suffixMatchingTagPrefixFold(f.buffer, responsesThinkCloseTag)
				return f.trimLeadingBlankLines(out.String())
			}
			f.buffer = f.buffer[idx+len(responsesThinkCloseTag):]
			f.inThink = false
			continue
		}

		idx := indexFold(f.buffer, responsesThinkOpenTag)
		if idx >= 0 {
			out.WriteString(f.buffer[:idx])
			f.buffer = f.buffer[idx+len(responsesThinkOpenTag):]
			f.inThink = true
			continue
		}

		suffix := suffixMatchingTagPrefixFold(f.buffer, responsesThinkOpenTag)
		emitLen := len(f.buffer) - len(suffix)
		if emitLen > 0 {
			out.WriteString(f.buffer[:emitLen])
		}
		f.buffer = suffix
		return f.trimLeadingBlankLines(out.String())
	}
}

func (f *responsesThinkTagFilter) Flush() string {
	if f.inThink {
		f.buffer = ""
		f.inThink = false
		return ""
	}
	out := f.trimLeadingBlankLines(f.buffer)
	f.buffer = ""
	return out
}

func (f *responsesThinkTagFilter) trimLeadingBlankLines(text string) string {
	if text == "" {
		return ""
	}
	if !f.emitted {
		text = strings.TrimLeft(text, "\r\n")
	}
	if text != "" {
		f.emitted = true
	}
	return text
}

func indexFold(s, substr string) int {
	if substr == "" {
		return 0
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(substr)], substr) {
			return i
		}
	}
	return -1
}

func suffixMatchingTagPrefixFold(s, tag string) string {
	max := len(tag) - 1
	if max > len(s) {
		max = len(s)
	}
	for n := max; n > 0; n-- {
		if strings.EqualFold(s[len(s)-n:], tag[:n]) {
			return s[len(s)-n:]
		}
	}
	return ""
}

func countResponsesTokens(req openAIResponsesRequest) int {
	total := estimateAnthropicTokens(req.Instructions)
	total += estimateAnthropicTokens(responsesContentText(req.Input))
	if total == 0 {
		return 1
	}
	return total
}

func buildResponsesOutputItem(itemID, text, status string) map[string]interface{} {
	return map[string]interface{}{
		"id":     itemID,
		"type":   "message",
		"status": status,
		"role":   "assistant",
		"content": []map[string]interface{}{{
			"type":        "output_text",
			"text":        text,
			"annotations": []interface{}{},
		}},
	}
}

func buildResponsesReasoningItem(itemID, text, status string) map[string]interface{} {
	return map[string]interface{}{
		"id":      itemID,
		"type":    "reasoning",
		"status":  status,
		"summary": []interface{}{},
		"content": []map[string]interface{}{{
			"type": "reasoning_text",
			"text": text,
		}},
	}
}

func buildResponsesResponse(id, itemID, modelID, text string, created int64, status string, inputTokens int) map[string]interface{} {
	outputTokens := estimateAnthropicTokens(text)
	return map[string]interface{}{
		"id":                  id,
		"object":              "response",
		"created_at":          created,
		"status":              status,
		"model":               modelID,
		"output":              []interface{}{buildResponsesOutputItem(itemID, text, status)},
		"output_text":         text,
		"parallel_tool_calls": false,
		"usage": map[string]interface{}{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"total_tokens":  inputTokens + outputTokens,
		},
	}
}

func writeResponsesSSE(w http.ResponseWriter, event string, payload interface{}) {
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
