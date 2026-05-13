package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

// POST /v1/chat/completions -- OpenAI-compatible chat completions
func (s *Server) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req api.OpenAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	if req.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	opts := inference.DefaultOptions()
	requestedNumCtx := 0
	requestedNumParallel := 0
	requestedNGPULayers := -1
	requestedCacheTypeK := ""
	requestedCacheTypeV := ""
	requestedDType := ""
	if req.Temperature != nil {
		opts.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		opts.TopP = *req.TopP
	}
	if req.MaxTokens != nil {
		opts.MaxTokens = *req.MaxTokens
	}
	if req.NumCtx != nil && *req.NumCtx > 0 {
		opts.NumCtx = *req.NumCtx
		requestedNumCtx = *req.NumCtx
	}
	if req.NumParallel != nil && *req.NumParallel > 0 {
		requestedNumParallel = *req.NumParallel
	}
	if req.NGPULayers != nil {
		requestedNGPULayers = *req.NGPULayers
	}
	if req.CacheTypeK != nil {
		requestedCacheTypeK = *req.CacheTypeK
	}
	if req.CacheTypeV != nil {
		requestedCacheTypeV = *req.CacheTypeV
	}
	if req.DType != nil {
		requestedDType = *req.DType
	}
	if req.Seed != nil {
		opts.Seed = *req.Seed
	}
	if len(req.Stop) > 0 {
		opts.Stop = req.Stop
	}

	eng, err := s.getChatEngine(r.Context(), req.Model, req.Source, requestedNumCtx, requestedNumParallel, requestedNGPULayers, requestedCacheTypeK, requestedCacheTypeV, requestedDType)
	if err != nil {
		if inference.HTTPStatusCode(err) != 0 {
			writeOpenAIInferenceError(w, err)
			return
		}
		writeOpenAIError(w, http.StatusBadRequest, "model_not_found", err.Error())
		return
	}
	defer s.touchEngine(req.Model)

	stream := req.Stream != nil && *req.Stream
	if openAIChatRequestHasToolFeatures(req) {
		s.handleOpenAIChatCompletionsWithTools(w, r, req, eng, opts, stream)
		return
	}
	if proxy, ok := eng.(inference.ChatCompletionProxier); ok {
		s.handleOpenAIChatCompletionsProxy(w, r, req, proxy, opts, stream)
		return
	}

	var messages []inference.Message
	for _, m := range req.Messages {
		messages = append(messages, inference.Message{Role: m.Role, Content: m.Content, ReasoningContent: m.ReasoningContent})
	}
	inputTokens := countMessageTokens(req.Messages)

	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		var full strings.Builder
		onToken := func(token string) {
			full.WriteString(token)
			chunk := api.OpenAIChatResponse{
				ID:      id,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   req.Model,
				Choices: []api.OpenAIChoice{{
					Index: 0,
					Delta: &api.Message{Role: "assistant", Content: token},
				}},
			}
			writeSSE(w, chunk)
		}

		_, err := eng.Chat(r.Context(), messages, opts, onToken)
		if err != nil {
			writeSSE(w, map[string]string{"error": err.Error()})
			return
		}
		s.recordAPIUsage(r, req.Model, inputTokens, estimateAnthropicTokens(full.String()))

		stop := "stop"
		writeSSE(w, api.OpenAIChatResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   req.Model,
			Choices: []api.OpenAIChoice{{
				Index:        0,
				Delta:        &api.Message{Role: "assistant", Content: ""},
				FinishReason: &stop,
			}},
		})
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	} else {
		response, err := eng.Chat(r.Context(), messages, opts, nil)
		if err != nil {
			writeOpenAIInferenceError(w, err)
			return
		}
		s.recordAPIUsage(r, req.Model, inputTokens, estimateAnthropicTokens(response))

		stop := "stop"
		writeJSON(w, http.StatusOK, api.OpenAIChatResponse{
			ID:      id,
			Object:  "chat.completion",
			Created: created,
			Model:   req.Model,
			Choices: []api.OpenAIChoice{{
				Index:        0,
				Message:      &api.Message{Role: "assistant", Content: response},
				FinishReason: &stop,
			}},
		})
	}
}

