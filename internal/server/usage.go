package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	apiUsageSourceLocal    = "local"
	apiUsageSourceCloud    = "cloud"
	apiUsageSourceProvider = "provider"
	apiUsageSourcePool     = "pool"
	apiUsageSourceUnknown  = "unknown"
	apiUsageBuiltinKeyID   = "builtin:lite-chat"
	apiUsageBuiltinKeyName = "Lite Chat / Local API"
)

type apiUsagePoolMetadata struct {
	PoolID        string
	PoolName      string
	PoolModel     string
	MemberModel   string
	FallbackCount int64
	LimitedCount  int64
}

func (s *Server) recordAPIUsage(r *http.Request, model, source string, inputTokens, outputTokens int) {
	memberSource, pool := providerPoolUsageCaptureFromContext(r.Context()).get()
	if memberSource != "" {
		source = memberSource
	}
	s.recordAPIUsageWithPool(r, model, source, inputTokens, outputTokens, pool)
}

func (s *Server) recordAPIUsageWithPool(r *http.Request, model, source string, inputTokens, outputTokens int, pool *apiUsagePoolMetadata) {
	if s == nil || s.apiUsage == nil {
		return
	}
	keyID := apiUsageBuiltinKeyID
	keyName := apiUsageBuiltinKeyName
	if key, ok := authenticatedAPIKey(r); ok {
		keyID = key.ID
		keyName = key.Name
	}
	if routeSource := providerRouteSourceFromContext(r.Context()); routeSource != "" && pool == nil {
		source = routeSource
	}
	resolvedSource, sourceType, sourceName := s.resolveAPIUsageSource(r.Context(), model, source)
	_ = s.apiUsage.Add(config.APIUsageEvent{
		APIKeyID:      keyID,
		APIKeyName:    keyName,
		Model:         model,
		Source:        resolvedSource,
		SourceType:    sourceType,
		SourceName:    sourceName,
		PoolID:        poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.PoolID }),
		PoolName:      poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.PoolName }),
		PoolModel:     poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.PoolModel }),
		MemberModel:   poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.MemberModel }),
		FallbackCount: poolMetadataCount(pool, func(value *apiUsagePoolMetadata) int64 { return value.FallbackCount }),
		LimitedCount:  poolMetadataCount(pool, func(value *apiUsagePoolMetadata) int64 { return value.LimitedCount }),
		InputTokens:   int64(inputTokens),
		OutputTokens:  int64(outputTokens),
	})
}

func poolMetadataValue(metadata *apiUsagePoolMetadata, value func(*apiUsagePoolMetadata) string) string {
	if metadata == nil {
		return ""
	}
	return strings.TrimSpace(value(metadata))
}

func poolMetadataCount(metadata *apiUsagePoolMetadata, value func(*apiUsagePoolMetadata) int64) int64 {
	if metadata == nil {
		return 0
	}
	return value(metadata)
}

func (s *Server) resolveAPIUsageSource(ctx context.Context, model, source string) (string, string, string) {
	source = strings.TrimSpace(source)
	normalized := strings.ToLower(source)
	if poolID := poolIDFromSource(source); poolID != "" {
		for _, pool := range config.GetProviderPools() {
			if pool.ID == poolID {
				return poolSource(poolID), apiUsageSourcePool, pool.Name
			}
		}
		return poolSource(poolID), apiUsageSourcePool, poolID
	}
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

	if s.isLocalAPIUsageModel(model) {
		return apiUsageSourceLocal, apiUsageSourceLocal, ""
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

func (s *Server) isLocalAPIUsageModel(modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if s == nil || s.manager == nil || modelID == "" {
		return false
	}
	if _, err := s.manager.Get(modelID); err == nil {
		return true
	}
	if _, err := s.manager.ResolveLocalModel(modelID); err == nil {
		return true
	}
	return s.matchesLegacyLocalAPIUsageModel(modelID)
}

func (s *Server) matchesLegacyLocalAPIUsageModel(modelID string) bool {
	_, legacyName, err := csghub.ParseModelID(modelID)
	if err != nil {
		return false
	}
	models, err := s.manager.List()
	if err != nil {
		return false
	}
	publicIDs := model.PublicModelIDs(models)
	matches := 0
	for _, item := range models {
		if item == nil {
			continue
		}
		fullName := strings.TrimSpace(item.FullName())
		if strings.TrimSpace(item.Name) == legacyName || strings.TrimSpace(publicIDs[fullName]) == legacyName {
			matches++
		}
	}
	return matches == 1
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
