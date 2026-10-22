package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

// GET /api/providers -- list all third-party providers
func (s *Server) handleProvidersList(w http.ResponseWriter, r *http.Request) {
	source := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("source")))
	switch source {
	case "", "model", "models":
		s.handleModelProvidersList(w, r)
	case "third_party", "third-party", "config":
		s.handleThirdPartyProvidersList(w, r)
	default:
		writeError(w, http.StatusBadRequest, "invalid providers source")
	}
}

func (s *Server) handleThirdPartyProvidersList(w http.ResponseWriter, r *http.Request) {
	providers := config.GetProviders()
	resp := api.ThirdPartyProvidersResponse{
		Providers: make([]api.ThirdPartyProvider, len(providers)),
	}
	for i, p := range providers {
		resp.Providers[i] = api.ThirdPartyProvider{
			ID:       p.ID,
			Name:     p.Name,
			BaseURL:  normalizeThirdPartyProviderBaseURL(p),
			APIKey:   p.APIKey,
			Provider: p.Provider,
			Enabled:  p.Enabled,
			Headers:  providerHeadersAPI(p.Headers),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleModelProvidersList(w http.ResponseWriter, r *http.Request) {
	providers, err := s.listModelProviders(r.Context(), requestWantsModelRefresh(r), requestLocale(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.ModelProvidersResponse{Providers: providers})
}

func requestLocale(r *http.Request) string {
	locale := strings.TrimSpace(r.URL.Query().Get("locale"))
	if locale == "" {
		locale = strings.TrimSpace(r.Header.Get("Accept-Language"))
	}
	locale = strings.ToLower(locale)
	if i := strings.IndexAny(locale, ",;"); i >= 0 {
		locale = locale[:i]
	}
	return strings.ReplaceAll(locale, "_", "-")
}

// POST /api/providers/validate -- validate provider settings without saving
func (s *Server) handleProviderValidate(w http.ResponseWriter, r *http.Request) {
	var req api.ThirdPartyProviderValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	provider := config.ThirdPartyProvider{
		ID:       strings.TrimSpace(req.ID),
		Name:     strings.TrimSpace(req.Name),
		BaseURL:  strings.TrimSpace(req.BaseURL),
		APIKey:   strings.TrimSpace(req.APIKey),
		Provider: strings.TrimSpace(req.Provider),
		Enabled:  boolDefault(req.Enabled, true),
		Headers:  providerHeadersFromAPI(req.Headers),
	}
	if err := validateProviderHeaders(provider.Headers); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if provider.APIKey == "" && provider.ID != "" {
		if existing, ok := getThirdPartyProvider(provider.ID); ok {
			provider.APIKey = existing.APIKey
		}
	}

	if !provider.Enabled && !req.Probe {
		writeJSON(w, http.StatusOK, api.ThirdPartyProviderValidateResponse{
			Valid:      true,
			ModelCount: 0,
		})
		return
	}

	modelCount, err := validateThirdPartyProvider(r.Context(), provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "provider configuration is invalid: "+err.Error())
		return
	}
	if req.Probe {
		if err := probeThirdPartyProvider(r.Context(), provider); err != nil {
			writeError(w, http.StatusBadRequest, "provider connection test failed: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, api.ThirdPartyProviderValidateResponse{
		Valid:      true,
		ModelCount: modelCount,
	})
}

// POST /api/providers -- create a new provider
func (s *Server) handleProviderCreate(w http.ResponseWriter, r *http.Request) {
	var req api.ThirdPartyProviderCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(req.Name)
	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	provider := strings.TrimSpace(req.Provider)

	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "base_url is required")
		return
	}

	providers := config.GetProviders()
	if providerNameExists(providers, name, "") {
		writeError(w, http.StatusBadRequest, "provider name already exists")
		return
	}
	newProvider := config.ThirdPartyProvider{
		ID:       config.GenerateProviderID(),
		Name:     name,
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Provider: provider,
		Enabled:  boolDefault(req.Enabled, true),
		Headers:  providerHeadersFromAPI(req.Headers),
	}
	if err := validateProviderHeaders(newProvider.Headers); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newProvider.BaseURL = normalizeThirdPartyProviderBaseURL(newProvider)
	if newProvider.Enabled {
		if _, err := validateThirdPartyProvider(r.Context(), newProvider); err != nil {
			writeError(w, http.StatusBadRequest, "provider configuration is invalid: "+err.Error())
			return
		}
	}
	providers = append(providers, newProvider)

	if err := config.SaveProviders(providers); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save provider: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, api.ThirdPartyProvider{
		ID:       newProvider.ID,
		Name:     newProvider.Name,
		BaseURL:  newProvider.BaseURL,
		Provider: newProvider.Provider,
		Enabled:  newProvider.Enabled,
		Headers:  providerHeadersAPI(newProvider.Headers),
	})
}

// PUT /api/providers/{id} -- update an existing provider
func (s *Server) handleProviderUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "provider id is required")
		return
	}

	var req api.ThirdPartyProviderUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	providers := config.GetProviders()
	found := false
	for i, p := range providers {
		if p.ID == id {
			found = true
			candidate := p
			if req.Name != "" {
				candidate.Name = strings.TrimSpace(req.Name)
			}
			if req.BaseURL != "" {
				candidate.BaseURL = strings.TrimSpace(req.BaseURL)
			}
			if req.APIKey != "" {
				candidate.APIKey = strings.TrimSpace(req.APIKey)
			}
			if req.Provider != "" {
				candidate.Provider = strings.TrimSpace(req.Provider)
			}
			if req.Enabled != nil {
				candidate.Enabled = *req.Enabled
			}
			if req.Headers != nil {
				candidate.Headers = providerHeadersFromAPI(req.Headers)
			}
			if err := validateProviderHeaders(candidate.Headers); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if providerNameExists(providers, candidate.Name, id) {
				writeError(w, http.StatusBadRequest, "provider name already exists")
				return
			}
			candidate.BaseURL = normalizeThirdPartyProviderBaseURL(candidate)
			if candidate.Enabled {
				if _, err := validateThirdPartyProvider(r.Context(), candidate); err != nil {
					writeError(w, http.StatusBadRequest, "provider configuration is invalid: "+err.Error())
					return
				}
			}
			providers[i] = candidate
			break
		}
	}

	if !found {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}

	if err := config.SaveProviders(providers); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save provider: "+err.Error())
		return
	}

	// Return updated provider without API key
	for _, p := range providers {
		if p.ID == id {
			writeJSON(w, http.StatusOK, api.ThirdPartyProvider{
				ID:       p.ID,
				Name:     p.Name,
				BaseURL:  p.BaseURL,
				Provider: p.Provider,
				Enabled:  p.Enabled,
				Headers:  providerHeadersAPI(p.Headers),
			})
			return
		}
	}
}

