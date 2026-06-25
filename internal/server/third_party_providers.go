package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

func getThirdPartyProviderByAlias(alias string) (config.ThirdPartyProvider, bool) {
	alias = normalizeModelProvider(alias)
	if alias == "" {
		return config.ThirdPartyProvider{}, false
	}
	for _, provider := range config.GetProviders() {
		if normalizeModelProvider(provider.ID) == alias || normalizeModelProvider(provider.Name) == alias {
			return provider, true
		}
	}
	return config.ThirdPartyProvider{}, false
}

func (s *Server) listSelectedThirdPartyProviderModels(ctx context.Context) []api.ModelInfo {
	providers := config.GetProviders()
	if len(providers) == 0 {
		return nil
	}

	out := make([]api.ModelInfo, 0)
	for _, provider := range providers {
		if !provider.Enabled {
			continue
		}
		selections := config.GetProviderModelSelections(provider.ID)
		if len(selections) == 0 {
			continue
		}
		for _, selection := range selections {
			if providerModelOriginalID(selection) == "" {
				continue
			}
			out = append(out, providerModelInfoFromSelection(provider, selection))
		}
	}
	return out
}

func providerModelInfoFromSelection(provider config.ThirdPartyProvider, selection config.ProviderModelSelection) api.ModelInfo {
	originalModel := providerModelOriginalID(selection)
	labelName := originalModel
	if catalogDisplayName := strings.TrimSpace(selection.CatalogDisplayName); catalogDisplayName != "" {
		labelName = catalogDisplayName
	}
	if displayName := strings.TrimSpace(selection.DisplayName); displayName != "" {
		labelName = displayName
	}
	label := fmt.Sprintf("%s [%s]", labelName, provider.Name)
	modelProvider := normalizeModelProvider(provider.Name)
	if modelProvider == "" {
		modelProvider = normalizeModelProvider(provider.ID)
	}
	pipelineTag := strings.TrimSpace(selection.PipelineTag)
	inputModalities := append([]string{}, selection.InputModalities...)
	outputModalities := append([]string{}, selection.OutputModalities...)
	if pipelineTag == "" || len(inputModalities) == 0 || len(outputModalities) == 0 {
		inferredTag, inferredInputs, inferredOutputs := inferThirdPartyModelMetadata(provider, thirdPartyProviderModel{ID: originalModel})
		if pipelineTag == "" {
			pipelineTag = inferredTag
		}
		if len(inputModalities) == 0 {
			inputModalities = inferredInputs
		}
		if len(outputModalities) == 0 {
			outputModalities = inferredOutputs
		}
	}
	model := api.ModelInfo{
		Name:             originalModel,
		Model:            originalModel,
		Label:            label,
		DisplayName:      label,
		Format:           "api",
		Source:           providerSource(provider.ID),
		Provider:         modelProvider,
		Category:         categoryForPipelineTag(pipelineTag),
		PipelineTag:      pipelineTag,
		InputModalities:  inputModalities,
		OutputModalities: outputModalities,
	}
	return applyProviderModelMetadata(model, selection)
}

func providerModelCatalogDisplayName(provider modelManageProvider, model api.ModelInfo) string {
	modelID := strings.TrimSpace(model.Model)
	for _, value := range []string{model.DisplayName, model.Label, model.Name} {
		value = strings.TrimSpace(value)
		if value == "" || value == modelID {
			continue
		}
		suffix := fmt.Sprintf(" [%s]", provider.Name)
		if strings.HasSuffix(value, suffix) {
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
		}
		if value != "" && value != modelID {
			return value
		}
	}
	return ""
}

func providerManagedSource(provider modelManageProvider) string {
	if provider.IsCloud() {
		return "cloud"
	}
	return providerSource(provider.ID)
}

func providerManagedModelProviderID(provider modelManageProvider) string {
	if provider.IsCloud() {
		return provider.Name
	}
	return normalizeModelProvider(provider.ID)
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
		Data []thirdPartyProviderModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]api.ModelInfo, 0, len(result.Data))
	for _, item := range result.Data {
		modelID := strings.TrimSpace(item.ID)
		if modelID == "" {
			modelID = strings.TrimSpace(item.Name)
		}
		if modelID == "" {
			continue
		}
		labelName := modelID
		if displayName := strings.TrimSpace(item.DisplayName); displayName != "" {
			labelName = displayName
		}
		label := fmt.Sprintf("%s [%s]", labelName, provider.Name)
		modelProvider := normalizeModelProvider(provider.Name)
		if modelProvider == "" {
			modelProvider = normalizeModelProvider(provider.ID)
		}
		pipelineTag, inputModalities, outputModalities := inferThirdPartyModelMetadata(provider, item)
		models = append(models, api.ModelInfo{
			Name:             modelID,
			Model:            modelID,
			Label:            label,
			DisplayName:      label,
			Format:           "api",
			Source:           providerSource(provider.ID),
			Provider:         modelProvider,
			Category:         categoryForPipelineTag(pipelineTag),
			PipelineTag:      pipelineTag,
			InputModalities:  inputModalities,
			OutputModalities: outputModalities,
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
	return inference.NewOpenAICompatibleEngine(baseURL, providerOriginalModelID(provider.ID, modelID), apiKey), nil
}

func providerOriginalModelID(providerID, modelID string) string {
	modelID = strings.TrimSpace(modelID)
	for _, selection := range config.GetProviderModelSelections(providerID) {
		if strings.TrimSpace(selection.Model) == modelID {
			if original := providerModelOriginalID(selection); original != "" {
				return original
			}
		}
	}
	return modelID
}
