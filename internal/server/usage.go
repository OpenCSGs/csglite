package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const (
	apiUsageSourceLocal    = "local"
	apiUsageSourceCloud    = "cloud"
	apiUsageSourceProvider = "provider"
	apiUsageSourceUnknown  = "unknown"
	apiUsageBuiltinKeyID   = "builtin:lite-chat"
	apiUsageBuiltinKeyName = "Lite Chat / Local API"
)

func (s *Server) recordAPIUsage(r *http.Request, model, source string, inputTokens, outputTokens int) {
	if s == nil || s.apiUsage == nil {
		return
	}
	keyID := apiUsageBuiltinKeyID
	keyName := apiUsageBuiltinKeyName
	if key, ok := authenticatedAPIKey(r); ok {
		keyID = key.ID
		keyName = key.Name
	}
	resolvedSource, sourceType, sourceName := s.resolveAPIUsageSource(r.Context(), model, source)
	_ = s.apiUsage.Add(config.APIUsageEvent{
		APIKeyID:     keyID,
		APIKeyName:   keyName,
		Model:        model,
		Source:       resolvedSource,
		SourceType:   sourceType,
		SourceName:   sourceName,
		InputTokens:  int64(inputTokens),
		OutputTokens: int64(outputTokens),
	})
}

func (s *Server) resolveAPIUsageSource(ctx context.Context, model, source string) (string, string, string) {
	source = strings.TrimSpace(source)
	normalized := strings.ToLower(source)
	if providerID := providerIDFromSource(source); providerID != "" {
		name := providerID
		if provider, ok := getThirdPartyProvider(providerID); ok && strings.TrimSpace(provider.Name) != "" {
			name = strings.TrimSpace(provider.Name)
		}
		return providerSource(providerID), apiUsageSourceProvider, name
	}
	switch normalized {
	case apiUsageSourceLocal:
		return apiUsageSourceLocal, apiUsageSourceLocal, ""
	case apiUsageSourceCloud:
		return apiUsageSourceCloud, apiUsageSourceCloud, "OpenCSG"
	}

	if s != nil && s.manager != nil {
		if _, err := s.manager.Get(model); err == nil {
			return apiUsageSourceLocal, apiUsageSourceLocal, ""
		}
	}
	if s != nil && !s.hasCloudCredential() {
		if providerSource := s.thirdPartyProviderSourceForModel(ctx, model); providerSource != "" {
			return s.resolveAPIUsageSource(ctx, model, providerSource)
		}
	}
	if models, err := s.listCloudModels(ctx, false); err == nil && modelInfoListContains(models, model) {
		return apiUsageSourceCloud, apiUsageSourceCloud, "OpenCSG"
	}
	if providerSource := s.thirdPartyProviderSourceForModel(ctx, model); providerSource != "" {
		return s.resolveAPIUsageSource(ctx, model, providerSource)
	}
	return apiUsageSourceUnknown, apiUsageSourceUnknown, ""
}

func countMessageTokens(messages []api.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateAnthropicTokens(contentAsString(msg.Content))
		total += estimateAnthropicTokens(msg.ReasoningContent)
	}
	if total == 0 {
		return 1
	}
	return total
}

func openAIUsageTokens(resp api.OpenAIChatResponse) (int, int) {
	if resp.Usage.TotalTokens > 0 || resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	output := ""
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		output = contentAsString(resp.Choices[0].Message.Content)
	}
	return 0, estimateAnthropicTokens(output)
}