// DELETE /api/providers/{id} -- delete a provider
func (s *Server) handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "provider id is required")
		return
	}
	for _, pool := range config.GetProviderPools() {
		for _, member := range pool.Members {
			if providerIDFromSource(member.Source) == id {
				writeError(w, http.StatusConflict, "provider is referenced by provider pool "+pool.Name)
				return
			}
		}
	}

	providers := config.GetProviders()
	found := false
	newProviders := make([]config.ThirdPartyProvider, 0, len(providers))
	for _, p := range providers {
		if p.ID == id {
			found = true
			continue
		}
		newProviders = append(newProviders, p)
	}

	if !found {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}

	if err := config.SaveProviders(newProviders); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save providers: "+err.Error())
		return
	}
	if err := config.DeleteProviderModelAllowlist(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete provider models: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func providerNameExists(providers []config.ThirdPartyProvider, name, excludeID string) bool {
	name = normalizeModelProvider(name)
	if name == "" {
		return false
	}
	for _, provider := range providers {
		if strings.TrimSpace(provider.ID) == strings.TrimSpace(excludeID) {
			continue
		}
		if normalizeModelProvider(provider.Name) == name {
			return true
		}
	}
	return false
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func providerHeadersAPI(headers []config.ProviderHeader) []api.ProviderHeader {
	out := make([]api.ProviderHeader, len(headers))
	for i, header := range headers {
		out[i] = api.ProviderHeader{Name: header.Name, Value: header.Value}
	}
	return out
}

func providerHeadersFromAPI(headers []api.ProviderHeader) []config.ProviderHeader {
	out := make([]config.ProviderHeader, 0, len(headers))
	for _, header := range headers {
		name := strings.TrimSpace(header.Name)
		value := strings.TrimSpace(header.Value)
		if name == "" || value == "" {
			continue
		}
		out = append(out, config.ProviderHeader{Name: name, Value: value})
	}
	return out
}

func validateProviderHeaders(headers []config.ProviderHeader) error {
	hasModelHeader := false
	for _, header := range headers {
		if strings.ContainsAny(header.Name, " \t\r\n:") {
			return fmt.Errorf("invalid provider header name %q", header.Name)
		}
		if strings.ContainsAny(header.Value, "\r\n") {
			return fmt.Errorf("invalid provider header value for %q", header.Name)
		}
		if strings.EqualFold(header.Name, providerModelHeader) {
			if hasModelHeader {
				return fmt.Errorf("provider header %q may only be configured once", providerModelHeader)
			}
			hasModelHeader = true
		}
	}
	return nil
}
