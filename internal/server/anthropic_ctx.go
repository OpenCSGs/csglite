package server

import (
	"os"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func (s *Server) anthropicPreferredNumCtx(modelID string) int {
	storageID := s.resolveLocalModelStorageID(modelID)
	preferred := 0
	if s.manager != nil {
		if modelDir, err := s.manager.ModelPath(storageID); err == nil {
			preferred = anthropicDefaultLocalNumCtx(modelDir)
		}
	}

	loaded := s.loadedModelNumCtx(storageID)
	if loaded > preferred {
		return loaded
	}
	if preferred > 0 {
		return preferred
	}
	if loaded > 0 {
		return loaded
	}
	return defaultAnthropicMaxInputTokens
}

func (s *Server) anthropicTokenLimitsForInfo(item api.ModelInfo) (int, int) {
	modelID := strings.TrimSpace(item.Model)
	if strings.EqualFold(strings.TrimSpace(item.Source), "local") {
		return s.anthropicPreferredNumCtx(modelID), defaultAnthropicMaxTokens
	}
	if strings.EqualFold(strings.TrimSpace(item.Source), "cloud") && s.cloud != nil {
		if limits, ok := s.cloud.ChatModelTokenLimits(modelID); ok {
			maxInputTokens := limits.MaxInputTokens
			if maxInputTokens <= 0 {
				maxInputTokens = defaultAnthropicMaxInputTokens
			}
			maxTokens := limits.MaxTokens
			if maxTokens <= 0 {
				maxTokens = defaultAnthropicMaxTokens
			}
			return maxInputTokens, maxTokens
		}
	}
	return defaultAnthropicMaxInputTokens, defaultAnthropicMaxTokens
}

func (s *Server) loadedModelNumCtx(modelID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if me, ok := s.engines[modelID]; ok {
		return me.numCtx
	}
	return 0
}

func anthropicDefaultLocalNumCtx(modelDir string) int {
	resolved := inference.ResolveNumCtx(modelDir, 0)
	if anthropicNumCtxExplicitlyConfigured() || resolved >= defaultAnthropicMaxInputTokens {
		return resolved
	}

	if maxPos := inference.ModelMaxPositionEmbeddings(modelDir); maxPos >= defaultAnthropicMaxInputTokens {
		return defaultAnthropicMaxInputTokens
	}
	return resolved
}

func anthropicNumCtxExplicitlyConfigured() bool {
	return strings.TrimSpace(os.Getenv("CSGHUB_LITE_LLAMA_NUM_CTX")) != ""
}
