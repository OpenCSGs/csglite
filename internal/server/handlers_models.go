package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
)

const (
	defaultAnthropicMaxInputTokens = 32768
	defaultAnthropicMaxTokens      = 8192
)

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if requestWantsAnthropicModels(r) {
		s.handleAnthropicModels(w, r)
		return
	}
	s.handleOpenAIModels(w, r)
}

func requestWantsAnthropicModels(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("anthropic-version")) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("anthropic-beta")) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("x-api-key")) != "" && strings.TrimSpace(r.Header.Get("authorization")) == "" {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(r.UserAgent())), "claude")
}

func (s *Server) listAvailableModels(ctx context.Context) ([]api.ModelInfo, error) {
	return s.listAvailableModelsWithRefresh(ctx, false)
}

func (s *Server) handleAnthropicModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")

	models, err := s.listAvailableModelsWithRefresh(r.Context(), requestWantsModelRefresh(r))
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, err.Error())
		return
	}

	data := make([]api.AnthropicModelInfo, 0, len(models))
	for _, item := range models {
		data = append(data, s.anthropicModelFromInfo(item))
	}

	resp := api.AnthropicModelListResponse{
		Data:    data,
		HasMore: false,
	}
	if len(data) > 0 {
		resp.FirstID = data[0].ID
		resp.LastID = data[len(data)-1].ID
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) anthropicModelFromInfo(item api.ModelInfo) api.AnthropicModelInfo {
	createdAt := time.Unix(0, 0).UTC().Format(time.RFC3339)
	if !item.ModifiedAt.IsZero() {
		createdAt = item.ModifiedAt.UTC().Format(time.RFC3339)
	}

	displayName := strings.TrimSpace(item.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(item.Model)
	}

	supportsVision := item.HasMMProj || strings.EqualFold(strings.TrimSpace(item.PipelineTag), "image-text-to-text")
	supportsThinking := strings.Contains(strings.ToLower(item.Model), "thinking")
	maxInputTokens, maxTokens := s.anthropicTokenLimitsForInfo(item)

	return api.AnthropicModelInfo{
		ID:             strings.TrimSpace(item.Model),
		Type:           "model",
		DisplayName:    displayName,
		CreatedAt:      createdAt,
		MaxInputTokens: maxInputTokens,
		MaxTokens:      maxTokens,
		Capabilities: api.AnthropicModelCapabilities{
			Batch:             api.AnthropicCapabilitySupport{Supported: false},
			Citations:         api.AnthropicCapabilitySupport{Supported: false},
			CodeExecution:     api.AnthropicCapabilitySupport{Supported: false},
			ContextManagement: api.AnthropicContextManagementCapability{Supported: false},
			ImageInput:        api.AnthropicCapabilitySupport{Supported: supportsVision},
			PDFInput:          api.AnthropicCapabilitySupport{Supported: false},
			StructuredOutputs: api.AnthropicCapabilitySupport{Supported: false},
			Thinking: api.AnthropicThinkingCapability{
				Supported: supportsThinking,
				Types: api.AnthropicThinkingTypes{
					Adaptive: api.AnthropicCapabilitySupport{Supported: supportsThinking},
					Enabled:  api.AnthropicCapabilitySupport{Supported: supportsThinking},
				},
			},
		},
	}
}
