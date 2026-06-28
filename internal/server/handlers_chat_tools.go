package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

func hasToolChatFeatures(req api.ChatRequest) bool {
	return hasChatToolFeatures(req.Messages, req.Tools)
}

func hasChatToolFeatures(messages []api.Message, tools []api.Tool) bool {
	if len(tools) > 0 {
		return true
	}
	for _, msg := range messages {
		if len(msg.ToolCalls) > 0 || msg.Role == "tool" || msg.ToolName != "" || msg.ToolCallID != "" {
			return true
		}
	}
	return false
}

func (s *Server) handleChatWithTools(w http.ResponseWriter, r *http.Request, req api.ChatRequest, eng inference.Engine, opts inference.Options, stream bool) {
	proxy, ok := eng.(inference.ChatCompletionProxier)
	if !ok {
		writeError(w, http.StatusBadRequest, "selected model backend does not support tool calling")
		return
	}

	openAIReq, err := ollamaChatRequestToOpenAI(req, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := proxy.ChatCompletion(r.Context(), openAIReq)
	if err != nil {
		s.writeToolChatError(w, req.Model, stream, requestWantsSSE(r), err)
		return
	}
	defer resp.Body.Close()

	var openAIResp api.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		s.writeToolChatError(w, req.Model, stream, requestWantsSSE(r), fmt.Errorf("decoding tool response: %w", err))
		return
	}
	openAIResp = normalizeOpenAIToolResponse(openAIResp, req.Tools)
	inputTokens, outputTokens := openAIUsageTokens(openAIResp)
	if inputTokens == 0 {
		inputTokens = countMessageTokens(req.Messages)
	}
	s.recordAPIUsage(r, req.Model, req.Source, inputTokens, outputTokens)

	ollamaResp, err := openAIChatResponseToOllama(req.Model, openAIResp)
	if err != nil {
		s.writeToolChatError(w, req.Model, stream, requestWantsSSE(r), err)
		return
	}

	if !stream {
		ollamaResp.Done = true
		writeJSON(w, http.StatusOK, ollamaResp)
		return
	}

	sse := requestWantsSSE(r)
	if sse {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		if shouldEmitToolChunk(ollamaResp.Message) {
			writeSSE(w, ollamaResp)
		}
		writeSSE(w, api.ChatResponse{
			Model:     req.Model,
			Done:      true,
			CreatedAt: time.Now(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if shouldEmitToolChunk(ollamaResp.Message) {
		writeNDJSON(w, ollamaResp)
	}
	writeNDJSON(w, api.ChatResponse{
		Model: req.Model,
		Message: &api.Message{
			Role:    "assistant",
			Content: "",
		},
		Done:      true,
		CreatedAt: time.Now(),
	})
}

func (s *Server) writeToolChatError(w http.ResponseWriter, model string, stream, sse bool, err error) {
	status := inference.HTTPStatusCode(err)
	message := inference.HTTPErrorMessage(err)
	if !stream {
		if status == 0 {
			status = http.StatusInternalServerError
		}
		writeError(w, status, message)
		return
	}
	if status != 0 {
		writeError(w, status, message)
		return
	}
	resp := api.ChatResponse{
		Model: model,
		Message: &api.Message{
			Role:    "assistant",
			Content: "Error: " + message,
		},
		Done:      true,
		CreatedAt: time.Now(),
	}
	if sse {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		writeSSE(w, resp)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	writeNDJSON(w, resp)
}

func shouldEmitToolChunk(msg *api.Message) bool {
	if msg == nil {
		return false
	}
	if len(msg.ToolCalls) > 0 {
		return true
	}
	if msg.Thinking != "" {
		return true
	}
	if s, ok := msg.Content.(string); ok {
		return s != ""
	}
	return msg.Content != nil
}

func ollamaChatRequestToOpenAI(req api.ChatRequest, opts inference.Options) (map[string]interface{}, error) {
	messages, err := ollamaMessagesToOpenAI(req.Messages, req.Tools)
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"model":       req.Model,
		"messages":    messages,
		"temperature": opts.Temperature,
		"top_p":       opts.TopP,
		"stream":      false,
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
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	return body, nil
}

type pendingToolCall struct {
	ID   string
	Name string
}

func ollamaMessagesToOpenAI(messages []api.Message, tools []api.Tool) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(messages))
	pending := make([]pendingToolCall, 0)
	assistantCount := 0

	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			m := map[string]interface{}{"role": "assistant"}
			if len(msg.ToolCalls) > 0 {
				openAIToolCalls := make([]map[string]interface{}, 0, len(msg.ToolCalls))
				for idx, call := range msg.ToolCalls {
					callID := call.ID
					if callID == "" {
						callID = fmt.Sprintf("call_%d_%d", assistantCount, idx)
					}
					pending = append(pending, pendingToolCall{ID: callID, Name: call.Function.Name})
					openAIToolCalls = append(openAIToolCalls, map[string]interface{}{
						"id":   callID,
						"type": defaultToolType(call.Type),
						"function": map[string]interface{}{
							"name":      call.Function.Name,
							"arguments": toolArgumentsJSONStringForTool(call.Function.Arguments, toolForName(tools, call.Function.Name)),
						},
					})
				}
				m["tool_calls"] = openAIToolCalls
				m["content"] = nil
				assistantCount++
			} else {
				m["content"] = msg.Content
			}
			// Preserve reasoning_content for thinking models (e.g., deepseek-v4-pro)
			if msg.ReasoningContent != "" {
				m["reasoning_content"] = msg.ReasoningContent
			}
			out = append(out, m)
		case "tool":
			toolCallID, nextPending := matchPendingToolCall(pending, msg.ToolName)
			pending = nextPending
			m := map[string]interface{}{
				"role":    "tool",
				"content": contentAsString(msg.Content),
			}
			if toolCallID != "" {
				m["tool_call_id"] = toolCallID
			}
			if msg.ToolCallID != "" {
				m["tool_call_id"] = msg.ToolCallID
			}
			if msg.ToolName != "" {
				m["name"] = msg.ToolName
			}
			out = append(out, m)
		default:
			out = append(out, map[string]interface{}{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}

	return out, nil
}

func matchPendingToolCall(pending []pendingToolCall, toolName string) (string, []pendingToolCall) {
	if len(pending) == 0 {
		return "", pending
	}
	matchIdx := 0
	if toolName != "" {
		matchIdx = -1
		for i, call := range pending {
			if call.Name == toolName {
				matchIdx = i
				break
			}
		}
		if matchIdx == -1 {
			matchIdx = 0
		}
	}
	callID := pending[matchIdx].ID
	return callID, append(pending[:matchIdx], pending[matchIdx+1:]...)
}

func toolArgumentsJSONString(args interface{}) string {
	return toolArgumentsJSONStringForTool(args, api.Tool{})
}

func toolArgumentsJSONStringForTool(args interface{}, tool api.Tool) string {
	if args == nil {
		return "{}"
	}
	if s, ok := args.(string); ok {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return "{}"
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			if wrapped, ok := toolArgumentObjectFromScalar(parsed, tool); ok {
				return marshalToolArguments(wrapped)
			}
			return trimmed
		}
		if wrapped, ok := toolArgumentObjectFromScalar(trimmed, tool); ok {
			return marshalToolArguments(wrapped)
		}
		return marshalToolArguments(s)
	}
	if wrapped, ok := toolArgumentObjectFromScalar(args, tool); ok {
		return marshalToolArguments(wrapped)
	}
	return marshalToolArguments(args)
}

func marshalToolArguments(args interface{}) string {
	buf, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(buf)
}

func toolArgumentObjectFromScalar(value interface{}, tool api.Tool) (map[string]interface{}, bool) {
	switch value.(type) {
	case map[string]interface{}, []interface{}:
		return nil, false
	}
	name, ok := singleToolArgumentName(tool)
	if !ok {
		return nil, false
	}
	return map[string]interface{}{name: value}, true
}

func singleToolArgumentName(tool api.Tool) (string, bool) {
	params, ok := tool.Function.Parameters.(map[string]interface{})
	if !ok || params == nil {
		return "", false
	}
	if name, ok := singleRequiredArgumentName(params["required"]); ok {
		return name, true
	}
	properties, ok := params["properties"].(map[string]interface{})
	if !ok || len(properties) != 1 {
		return "", false
	}
	for name := range properties {
		if strings.TrimSpace(name) != "" {
			return name, true
		}
	}
	return "", false
}

func singleRequiredArgumentName(required interface{}) (string, bool) {
	switch v := required.(type) {
	case []interface{}:
		if len(v) != 1 {
			return "", false
		}
		return stringValue(v[0]), stringValue(v[0]) != ""
	case []string:
		if len(v) != 1 || strings.TrimSpace(v[0]) == "" {
			return "", false
		}
		return strings.TrimSpace(v[0]), true
	default:
		return "", false
	}
}

func contentAsString(content interface{}) string {
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

func openAIChatResponseToOllama(model string, resp api.OpenAIChatResponse) (api.ChatResponse, error) {
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return api.ChatResponse{}, fmt.Errorf("no choices in tool response")
	}
	msg := resp.Choices[0].Message
	out := &api.Message{
		Role:    "assistant",
		Content: contentOrEmpty(msg.Content),
	}
	if msg.Thinking != "" {
		out.Thinking = msg.Thinking
	}
	if len(msg.ToolCalls) > 0 {
		out.ToolCalls = toOllamaToolCalls(msg.ToolCalls)
	}
	return api.ChatResponse{
		Model:     model,
		Message:   out,
		Done:      false,
		CreatedAt: time.Now(),
	}, nil
}

func contentOrEmpty(content interface{}) interface{} {
	if content == nil {
		return ""
	}
	return content
}

func toOllamaToolCalls(toolCalls []api.ToolCall) []api.ToolCall {
	out := make([]api.ToolCall, 0, len(toolCalls))
	for i, call := range toolCalls {
		index := i
		out = append(out, api.ToolCall{
			ID:   call.ID,
			Type: defaultToolType(call.Type),
			Function: api.ToolFunction{
				Index:       &index,
				Name:        call.Function.Name,
				Description: call.Function.Description,
				Parameters:  call.Function.Parameters,
				Arguments:   parseToolArguments(call.Function.Arguments),
			},
		})
	}
	return out
}

func parseToolArguments(args interface{}) interface{} {
	s, ok := args.(string)
	if !ok {
		return args
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return map[string]interface{}{}
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parsed
	}
	return s
}

func defaultToolType(toolType string) string {
	if toolType == "" {
		return "function"
	}
	return toolType
}

func normalizeOpenAIToolResponse(resp api.OpenAIChatResponse, tools []api.Tool) api.OpenAIChatResponse {
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return resp
	}

	choice := &resp.Choices[0]
	msg := choice.Message
	if len(msg.ToolCalls) == 0 {
		if calls, ok := synthesizeToolCallsFromContent(msg.Content, tools); ok {
			msg.Content = nil
			msg.ToolCalls = calls
		}
	}

	if len(msg.ToolCalls) > 0 {
		finishReason := "tool_calls"
		choice.FinishReason = &finishReason
	}
	return resp
}

func synthesizeToolCallsFromContent(content interface{}, tools []api.Tool) ([]api.ToolCall, bool) {
	if len(tools) == 0 {
		return nil, false
	}

	text := strings.TrimSpace(contentAsString(content))
	if text == "" {
		return nil, false
	}

	if calls, ok := toolCallsFromJSONText(text, tools); ok {
		return calls, true
	}
	if call, ok := toolCallFromBareName(text, tools, 0); ok {
		return []api.ToolCall{call}, true
	}
	return nil, false
}

func toolCallsFromJSONText(text string, tools []api.Tool) ([]api.ToolCall, bool) {
	candidates := []string{strings.TrimSpace(text)}
	if fenced := stripMarkdownCodeFence(text); fenced != strings.TrimSpace(text) {
		candidates = append(candidates, fenced)
	}

	for _, candidate := range candidates {
		var value interface{}
		if err := json.Unmarshal([]byte(candidate), &value); err != nil {
			continue
		}
		if calls, ok := toolCallsFromStructuredValue(value, tools); ok {
			return calls, true
		}
	}
	return nil, false
}

func toolCallsFromStructuredValue(value interface{}, tools []api.Tool) ([]api.ToolCall, bool) {
	switch v := value.(type) {
	case string:
		if call, ok := toolCallFromBareName(v, tools, 0); ok {
			return []api.ToolCall{call}, true
		}
	case map[string]interface{}:
		if nested, ok := v["tool_calls"]; ok {
			return toolCallsFromStructuredValue(nested, tools)
		}
		if call, ok := structuredValueToToolCall(v, tools, 0); ok {
			return []api.ToolCall{call}, true
		}
	case []interface{}:
		calls := make([]api.ToolCall, 0, len(v))
		for i, item := range v {
			valueMap, ok := item.(map[string]interface{})
			if !ok {
				return nil, false
			}
			call, ok := structuredValueToToolCall(valueMap, tools, i)
			if !ok {
				return nil, false
			}
			calls = append(calls, call)
		}
		if len(calls) > 0 {
			return calls, true
		}
	}
	return nil, false
}

func structuredValueToToolCall(value map[string]interface{}, tools []api.Tool, index int) (api.ToolCall, bool) {
	var (
		callID   = stringValue(value["id"])
		toolType = stringValue(value["type"])
		name     string
		args     interface{}
	)

	if function, ok := value["function"].(map[string]interface{}); ok {
		name = stringValue(function["name"])
		args = function["arguments"]
	}
	if name == "" {
		name = stringValue(value["name"])
	}
	if name == "" {
		name = stringValue(value["tool_name"])
	}
	if args == nil {
		args = value["arguments"]
	}
	if args == nil {
		args = value["params"]
	}

	tool, ok := lookupToolByName(tools, name)
	if !ok {
		return api.ToolCall{}, false
	}
	if args == nil {
		if !toolAllowsEmptyArguments(tool) {
			return api.ToolCall{}, false
		}
		args = map[string]interface{}{}
	}

	return api.ToolCall{
		ID:   syntheticToolCallID(callID, index),
		Type: defaultToolType(toolType),
		Function: api.ToolFunction{
			Name:      name,
			Arguments: toolArgumentsJSONString(args),
		},
	}, true
}

func toolCallFromBareName(text string, tools []api.Tool, index int) (api.ToolCall, bool) {
	trimmed := strings.TrimSpace(strings.Trim(text, "\"'`"))
	if trimmed == "" {
		return api.ToolCall{}, false
	}

	if tool, ok := lookupToolByName(tools, trimmed); ok && toolAllowsEmptyArguments(tool) {
		return api.ToolCall{
			ID:   syntheticToolCallID("", index),
			Type: "function",
			Function: api.ToolFunction{
				Name:      trimmed,
				Arguments: "{}",
			},
		}, true
	}

	if strings.HasSuffix(trimmed, "()") {
		name := strings.TrimSpace(strings.TrimSuffix(trimmed, "()"))
		if tool, ok := lookupToolByName(tools, name); ok && toolAllowsEmptyArguments(tool) {
			return api.ToolCall{
				ID:   syntheticToolCallID("", index),
				Type: "function",
				Function: api.ToolFunction{
					Name:      name,
					Arguments: "{}",
				},
			}, true
		}
	}

	openIdx := strings.Index(trimmed, "(")
	closeIdx := strings.LastIndex(trimmed, ")")
	if openIdx <= 0 || closeIdx != len(trimmed)-1 {
		return api.ToolCall{}, false
	}

	name := strings.TrimSpace(trimmed[:openIdx])
	argText := strings.TrimSpace(trimmed[openIdx+1 : closeIdx])
	tool, ok := lookupToolByName(tools, name)
	if !ok {
		return api.ToolCall{}, false
	}
	if argText == "" {
		if !toolAllowsEmptyArguments(tool) {
			return api.ToolCall{}, false
		}
		argText = "{}"
	}

	return api.ToolCall{
		ID:   syntheticToolCallID("", index),
		Type: "function",
		Function: api.ToolFunction{
			Name:      name,
			Arguments: toolArgumentsJSONString(argText),
		},
	}, true
}

func lookupToolByName(tools []api.Tool, name string) (api.Tool, bool) {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return tool, true
		}
	}
	return api.Tool{}, false
}

func toolForName(tools []api.Tool, name string) api.Tool {
	tool, _ := lookupToolByName(tools, name)
	return tool
}

func toolAllowsEmptyArguments(tool api.Tool) bool {
	params, ok := tool.Function.Parameters.(map[string]interface{})
	if !ok || params == nil {
		return true
	}

	required, ok := params["required"].([]interface{})
	return !ok || len(required) == 0
}

func syntheticToolCallID(existing string, index int) string {
	if strings.TrimSpace(existing) != "" {
		return existing
	}
	return fmt.Sprintf("call_synth_%d", index)
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func stripMarkdownCodeFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return trimmed
	}
	lines = lines[1:]
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
