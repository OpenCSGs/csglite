package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

const thirdPartyProviderSourcePrefix = "provider:"
const (
	bigModelProviderType        = "bigmodel"
	bigModelOfficialBaseURL     = "https://open.bigmodel.cn/api/paas/v4"
	bigModelLegacyCodingBaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
)

func providerSource(id string) string {
	return thirdPartyProviderSourcePrefix + strings.TrimSpace(id)
}

func providerIDFromSource(source string) string {
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(source, thirdPartyProviderSourcePrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(source, thirdPartyProviderSourcePrefix))
}

func getThirdPartyProvider(id string) (config.ThirdPartyProvider, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return config.ThirdPartyProvider{}, false
	}
	for _, provider := range config.GetProviders() {
		if provider.ID == id {
			return provider, true
		}
	}
	return config.ThirdPartyProvider{}, false
}

func (s *Server) invalidateThirdPartyProviderModelsCache() {
	if s == nil {
		return
	}
	s.thirdPartyModelsCacheMu.Lock()
	s.thirdPartyModelsCache = nil
	s.thirdPartyModelsCacheAt = time.Time{}
	s.thirdPartyModelsCacheMu.Unlock()
}

func (s *Server) listThirdPartyProviderModels(ctx context.Context) []api.ModelInfo {
	providers := config.GetProviders()
	if len(providers) == 0 {
		return nil
	}

	// Use cached data if available and fresh (within 30 seconds).
	// This avoids repeated API calls to third-party providers.
	s.thirdPartyModelsCacheMu.Lock()
	if s.thirdPartyModelsCache != nil && time.Since(s.thirdPartyModelsCacheAt) < 30*time.Second {
		cache := s.thirdPartyModelsCache
		s.thirdPartyModelsCacheMu.Unlock()
		return cache
	}
	s.thirdPartyModelsCacheMu.Unlock()

	// Query all providers in parallel to reduce latency.
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := make([]api.ModelInfo, 0, len(providers)*4)

	for _, provider := range providers {
		if !provider.Enabled {
			continue
		}
		wg.Add(1)
		go func(p config.ThirdPartyProvider) {
			defer wg.Done()
			models, err := listOpenAICompatibleProviderModels(ctx, p)
			if err != nil {
				log.Printf("listThirdPartyProviderModels: provider %s (%s) error: %v", p.ID, p.Name, err)
				return
			}
			mu.Lock()
			out = append(out, models...)
			mu.Unlock()
		}(provider)
	}
	wg.Wait()

	// Cache the result.
	if len(out) > 0 {
		s.thirdPartyModelsCacheMu.Lock()
		s.thirdPartyModelsCache = out
		s.thirdPartyModelsCacheAt = time.Now()
		s.thirdPartyModelsCacheMu.Unlock()
	}

	return out
}

func listOpenAICompatibleProviderModels(ctx context.Context, provider config.ThirdPartyProvider) ([]api.ModelInfo, error) {
	baseURL := normalizeThirdPartyProviderBaseURL(provider)
	if baseURL == "" || strings.TrimSpace(provider.APIKey) == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(provider.APIKey))

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("listing provider models failed %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]api.ModelInfo, 0, len(result.Data))
	for _, item := range result.Data {
		modelID := strings.TrimSpace(item.ID)
		if modelID == "" {
			continue
		}
		label := fmt.Sprintf("%s [%s]", modelID, provider.Name)
		models = append(models, api.ModelInfo{
			Name:        modelID,
			Model:       modelID,
			Label:       label,
			DisplayName: label,
			Format:      "api",
			Source:      providerSource(provider.ID),
			PipelineTag: "text-generation",
		})
	}
	return models, nil
}

func normalizeThirdPartyProviderBaseURL(provider config.ThirdPartyProvider) string {
	baseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	providerType := strings.TrimSpace(strings.ToLower(provider.Provider))
	if providerType == bigModelProviderType && strings.EqualFold(baseURL, bigModelLegacyCodingBaseURL) {
		return bigModelOfficialBaseURL
	}
	return baseURL
}

func validateThirdPartyProvider(ctx context.Context, provider config.ThirdPartyProvider) (int, error) {
	if strings.TrimSpace(provider.BaseURL) == "" {
		return 0, fmt.Errorf("base_url is required")
	}
	if strings.TrimSpace(provider.APIKey) == "" {
		return 0, fmt.Errorf("api_key is required")
	}
	models, err := listOpenAICompatibleProviderModels(ctx, provider)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch model list: %w", err)
	}
	if len(models) == 0 {
		return 0, fmt.Errorf("no models returned from provider")
	}
	return len(models), nil
}

func newThirdPartyProviderEngine(source, modelID string) (inference.Engine, error) {
	providerID := providerIDFromSource(source)
	provider, ok := getThirdPartyProvider(providerID)
	if !ok {
		return nil, inference.NewHTTPStatusError(http.StatusNotFound, "third-party provider not found")
	}
	if !provider.Enabled {
		return nil, inference.NewHTTPStatusError(http.StatusForbidden, "third-party provider is disabled")
	}
	baseURL := normalizeThirdPartyProviderBaseURL(provider)
	apiKey := strings.TrimSpace(provider.APIKey)
	if baseURL == "" || apiKey == "" {
		return nil, inference.NewHTTPStatusError(http.StatusBadRequest, "third-party provider is missing base URL or API key")
	}
	return inference.NewOpenAICompatibleEngine(baseURL, modelID, apiKey), nil
}
