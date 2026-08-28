package server

import (
	"context"
	"math"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
)

type requestCostSnapshot struct {
	InputPerMillion  float64
	OutputPerMillion float64
	Currency         string
	Known            bool
}

func pricingCacheKey(source, model string) string {
	return strings.ToLower(strings.TrimSpace(source)) + "\x00" + strings.TrimSpace(model)
}

func (s *Server) rememberModelPricing(models []api.ModelInfo) {
	if s == nil {
		return
	}
	s.pricingMu.Lock()
	defer s.pricingMu.Unlock()
	if s.pricingCache == nil {
		s.pricingCache = make(map[string]requestCostSnapshot)
	}
	for _, info := range models {
		snapshot := requestCostSnapshot{}
		if info.Pricing != nil && info.Pricing.InputTokenPrice != nil && info.Pricing.OutputTokenPrice != nil {
			input, output := info.Pricing.InputTokenPrice, info.Pricing.OutputTokenPrice
			if input.PricePerMillion >= 0 && output.PricePerMillion >= 0 &&
				!math.IsNaN(input.PricePerMillion) && !math.IsNaN(output.PricePerMillion) &&
				input.Currency != "" && input.Currency == output.Currency {
				snapshot = requestCostSnapshot{
					InputPerMillion: input.PricePerMillion, OutputPerMillion: output.PricePerMillion,
					Currency: input.Currency, Known: true,
				}
			}
		}
		s.pricingCache[pricingCacheKey(info.Source, info.Model)] = snapshot
	}
}

func (s *Server) requestPricingSnapshot(source, model string, inputTokens, outputTokens int64) (requestCostSnapshot, float64) {
	if s == nil {
		return requestCostSnapshot{}, 0
	}
	s.pricingMu.RLock()
	snapshot, ok := s.pricingCache[pricingCacheKey(source, model)]
	s.pricingMu.RUnlock()
	if !ok || !snapshot.Known {
		return requestCostSnapshot{}, 0
	}
	cost := (float64(inputTokens)*snapshot.InputPerMillion + float64(outputTokens)*snapshot.OutputPerMillion) / 1_000_000
	return snapshot, cost
}

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
	PoolID                     string
	PoolName                   string
	PoolModel                  string
	ActualMemberID             string
	MemberModel                string
	Policy                     string
	RouterProfileID            string
	RouterProfileVersion       int
	RouterProfileSchemaVersion int
	RouterAlgorithm            string
	RoutingTextVersion         string
	RouterConfidence           float64
	RouterMargin               float64
	RouterSimilarity           float64
	SemanticRouted             bool
	SemanticCluster            int
	SemanticClusterID          string
	SemanticDistance           float64
	SemanticOOD                bool
	SemanticFallback           bool
	SemanticFallbackReason     string
	FallbackCount              int64
	LimitedCount               int64
}

func (s *Server) recordAPIUsage(r *http.Request, model, source string, inputTokens, outputTokens int) {
	memberSource, pool := providerPoolUsageCaptureFromContext(r.Context()).get()
	if memberSource != "" {
		source = memberSource
	}
	s.recordAPIUsageWithPool(r, model, source, inputTokens, outputTokens, pool)
}

func (s *Server) recordAPIUsageWithPool(r *http.Request, model, source string, inputTokens, outputTokens int, pool *apiUsagePoolMetadata) {
	if s == nil {
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
	pricingModel := model
	if pool != nil && pool.MemberModel != "" {
		pricingModel = pool.MemberModel
	}
	costSnapshot, estimatedCost := s.requestPricingSnapshot(
		resolvedSource, pricingModel, int64(inputTokens), int64(outputTokens),
	)
	observationFromContext(r.Context()).setUsage(
		model,
		resolvedSource,
		sourceType,
		sourceName,
		int64(inputTokens),
		int64(outputTokens),
		pool,
	)
	if s.apiUsage == nil {
		return
	}
	_ = s.apiUsage.Add(config.APIUsageEvent{
		APIKeyID:       keyID,
		APIKeyName:     keyName,
		Model:          model,
		Source:         resolvedSource,
		SourceType:     sourceType,
		SourceName:     sourceName,
		PoolID:         poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.PoolID }),
		PoolName:       poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.PoolName }),
		PoolModel:      poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.PoolModel }),
		ActualMemberID: poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.ActualMemberID }),
		MemberModel:    poolMetadataValue(pool, func(value *apiUsagePoolMetadata) string { return value.MemberModel }),
		EstimatedCost:  estimatedCost,
		CostCurrency:   costSnapshot.Currency,
		CostKnown:      costSnapshot.Known,
		FallbackCount:  poolMetadataCount(pool, func(value *apiUsagePoolMetadata) int64 { return value.FallbackCount }),
		LimitedCount:   poolMetadataCount(pool, func(value *apiUsagePoolMetadata) int64 { return value.LimitedCount }),
		InputTokens:    int64(inputTokens),
		OutputTokens:   int64(outputTokens),
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
