package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func (s *Server) handleAPIKeysList(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeError(w, http.StatusInternalServerError, "API key store is unavailable")
		return
	}
	state, err := s.apiKeys.State()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load API keys")
		return
	}
	writeJSON(w, http.StatusOK, apiKeysResponse(state))
}

func (s *Server) handleAPIKeysSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeError(w, http.StatusInternalServerError, "API key store is unavailable")
		return
	}
	var req api.APIKeySettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	state, err := s.apiKeys.SetAuthEnabled(req.AuthEnabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save API key settings")
		return
	}
	writeJSON(w, http.StatusOK, apiKeysResponse(state))
}

func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeError(w, http.StatusInternalServerError, "API key store is unavailable")
		return
	}
	var req api.APIKeyCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	record, plain, err := s.apiKeys.Create(strings.TrimSpace(req.Name))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create API key")
		return
	}
	writeJSON(w, http.StatusCreated, api.APIKeyCreateResponse{
		Key:    apiKeyInfo(record),
		APIKey: plain,
	})
}

func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeError(w, http.StatusInternalServerError, "API key store is unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "API key id is required")
		return
	}
	deleted, err := s.apiKeys.Delete(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete API key")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "API key not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAPIUsage(w http.ResponseWriter, r *http.Request) {
	if s.apiUsage == nil {
		writeError(w, http.StatusInternalServerError, "API usage store is unavailable")
		return
	}
	state, err := s.apiUsage.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load API usage")
		return
	}
	resp := api.APIUsageResponse{
		Rows: make([]api.APIUsageRow, 0, len(state.Records)),
	}
	for _, record := range state.Records {
		resp.Totals.Requests += record.Requests
		resp.Totals.InputTokens += record.InputTokens
		resp.Totals.OutputTokens += record.OutputTokens
		resp.Totals.TotalTokens += record.TotalTokens
		resp.Rows = append(resp.Rows, api.APIUsageRow{
			APIKeyID:     record.APIKeyID,
			APIKeyName:   record.APIKeyName,
			Model:        record.Model,
			Requests:     record.Requests,
			InputTokens:  record.InputTokens,
			OutputTokens: record.OutputTokens,
			TotalTokens:  record.TotalTokens,
			LastUsedAt:   record.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func apiKeysResponse(state config.APIKeyState) api.APIKeysResponse {
	resp := api.APIKeysResponse{
		AuthEnabled: state.AuthEnabled,
		Keys:        make([]api.APIKeyInfo, 0, len(state.Keys)),
	}
	for _, record := range state.Keys {
		resp.Keys = append(resp.Keys, apiKeyInfo(record))
	}
	return resp
}

func apiKeyInfo(record config.APIKeyRecord) api.APIKeyInfo {
	var lastUsedAt *time.Time
	if !record.LastUsedAt.IsZero() {
		lastUsedAt = &record.LastUsedAt
	}
	return api.APIKeyInfo{
		ID:         record.ID,
		Name:       record.Name,
		Prefix:     record.Prefix,
		CreatedAt:  record.CreatedAt,
		LastUsedAt: lastUsedAt,
	}
}
