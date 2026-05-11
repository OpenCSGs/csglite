package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

// GET /api/providers -- list all third-party providers
func (s *Server) handleProvidersList(w http.ResponseWriter, r *http.Request) {
	providers := config.GetProviders()
	resp := api.ThirdPartyProvidersResponse{
		Providers: make([]api.ThirdPartyProvider, len(providers)),
	}
	for i, p := range providers {
		resp.Providers[i] = api.ThirdPartyProvider{
			ID:       p.ID,
			Name:     p.Name,
			BaseURL:  normalizeThirdPartyProviderBaseURL(p),
			Provider: p.Provider,
			Enabled:  p.Enabled,
			// APIKey is intentionally not returned for security
		}
	}
	writeJSON(w, http.StatusOK, resp)
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
		Enabled:  req.Enabled,
	}
	if provider.APIKey == "" && provider.ID != "" {
		if existing, ok := getThirdPartyProvider(provider.ID); ok {
			provider.APIKey = existing.APIKey
		}
	}

	modelCount, err := validateThirdPartyProvider(r.Context(), provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "provider configuration is invalid: "+err.Error())
		return
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
	if apiKey == "" {
		writeError(w, http.StatusBadRequest, "api_key is required")
		return
	}

	providers := config.GetProviders()
	newProvider := config.ThirdPartyProvider{
		ID:       config.GenerateProviderID(),
		Name:     name,
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Provider: provider,
		Enabled:  req.Enabled,
	}
	newProvider.BaseURL = normalizeThirdPartyProviderBaseURL(newProvider)
	if _, err := validateThirdPartyProvider(r.Context(), newProvider); err != nil {
		writeError(w, http.StatusBadRequest, "provider configuration is invalid: "+err.Error())
		return
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
			candidate.BaseURL = normalizeThirdPartyProviderBaseURL(candidate)
			if _, err := validateThirdPartyProvider(r.Context(), candidate); err != nil {
				writeError(w, http.StatusBadRequest, "provider configuration is invalid: "+err.Error())
				return
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

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
