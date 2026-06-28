package server

import (
	"context"
	"sort"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

const localModelProvider = "local"

func normalizeModelProvider(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func modelProviderID(info api.ModelInfo) string {
	if isLocalModelInfo(info) {
		return localModelProvider
	}
	if providerID := providerIDFromSource(info.Source); providerID != "" {
		return normalizeModelProvider(providerID)
	}
	if isCloudModelInfo(info) {
		if provider := normalizeModelProvider(info.Provider); provider != "" {
			return provider
		}
		return config.DefaultCloudProviderName
	}
	if provider := normalizeModelProvider(info.Provider); provider != "" {
		return provider
	}
	if ownedBy := normalizeModelProvider(info.OwnedBy); ownedBy != "" {
		return ownedBy
	}
	return normalizeModelProvider(info.Source)
}

func modelProviderAliases(info api.ModelInfo) []string {
	if isLocalModelInfo(info) {
		return []string{localModelProvider}
	}
	if providerID := providerIDFromSource(info.Source); providerID != "" {
		values := []string{providerID, info.Provider}
		if provider, ok := getThirdPartyProvider(providerID); ok {
			values = append(values, provider.Name)
		}
		return normalizedProviderAliases(values)
	}
	if isCloudModelInfo(info) {
		return normalizedProviderAliases([]string{info.Provider, config.DefaultCloudProviderName})
	}

	return normalizedProviderAliases([]string{info.Provider, info.OwnedBy, info.Source})
}

func normalizedProviderAliases(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = normalizeModelProvider(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func modelMatchesProvider(info api.ModelInfo, provider string) bool {
	provider = normalizeModelProvider(provider)
	if provider == "" {
		return true
	}
	for _, alias := range modelProviderAliases(info) {
		if alias == provider {
			return true
		}
	}
	return false
}

func modelProviderName(info api.ModelInfo, id string, locale string) string {
	if id == localModelProvider {
		return localModelProviderName(locale)
	}
	if providerID := providerIDFromSource(info.Source); providerID != "" {
		if provider, ok := getThirdPartyProvider(providerID); ok && strings.TrimSpace(provider.Name) != "" {
			return strings.TrimSpace(provider.Name)
		}
	}
	if isCloudModelInfo(info) {
		if provider := strings.TrimSpace(info.Provider); normalizeModelProvider(provider) == id {
			return provider
		}
		if id == config.DefaultCloudProviderName {
			return config.DefaultCloudProviderName
		}
	}
	if ownedBy := strings.TrimSpace(info.OwnedBy); normalizeModelProvider(ownedBy) == id {
		return ownedBy
	}
	if provider := strings.TrimSpace(info.Provider); normalizeModelProvider(provider) == id {
		return provider
	}
	return id
}

func localModelProviderName(locale string) string {
	switch locale {
	case "zh", "zh-cn", "zh-hans", "zh-hans-cn":
		return "本地"
	default:
		return localModelProvider
	}
}

func modelProviderSource(info api.ModelInfo) string {
	if isLocalModelInfo(info) {
		return localModelProvider
	}
	if providerIDFromSource(info.Source) != "" {
		return "provider"
	}
	if isCloudModelInfo(info) {
		return "cloud"
	}
	if source := normalizeModelProvider(info.Source); source != "" {
		return source
	}
	return ""
}

func isCloudModelInfo(model api.ModelInfo) bool {
	source := strings.TrimSpace(strings.ToLower(model.Source))
	format := strings.TrimSpace(strings.ToLower(model.Format))
	return source == "cloud" || format == "cloud"
}

func (s *Server) listModelProviders(ctx context.Context, refresh bool, locale string) ([]api.ProviderInfo, error) {
	localModels, err := s.listLocalModelInfos()
	if err != nil {
		return nil, err
	}

	byID := make(map[string]*api.ProviderInfo)
	addModelProviderInfos(byID, localModels, locale)

	cloudModels, err := s.listCloudModels(ctx, refresh)
	if err == nil {
		addModelProviderInfos(byID, cloudModels, locale)
	}

	addSelectedThirdPartyProviderInfos(byID)

	out := make([]api.ProviderInfo, 0, len(byID))
	for _, provider := range byID {
		out = append(out, *provider)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == localModelProvider && out[j].ID != localModelProvider {
			return true
		}
		if out[j].ID == localModelProvider && out[i].ID != localModelProvider {
			return false
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func addModelProviderInfos(byID map[string]*api.ProviderInfo, models []api.ModelInfo, locale string) {
	for _, model := range models {
		id := modelProviderID(model)
		if id == "" {
			continue
		}
		provider, ok := byID[id]
		if !ok {
			provider = &api.ProviderInfo{
				ID:     id,
				Name:   modelProviderName(model, id, locale),
				Source: modelProviderSource(model),
			}
			byID[id] = provider
		}
		provider.ModelCount++
	}
}

func addSelectedThirdPartyProviderInfos(byID map[string]*api.ProviderInfo) {
	for _, provider := range config.GetProviders() {
		if !provider.Enabled {
			continue
		}
		id := normalizeModelProvider(provider.ID)
		if id == "" {
			continue
		}
		count := 0
		for _, selection := range config.GetProviderModelSelections(provider.ID) {
			if providerModelOriginalID(selection) != "" {
				count++
			}
		}
		if count == 0 {
			continue
		}
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			name = id
		}
		byID[id] = &api.ProviderInfo{
			ID:         id,
			Name:       name,
			Source:     "provider",
			ModelCount: count,
		}
	}
}
