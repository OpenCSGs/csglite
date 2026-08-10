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
		if _, ok := getThirdPartyProvider(providerID); !ok {
			return "", fmt.Errorf("provider not found")
		}
		return providerSource(providerID), nil
	}
}

func providerRouteSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	source, _ := ctx.Value(providerRouteSourceContextKey{}).(string)
	return strings.TrimSpace(source)
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
