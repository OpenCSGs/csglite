package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/csghub"
	"github.com/opencsgs/csghub-lite/internal/inference"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

type cloudAuthUser struct {
	Username string `json:"username"`
	Nickname string `json:"nickname,omitempty"`
	Email    string `json:"email,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	UUID     string `json:"uuid,omitempty"`
}

type cloudAuthStatus struct {
	AuthMode       string         `json:"auth_mode"`
	HasToken       bool           `json:"has_token"`
	Authenticated  bool           `json:"authenticated"`
	LoginURL       string         `json:"login_url"`
	AccessTokenURL string         `json:"access_token_url"`
	HasAPIKey      bool           `json:"has_api_key"`
	APIKeySource   string         `json:"api_key_source,omitempty"`
	APIKeyPrefix   string         `json:"api_key_prefix,omitempty"`
	APIKeyError    string         `json:"api_key_error,omitempty"`
	User           *cloudAuthUser `json:"user,omitempty"`
}

type cloudAuthTokenRequest struct {
	Token string `json:"token"`
}

type cloudAPIKeyRequest struct {
	APIKey string `json:"api_key"`
}

func (s *Server) handleCloudAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) handleCloudAuthTokenSave(w http.ResponseWriter, r *http.Request) {
	var req cloudAuthTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, "token cannot be empty")
		return
	}

	s.cfg.Token = token
	if err := config.Save(s.cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "saving token: "+err.Error())
		return
	}
	if s.cloud != nil {
		s.cloud.InvalidateChatModels()
	}

	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) handleCloudAuthTokenDelete(w http.ResponseWriter, r *http.Request) {
	s.cfg.Token = ""
	if err := config.Save(s.cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "clearing token: "+err.Error())
		return
	}
	if s.cloud != nil {
		s.cloud.InvalidateChatModels()
	}

	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) handleCloudAPIKeySave(w http.ResponseWriter, r *http.Request) {
	var req cloudAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		writeError(w, http.StatusBadRequest, "api key cannot be empty")
		return
	}

	s.cfg.OpenCSGAPIKey = apiKey
	if err := config.Save(s.cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "saving API key: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) handleCloudAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	s.cfg.OpenCSGAPIKey = ""
	if err := config.Save(s.cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "clearing API key: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, s.cloudAuthStatus(r.Context()))
}

func (s *Server) cloudAuthStatus(ctx context.Context) cloudAuthStatus {
	displayURL := strings.TrimRight(s.cfg.DisplayURL(), "/")
	loginURL := displayURL + "/login"
	if s.cfg.ServerURL == config.DefaultServerURL || s.cfg.ServerURL == "" {
		loginURL = cloud.DefaultLoginURL
	}

	status := cloudAuthStatus{
		AuthMode:       "token",
		LoginURL:       loginURL,
		AccessTokenURL: displayURL + "/settings/access-token",
	}
	if apiKey := strings.TrimSpace(s.cfg.OpenCSGAPIKey); apiKey != "" {
		status.HasAPIKey = true
		status.APIKeySource = "manual"
		status.APIKeyPrefix = apiKeyDisplayPrefix(apiKey)
	}
	token := strings.TrimSpace(s.cfg.Token)
	status.HasToken = token != ""
	if !status.HasToken {
		return status
	}

	client := csghub.NewClient(s.cfg.ServerURL, token)
	user, err := client.GetCurrentUser(ctx)
	if err != nil || user == nil {
		return status
	}

	status.Authenticated = true
	status.User = &cloudAuthUser{
		Username: strings.TrimSpace(user.Username),
		Nickname: strings.TrimSpace(user.Nickname),
		Email:    strings.TrimSpace(user.Email),
		Avatar:   strings.TrimSpace(user.Avatar),
		UUID:     strings.TrimSpace(user.UUID),
	}
	if !status.HasAPIKey {
		apiKey, err := s.builtinCloudAPIKey(ctx, client, user)
		if err != nil {
			status.APIKeyError = err.Error()
		} else {
			status.HasAPIKey = true
			status.APIKeySource = "builtin"
			status.APIKeyPrefix = apiKeyDisplayPrefix(apiKey)
		}
	}
	return status
}

func (s *Server) getChatEngine(ctx context.Context, modelID, source string, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) (inference.Engine, error) {
	source = strings.TrimSpace(source)
	normalizedSource := strings.ToLower(source)
	if providerIDFromSource(source) != "" {
		return newThirdPartyProviderEngine(source, modelID)
	}
	if normalizedSource == "cloud" {
		return s.newCloudEngine(ctx, modelID)
	}

	eng, err := s.getOrLoadEngineWithOpts(modelID, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
	if err == nil {
		return eng, nil
	}
	if normalizedSource == "local" {
		return nil, err
	}

	if !s.hasCloudCredential() {
		if providerSource := s.thirdPartyProviderSourceForModel(ctx, modelID); providerSource != "" {
			return newThirdPartyProviderEngine(providerSource, modelID)
		}
	}

	models, cloudErr := s.listCloudModels(ctx, false)
	if cloudErr != nil {
		return nil, err
	}
	if modelInfoListContains(models, modelID) {
		return s.newCloudEngine(ctx, modelID)
	}

	if s.cloud == nil {
		return nil, err
	}
	models, cloudErr = s.cloud.RefreshChatModels(ctx)
	if cloudErr != nil {
		return nil, err
	}
	if modelInfoListContains(models, modelID) {
		return s.newCloudEngine(ctx, modelID)
	}

	if providerSource := s.thirdPartyProviderSourceForModel(ctx, modelID); providerSource != "" {
		return newThirdPartyProviderEngine(providerSource, modelID)
	}

	return nil, err
}

func (s *Server) thirdPartyProviderSourceForModel(ctx context.Context, modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	for _, item := range s.listSelectedThirdPartyProviderModels(ctx) {
		if strings.TrimSpace(item.Model) == modelID {
			return strings.TrimSpace(item.Source)
		}
	}
	return ""
}

func (s *Server) newCloudEngine(ctx context.Context, modelID string) (inference.Engine, error) {
	apiKey, err := s.cloudAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	baseURL := resolveCloudURL(s.cfg)
	if s.cloud != nil && strings.TrimSpace(s.cloud.BaseURL()) != "" {
		baseURL = s.cloud.BaseURL()
	}
	return inference.NewOpenAIEngine(baseURL, modelID, apiKey), nil
}

func (s *Server) hasCloudCredential() bool {
	if s == nil || s.cfg == nil {
		return false
	}
	return strings.TrimSpace(s.cfg.OpenCSGAPIKey) != "" || strings.TrimSpace(s.cfg.Token) != ""
}

func (s *Server) cloudAPIKey(ctx context.Context) (string, error) {
	if s == nil || s.cfg == nil {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "Cloud login required. Please sign in to OpenCSG or save an API Key.")
	}
	if apiKey := strings.TrimSpace(s.cfg.OpenCSGAPIKey); apiKey != "" {
		return apiKey, nil
	}
	token := strings.TrimSpace(s.cfg.Token)
	if token == "" {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "Cloud login required. Please sign in to OpenCSG or save an API Key.")
	}
	client := csghub.NewClient(s.cfg.ServerURL, token)
	user, err := client.GetCurrentUser(ctx)
	if err != nil || user == nil {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "OpenCSG login expired or token is invalid. Please log in again.")
	}
	apiKey, err := s.builtinCloudAPIKey(ctx, client, user)
	if err != nil {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "Failed to load OpenCSG built-in API Key. Please sign in again or save an API Key.")
	}
	return apiKey, nil
}

func (s *Server) builtinCloudAPIKey(ctx context.Context, client *csghub.Client, user *csghub.User) (string, error) {
	namespace := strings.TrimSpace(user.UUID)
	if namespace == "" {
		namespace = strings.TrimSpace(user.Username)
	}
	if namespace == "" {
		return "", inference.NewHTTPStatusError(http.StatusUnauthorized, "OpenCSG user namespace is unavailable.")
	}
	apiKey, err := client.GetBuiltinAPIKey(ctx, namespace)
	if err != nil {
		return "", err
	}
	return apiKey, nil
}

func apiKeyDisplayPrefix(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	if len(apiKey) <= 10 {
		return apiKey
	}
	return apiKey[:6] + "..." + apiKey[len(apiKey)-4:]
}

func modelInfoListContains(models []api.ModelInfo, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}
	for _, item := range models {
		if strings.TrimSpace(item.Model) == modelID {
			return true
		}
	}
	return false
}
