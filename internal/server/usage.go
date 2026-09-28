package server

import (
	"bytes"
	"context"
	"encoding/json"
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

// streamUsageCapture keeps the tail of a proxied upstream stream so the usage
// block a provider emits at the end of the stream can still be recorded after
// the bytes have been forwarded to the client.
type streamUsageCapture struct {
	tail []byte
}

func (c *streamUsageCapture) Write(p []byte) (int, error) {
	c.tail = appendUsageTail(c.tail, p)
	return len(p), nil
}

// usage reports the token counts advertised by the streamed response. It
// returns false when the stream carried no usage at all, leaving the caller to
// fall back to its own estimate.
func (c *streamUsageCapture) usage() (int, int, bool) {
	result := observationResponseUsageFromBodies(c.tail)
	if result.inputTokens <= 0 && result.outputTokens <= 0 {
		return 0, 0, false
	}
	return int(result.inputTokens), int(result.outputTokens), true
}

// extractOpenAIStreamContent scans captured SSE bytes from an OpenAI streaming
// response and concatenates assistant content deltas into a single string.
func extractOpenAIStreamContent(tail []byte) string {
	var sb strings.Builder
	for _, line := range bytes.Split(tail, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &chunk) == nil {
			for _, ch := range chunk.Choices {
				sb.WriteString(ch.Delta.Content)
			}
		}
	}
	return sb.String()
}

// extractAnthropicStreamContent scans captured SSE bytes from an Anthropic
// streaming response and concatenates text deltas into a single string.
func extractAnthropicStreamContent(tail []byte) string {
	var sb strings.Builder
	for _, line := range bytes.Split(tail, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data: "))
		var chunk struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal(data, &chunk) == nil {
			if chunk.Type == "content_block_delta" && chunk.Delta.Text != "" {
				sb.WriteString(chunk.Delta.Text)
			}
		}
	}
	return sb.String()
}

// sanePrompt returns true when a prompt-token count is positive and below a
// generous single-request ceiling. A zero or negative prompt means the backend
// did not report it, so the caller should fall back to its own estimate.
func sanePrompt(prompt int64) bool {
	const maxReasonableTokens = 10_000_000
	return prompt > 0 && prompt <= maxReasonableTokens
}

// saneCompletion returns true when a completion-token count is positive and
// below a generous single-request ceiling. A zero completion is treated as
// "not reported" rather than "real zero output": the OnUsage callback only
// stores values > 0, so a zero reaching resolveUsageTokens means the backend
// omitted completion_tokens. Falling back to the caller's text-based estimate
// is safer than trusting the missing value as real. When the model truly
// produced no output the estimate is also 0, so the recorded value is the same
// — only the "estimated" tag differs, which is an acceptable trade-off.
func saneCompletion(completion int64) bool {
	const maxReasonableTokens = 10_000_000
	return completion > 0 && completion <= maxReasonableTokens
}

// resolveUsageTokens prefers the token counts a backend actually reported and
// falls back to the caller's estimate per side: when the backend reports only
// one of prompt/completion, the other side uses the estimate and the result is
// marked as not fully real. The returned bool is true only when both sides
// came from the backend.
func resolveUsageTokens(realIn, realOut int64, estIn, estOut int) (in, out int, real bool) {
	inSane := sanePrompt(realIn)
	outSane := saneCompletion(realOut)
	if inSane && outSane {
		return int(realIn), int(realOut), true
	}
	in, out = estIn, estOut
	if inSane {
		in = int(realIn)
	}
	if outSane {
		out = int(realOut)
	}
	return in, out, false
}

type usageEstimatedContextKey struct{}

// markUsageEstimated tags the request so recordAPIUsage counts the recorded
// values as estimated rather than real. Used at sites that fell back to a
// heuristic because the backend reported no usable usage.
func markUsageEstimated(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), usageEstimatedContextKey{}, true))
}

func usageEstimatedFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(usageEstimatedContextKey{}).(bool)
	return v
}

// recordResolvedUsage records token usage, preferring backend-reported values
// over the caller's estimate and tagging the record as estimated when the
// backend reported nothing usable.
func (s *Server) recordResolvedUsage(r *http.Request, model, source string, realIn, realOut int64, estIn, estOut int) {
	in, out, real := resolveUsageTokens(realIn, realOut, estIn, estOut)
	if !real {
		r = markUsageEstimated(r)
	}
	s.recordAPIUsage(r, model, source, in, out)
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
		Estimated:      usageEstimatedFromContext(r.Context()),
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
	return 0, estimateOpenAIOutputTokens(resp)
}

// estimateOpenAIOutputTokens extracts assistant text from a non-streaming
// OpenAI chat response and returns an estimated output token count. Used at
// sites that need to estimate output when the upstream reported prompt tokens
// but omitted completion_tokens.
func estimateOpenAIOutputTokens(resp api.OpenAIChatResponse) int {
	output := ""
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		output = contentAsString(resp.Choices[0].Message.Content)
	}
	return estimateAnthropicTokens(output)
}
