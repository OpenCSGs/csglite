package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/pkg/api"
)

func (s *Server) handleEmbeddingChat(w http.ResponseWriter, r *http.Request, req api.ChatRequest) {
	input := latestUserText(req.Messages)
	if input == "" {
		writeError(w, http.StatusBadRequest, "embedding input is required")
		return
	}

	requestedNumCtx := 0
	requestedNGPULayers := -1
	requestedDType := ""
	if req.Options != nil {
		if req.Options.NumCtx > 0 {
			requestedNumCtx = req.Options.NumCtx
		}
		if req.Options.NGPULayers != nil {
			requestedNGPULayers = *req.Options.NGPULayers
		}
		if req.Options.DType != "" {
			requestedDType = req.Options.DType
		}
	}

	eng, err := s.getEmbeddingEngine(r.Context(), req.Model, req.Source, requestedNumCtx, requestedNGPULayers, requestedDType)
	if err != nil {
		writeInferenceError(w, err)
		return
	}
	proxy, ok := eng.(inference.EmbeddingsProxier)
	if !ok {
		writeError(w, http.StatusBadRequest, "selected model backend does not support embeddings")
		return
	}
	defer s.touchEngineKey(engineCacheKey(s.resolveLocalModelStorageID(req.Model), engineModeEmbed))

	resp, err := proxy.Embeddings(r.Context(), map[string]interface{}{
		"model": req.Model,
		"input": input,
	})
	if err != nil {
		writeInferenceError(w, err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading embeddings response: "+err.Error())
		return
	}
	content := formatEmbeddingChatContent(body)
	s.recordAPIUsage(r, req.Model, req.Source, openAIEmbeddingPromptTokens(body, input), 0)

	stream := req.Stream == nil || *req.Stream
	if !stream {
		writeJSON(w, http.StatusOK, api.ChatResponse{
			Model: req.Model,
			Message: &api.Message{
				Role:    "assistant",
				Content: content,
			},
			Done:      true,
			CreatedAt: time.Now(),
		})
		return
	}
	if requestWantsSSE(r) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		writeSSE(w, api.ChatResponse{
			Model: req.Model,
			Message: &api.Message{
				Role:    "assistant",
				Content: content,
			},
			Done:      false,
			CreatedAt: time.Now(),
		})
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
	writeNDJSON(w, api.ChatResponse{
		Model: req.Model,
		Message: &api.Message{
			Role:    "assistant",
			Content: content,
		},
		Done:      false,
		CreatedAt: time.Now(),
	})
	writeNDJSON(w, api.ChatResponse{
		Model:     req.Model,
		Done:      true,
		CreatedAt: time.Now(),
	})
}

// POST /v1/embeddings -- OpenAI-compatible embeddings
func (s *Server) handleOpenAIEmbeddings(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	var req api.OpenAIEmbeddingsRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	var rawReq map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &rawReq); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if req.Input == nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "input is required")
		return
	}

	requestedNumCtx := 0
	requestedNGPULayers := -1
	requestedDType := ""
	if req.NumCtx != nil && *req.NumCtx > 0 {
		requestedNumCtx = *req.NumCtx
	}
	if req.NGPULayers != nil {
		requestedNGPULayers = *req.NGPULayers
	}
	if req.DType != nil {
		requestedDType = *req.DType
	}

	eng, err := s.getEmbeddingEngine(r.Context(), req.Model, req.Source, requestedNumCtx, requestedNGPULayers, requestedDType)
	if err != nil {
		if inference.HTTPStatusCode(err) != 0 {
			writeOpenAIInferenceError(w, err)
			return
		}
		writeOpenAIError(w, http.StatusBadRequest, "model_not_found", err.Error())
		return
	}
	proxy, ok := eng.(inference.EmbeddingsProxier)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "selected model backend does not support embeddings")
		return
	}
	defer s.touchEngineKey(engineCacheKey(s.resolveLocalModelStorageID(req.Model), engineModeEmbed))

	reqBody, err := openAIEmbeddingsRequestToProxyBody(req, rawReq)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	resp, err := proxy.Embeddings(r.Context(), reqBody)
	if err != nil {
		writeOpenAIInferenceError(w, err)
		return
	}
	defer resp.Body.Close()

	if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	s.recordAPIUsage(r, req.Model, req.Source, openAIEmbeddingPromptTokens(body, req.Input), 0)
	_, _ = w.Write(body)
}

func openAIEmbeddingsRequestToProxyBody(req api.OpenAIEmbeddingsRequest, raw map[string]interface{}) (map[string]interface{}, error) {
	body := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		body[key] = value
	}
	body["model"] = req.Model
	body["input"] = req.Input
	delete(body, "source")
	delete(body, "num_ctx")
	delete(body, "n_gpu_layers")
	delete(body, "dtype")
	return body, nil
}

func formatEmbeddingChatContent(body []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err == nil {
		return buf.String()
	}
	return string(body)
}

func openAIEmbeddingPromptTokens(body []byte, input interface{}) int {
	var resp api.OpenAIEmbeddingsResponse
	if err := json.Unmarshal(body, &resp); err == nil && resp.Usage.PromptTokens > 0 {
		return resp.Usage.PromptTokens
	}
	return estimateEmbeddingInputTokens(input)
}

func estimateEmbeddingInputTokens(input interface{}) int {
	switch v := input.(type) {
	case string:
		return estimateAnthropicTokens(v)
	case []interface{}:
		total := 0
		for _, item := range v {
			if s, ok := item.(string); ok {
				total += estimateAnthropicTokens(s)
			}
		}
		return total
	default:
		return int(math.Max(1, float64(estimateAnthropicTokens(fmt.Sprint(input)))))
	}
}
