package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	localProviderRouteID       = "local"
	cloudProviderRouteID       = config.DefaultCloudProviderName
	legacyCloudProviderRouteID = "cloud"
)

type providerRouteSourceContextKey struct{}

func (s *Server) registerProviderInferenceRoutes(mux *http.ServeMux) {
	register := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, s.withProviderRouteSource(handler))
	}

	register("GET /providers/{providerID}/v1/models", s.handleModels)
	register("GET /providers/{providerID}/v1/responses", s.handleOpenAIResponsesUnsupported)
	register("POST /providers/{providerID}/v1/chat/completions", s.handleOpenAIChatCompletions)
	register("POST /providers/{providerID}/v1/embeddings", s.handleOpenAIEmbeddings)
	register("POST /providers/{providerID}/v1/images/generations", s.handleOpenAIImagesGenerations)
	register("POST /providers/{providerID}/v1/images/edits", s.handleOpenAIImagesEdits)
	register("POST /providers/{providerID}/v1/audio/transcriptions", s.handleOpenAIAudioTranscriptions)
	register("POST /providers/{providerID}/v1/responses", s.handleOpenAIResponses)
	register("POST /providers/{providerID}/v1/messages", s.handleAnthropicMessages)
	register("POST /providers/{providerID}/v1/messages/count_tokens", s.handleAnthropicCountTokens)

	notFound := func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "provider inference endpoint not found")
	}
	mux.HandleFunc("GET /providers/{providerID}/{rest...}", notFound)
	mux.HandleFunc("POST /providers/{providerID}/{rest...}", notFound)
}

func (s *Server) withProviderRouteSource(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source, err := providerRouteSource(r.PathValue("providerID"))
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if poolID := poolIDFromSource(source); poolID != "" {
			pool, ok := providerPoolByID(poolID)
			if !ok {
				writeError(w, http.StatusNotFound, "provider pool not found")
				return
			}
			if !pool.Enabled {
				writeError(w, http.StatusForbidden, "provider pool is disabled")
				return
			}
			if providerPoolRouteUnsupported(r.URL.Path) {
				writeError(w, http.StatusNotImplemented, "provider pools do not support this inference endpoint")
				return
			}
		}
		if providerID := providerIDFromSource(source); providerID != "" {
			provider, ok := getThirdPartyProvider(providerID)
			if !ok {
				writeError(w, http.StatusNotFound, "provider not found")
				return
			}
			if !provider.Enabled {
				writeError(w, http.StatusForbidden, "provider is disabled")
				return
			}
		}
		ctx := context.WithValue(r.Context(), providerRouteSourceContextKey{}, source)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func providerPoolRouteUnsupported(path string) bool {
	return strings.HasSuffix(path, "/v1/images/generations") ||
		strings.HasSuffix(path, "/v1/images/edits") ||
		strings.HasSuffix(path, "/v1/audio/transcriptions")
}

func providerPoolByID(id string) (config.ProviderPool, bool) {
	id = strings.TrimSpace(id)
	for _, pool := range config.GetProviderPools() {
		if pool.ID == id {
			return pool, true
		}
	}
	return config.ProviderPool{}, false
}

func providerRouteSource(providerID string) (string, error) {
	providerID = strings.TrimSpace(providerID)
	switch strings.ToLower(providerID) {
	case localProviderRouteID:
		return "local", nil
	case cloudProviderRouteID, legacyCloudProviderRouteID:
		return "cloud", nil
	case "":
		return "", fmt.Errorf("provider ID is required")
	default:
		if _, ok := getThirdPartyProvider(providerID); ok {
			return providerSource(providerID), nil
		}
		if _, ok := providerPoolByID(providerID); ok {
			return poolSource(providerID), nil
		}
		return "", fmt.Errorf("provider or provider pool not found")
	}
}

func providerRouteSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	source, _ := ctx.Value(providerRouteSourceContextKey{}).(string)
	return strings.TrimSpace(source)
}

func withoutProviderRouteSource(ctx context.Context) context.Context {
	return context.WithValue(ctx, providerRouteSourceContextKey{}, "")
}

func effectiveRequestSource(ctx context.Context, requested string) (string, error) {
	routeSource := providerRouteSourceFromContext(ctx)
	requested = strings.TrimSpace(requested)
	if routeSource == "" {
		return requested, nil
	}
	if requested == "" || strings.EqualFold(requested, routeSource) {
		return routeSource, nil
	}
	return "", fmt.Errorf("request source %q conflicts with provider route source %q", requested, routeSource)
}

func providerRouteIDForSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	switch strings.ToLower(source) {
	case "", "local":
		return localProviderRouteID, nil
	case "cloud":
		return cloudProviderRouteID, nil
	default:
		if providerID := providerIDFromSource(source); providerID != "" {
			if _, ok := getThirdPartyProvider(providerID); !ok {
				return "", fmt.Errorf("provider %q not found", providerID)
			}
			return providerID, nil
		}
		return "", fmt.Errorf("unsupported model source %q", source)
	}
}

func providerScopedBaseURL(baseURL, source string) (string, error) {
	providerID, err := providerRouteIDForSource(source)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/providers/" + url.PathEscape(providerID), nil
}

func filterModelsByProviderRoute(models []api.ModelInfo, source string) []api.ModelInfo {
	if source == "" {
		return models
	}
	out := make([]api.ModelInfo, 0, len(models))
	for _, item := range models {
		if strings.EqualFold(strings.TrimSpace(item.Source), source) {
			out = append(out, item)
		}
	}
	return out
}