func (s *Server) handleOpenAIChatCompletionsProxy(
	w http.ResponseWriter,
	r *http.Request,
	req api.OpenAIChatRequest,
	proxy inference.ChatCompletionProxier,
	opts inference.Options,
	stream bool,
) {
	reqBody, err := openAIChatRequestToProxyBody(req, opts, stream)
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

	if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else if stream {
		w.Header().Set("Content-Type", "text/event-stream")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	if stream {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	}
	w.WriteHeader(http.StatusOK)
	if stream {
		_, _ = io.Copy(w, resp.Body)
		s.recordAPIUsage(r, req.Model, countMessageTokens(req.Messages), 0)
	} else {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return
		}
		var openAIResp api.OpenAIChatResponse
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&openAIResp); err == nil {
			inputTokens, outputTokens := openAIUsageTokens(openAIResp)
			if inputTokens == 0 {
				inputTokens = countMessageTokens(req.Messages)
			}
			s.recordAPIUsage(r, req.Model, inputTokens, outputTokens)
		} else {
			s.recordAPIUsage(r, req.Model, countMessageTokens(req.Messages), 0)
		}
		_, _ = w.Write(body)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func openAIChatRequestHasToolFeatures(req api.OpenAIChatRequest) bool {
	return hasChatToolFeatures(req.Messages, req.Tools)
}

func (s *Server) handleOpenAIChatCompletionsWithTools(
	w http.ResponseWriter,
	r *http.Request,
	req api.OpenAIChatRequest,
	eng inference.Engine,
	opts inference.Options,
	stream bool,
) {
	proxy, ok := eng.(inference.ChatCompletionProxier)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "selected model backend does not support tool calling")
		return
	}

	reqBody, err := openAIChatRequestToProxyBody(req, opts, false)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	resp, err := proxy.ChatCompletion(r.Context(), reqBody)
	if err != nil {
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeSSE(w, map[string]string{"error": err.Error()})
			return
		}
		writeOpenAIInferenceError(w, err)
		return
	}
	defer resp.Body.Close()

	var openAIResp api.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			writeSSE(w, map[string]string{"error": "decoding tool response: " + err.Error()})
			return
		}
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "decoding tool response: "+err.Error())
		return
	}

	openAIResp = normalizeOpenAIToolResponse(openAIResp, req.Tools)
	if openAIResp.ID == "" {
		openAIResp.ID = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	if openAIResp.Object == "" {
		openAIResp.Object = "chat.completion"
	}
	if openAIResp.Created == 0 {
		openAIResp.Created = time.Now().Unix()
	}
	if openAIResp.Model == "" {
		openAIResp.Model = req.Model
	}
	inputTokens, outputTokens := openAIUsageTokens(openAIResp)
	if inputTokens == 0 {
		inputTokens = countMessageTokens(req.Messages)
	}
	s.recordAPIUsage(r, req.Model, inputTokens, outputTokens)

	if !stream {
		writeJSON(w, http.StatusOK, openAIResp)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if len(openAIResp.Choices) == 0 {
		writeSSE(w, map[string]string{"error": "no choices in tool response"})
		return
	}

	choice := openAIResp.Choices[0]
	if choice.Message != nil && shouldEmitToolChunk(choice.Message) {
		writeSSE(w, api.OpenAIChatResponse{
			ID:      openAIResp.ID,
			Object:  "chat.completion.chunk",
			Created: openAIResp.Created,
			Model:   openAIResp.Model,
			Choices: []api.OpenAIChoice{{
				Index: choice.Index,
				Delta: choice.Message,
			}},
		})
	}

	finishReason := openAIChoiceFinishReason(choice)
	writeSSE(w, api.OpenAIChatResponse{
		ID:      openAIResp.ID,
		Object:  "chat.completion.chunk",
		Created: openAIResp.Created,
		Model:   openAIResp.Model,
		Choices: []api.OpenAIChoice{{
			Index:        choice.Index,
			Delta:        &api.Message{Role: "assistant", Content: ""},
			FinishReason: &finishReason,
		}},
	})
	fmt.Fprintf(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func openAIChatRequestToProxyBody(req api.OpenAIChatRequest, opts inference.Options, stream bool) (map[string]interface{}, error) {
	messages, err := ollamaMessagesToOpenAI(req.Messages)
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
	if opts.Seed >= 0 {
		body["seed"] = opts.Seed
	}
	if len(opts.Stop) > 0 {
		body["stop"] = opts.Stop
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = req.ToolChoice
	}
	if req.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	return body, nil
}

func openAIChoiceFinishReason(choice api.OpenAIChoice) string {
	if choice.FinishReason != nil && *choice.FinishReason != "" {
		return *choice.FinishReason
	}
	if choice.Message != nil && len(choice.Message.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

// GET /v1/models -- OpenAI-compatible model listing
func (s *Server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")

	models, err := s.listAvailableModelsWithRefresh(r.Context(), requestWantsModelRefresh(r))
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}

	seen := make(map[string]struct{}, len(models))
	data := make([]api.OpenAIModel, 0, len(models)+4)
	for _, item := range models {
		data = appendOpenAIModel(data, seen, item.Model, item.ModifiedAt)
	}

	writeJSON(w, http.StatusOK, api.OpenAIModelList{
		Object: "list",
		Data:   data,
	})
}

func writeOpenAIInferenceError(w http.ResponseWriter, err error) {
	status := inference.HTTPStatusCode(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}

	errType := "server_error"
	switch status {
	case http.StatusBadRequest:
		errType = "invalid_request_error"
	case http.StatusUnauthorized, http.StatusForbidden:
		errType = "authentication_error"
	case http.StatusNotFound:
		errType = "model_not_found"
	case http.StatusTooManyRequests:
		errType = "rate_limit_error"
	}

	writeOpenAIError(w, status, errType, inference.HTTPErrorMessage(err))
}

func writeOpenAIError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": msg,
			"type":    errType,
		},
	})
}

func appendOpenAIModel(data []api.OpenAIModel, seen map[string]struct{}, modelID string, createdAt time.Time) []api.OpenAIModel {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return data
	}
	if _, ok := seen[modelID]; ok {
		return data
	}
	seen[modelID] = struct{}{}

	created := int64(0)
	if !createdAt.IsZero() {
		created = createdAt.Unix()
	}

	return append(data, api.OpenAIModel{
		ID:      modelID,
		Object:  "model",
		Created: created,
		OwnedBy: "csghub",
	})
}
