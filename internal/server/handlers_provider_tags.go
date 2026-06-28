package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

type modelManageProvider struct {
	ID         string
	Name       string
	Source     string
	ThirdParty config.ThirdPartyProvider
}

func (p modelManageProvider) IsCloud() bool {
	return p.Source == "cloud"
}

func (s *Server) handleProviderTagsManageList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")

	provider, ok := s.providerFromTagsManageRequest(w, r)
	if !ok {
		return
	}
	models, err := s.listProviderManageCatalogModels(r, provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to fetch provider models: "+err.Error())
		return
	}
	models, ok = filterModelsByPipelineCategory(models, r.URL.Query().Get("category"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid category")
		return
	}
	writeJSON(w, http.StatusOK, api.TagsResponse{Models: models})
}

func (s *Server) handleProviderTagsManageAdd(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.providerFromTagsManageRequest(w, r)
	if !ok {
		return
	}

	var req api.ProviderTagModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	model, ok, err := s.providerModelFromCatalog(r, provider, req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to fetch provider models: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "provider model not found")
		return
	}

	if existing, ok := providerModelSelectionByOriginal(provider.ID, model.Model); ok {
		model = applyProviderModelMetadata(model, existing)
		writeJSON(w, http.StatusOK, model)
		return
	}
	catalogDisplayName := providerModelCatalogDisplayName(provider, model)
	model = applyProviderModelMetadata(model, config.ProviderModelSelection{
		Model:              model.Model,
		OriginalModel:      model.Model,
		CatalogDisplayName: catalogDisplayName,
		DisplayName:        req.DisplayName,
		Description:        req.Description,
		PipelineTag:        model.PipelineTag,
		InputModalities:    model.InputModalities,
		OutputModalities:   model.OutputModalities,
	})
	alreadySelected := modelIDInList(config.GetProviderModelAllowlist(provider.ID), model.Model)
	if err := config.AddProviderModelSelection(provider.ID, config.ProviderModelSelection{
		Model:              model.Model,
		OriginalModel:      model.Model,
		CatalogDisplayName: catalogDisplayName,
		DisplayName:        strings.TrimSpace(req.DisplayName),
		Description:        strings.TrimSpace(req.Description),
		PipelineTag:        model.PipelineTag,
		InputModalities:    model.InputModalities,
		OutputModalities:   model.OutputModalities,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save provider model: "+err.Error())
		return
	}

	status := http.StatusCreated
	if alreadySelected {
		status = http.StatusOK
	}
	writeJSON(w, status, model)
}

func (s *Server) handleProviderTagsManageReplace(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.providerFromTagsManageRequest(w, r)
	if !ok {
		return
	}

	var req api.ProviderTagModelsReplaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	catalog, err := s.listProviderManageCatalogModels(r, provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to fetch provider models: "+err.Error())
		return
	}
	if _, ok := pipelineTagsForCategory(r.URL.Query().Get("category")); !ok {
		writeError(w, http.StatusBadRequest, "invalid category")
		return
	}
	selected := make([]config.ProviderModelSelection, 0, len(req.Models))
	selectedModels := make([]api.ModelInfo, 0, len(req.Models))
	for _, requested := range normalizeProviderTagModelSelections(req.Models) {
		model, ok := findModelInfoByID(catalog, requested.Model)
		if !ok {
			writeError(w, http.StatusNotFound, "provider model not found: "+requested.Model)
			return
		}
		catalogDisplayName := providerModelCatalogDisplayName(provider, model)
		model = applyProviderModelMetadata(model, config.ProviderModelSelection{
			Model:              model.Model,
			OriginalModel:      model.Model,
			CatalogDisplayName: catalogDisplayName,
			DisplayName:        requested.DisplayName,
			Description:        requested.Description,
			PipelineTag:        model.PipelineTag,
			InputModalities:    model.InputModalities,
			OutputModalities:   model.OutputModalities,
		})
		selected = append(selected, config.ProviderModelSelection{
			Model:              model.Model,
			OriginalModel:      model.Model,
			CatalogDisplayName: catalogDisplayName,
			DisplayName:        strings.TrimSpace(requested.DisplayName),
			Description:        strings.TrimSpace(requested.Description),
			PipelineTag:        model.PipelineTag,
			InputModalities:    model.InputModalities,
			OutputModalities:   model.OutputModalities,
		})
		selectedModels = append(selectedModels, model)
	}

	if err := config.ReplaceProviderModelSelections(provider.ID, selected); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save provider models: "+err.Error())
		return
	}
	selectedModels, ok = filterModelsByPipelineCategory(selectedModels, r.URL.Query().Get("category"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid category")
		return
	}
	writeJSON(w, http.StatusOK, api.TagsResponse{Models: selectedModels})
}

func (s *Server) handleProviderTagsManageUpdate(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.providerFromTagsManageRequest(w, r)
	if !ok {
		return
	}

	var req api.ProviderTagModelUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	if modelID == "" && req.Model != nil {
		modelID = strings.TrimSpace(*req.Model)
	}
	if modelID == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	selection, found, err := config.UpdateProviderModelSelection(provider.ID, modelID, config.ProviderModelSelectionUpdate{
		Model:       req.Model,
		DisplayName: req.DisplayName,
		Description: req.Description,
	})
	if err != nil {
		if errors.Is(err, config.ErrProviderModelSelectionDuplicate) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save provider model: "+err.Error())
		return
	}
	if !found {
		if !provider.IsCloud() {
			writeError(w, http.StatusNotFound, "provider model not selected")
			return
		}
		created, ok, createErr := s.createCloudProviderModelSelectionForUpdate(r, provider, modelID, req)
		if createErr != nil {
			writeError(w, http.StatusBadRequest, "failed to fetch provider models: "+createErr.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "provider model not found")
			return
		}
		selection = created
	}

	model := api.ModelInfo{
		Name:        selection.Model,
		Model:       selection.Model,
		Label:       selection.Model,
		DisplayName: selection.Model,
		Format:      "api",
		Source:      providerManagedSource(provider),
		Provider:    providerManagedModelProviderID(provider),
	}
	if catalogModel, ok, err := s.providerModelFromCatalog(r, provider, providerModelOriginalID(selection)); err == nil && ok {
		model = catalogModel
	}
	model = applyProviderModelMetadata(model, selection)
	writeJSON(w, http.StatusOK, model)
}

func (s *Server) handleProviderTagsManageDelete(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.providerFromTagsManageRequest(w, r)
	if !ok {
		return
	}

	modelID := strings.TrimSpace(r.URL.Query().Get("model"))
	if modelID == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	removed, err := config.RemoveProviderModelAllowlist(provider.ID, modelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save provider models: "+err.Error())
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "provider model not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) providerFromTagsManageRequest(w http.ResponseWriter, r *http.Request) (modelManageProvider, bool) {
	providerName := normalizeModelProvider(r.URL.Query().Get("provider"))
	if providerName == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return modelManageProvider{}, false
	}
	if s.isCloudModelProviderAlias(providerName) {
		return modelManageProvider{
			ID:     config.DefaultCloudProviderName,
			Name:   s.cloudModelProviderDisplayName(),
			Source: "cloud",
		}, true
	}
	provider, ok := getThirdPartyProviderByAlias(providerName)
	if !ok {
		writeError(w, http.StatusNotFound, "provider not found")
		return modelManageProvider{}, false
	}
	if !provider.Enabled {
		writeError(w, http.StatusForbidden, "provider is disabled")
		return modelManageProvider{}, false
	}
	return modelManageProvider{
		ID:         provider.ID,
		Name:       provider.Name,
		Source:     "provider",
		ThirdParty: provider,
	}, true
}

func (s *Server) providerModelFromCatalog(r *http.Request, provider modelManageProvider, modelID string) (api.ModelInfo, bool, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return api.ModelInfo{}, false, nil
	}
	models, err := s.listProviderManageCatalogModels(r, provider)
	if err != nil {
		return api.ModelInfo{}, false, err
	}
	model, ok := findModelInfoByID(models, modelID)
	return model, ok, nil
}

func (s *Server) listProviderManageCatalogModels(r *http.Request, provider modelManageProvider) ([]api.ModelInfo, error) {
	if provider.IsCloud() {
		return s.listCloudModelCatalog(r.Context(), requestWantsModelRefresh(r))
	}
	return listOpenAICompatibleProviderModels(r.Context(), provider.ThirdParty)
}

func (s *Server) createCloudProviderModelSelectionForUpdate(r *http.Request, provider modelManageProvider, modelID string, req api.ProviderTagModelUpdateRequest) (config.ProviderModelSelection, bool, error) {
	model, ok, err := s.providerModelFromCatalog(r, provider, modelID)
	if err != nil || !ok {
		return config.ProviderModelSelection{}, ok, err
	}
	selection := config.ProviderModelSelection{
		Model:              model.Model,
		OriginalModel:      model.Model,
		CatalogDisplayName: providerModelCatalogDisplayName(provider, model),
		PipelineTag:        model.PipelineTag,
		InputModalities:    model.InputModalities,
		OutputModalities:   model.OutputModalities,
	}
	if req.Model != nil {
		nextModel := strings.TrimSpace(*req.Model)
		if nextModel != "" {
			selection.Model = nextModel
		}
	}
	if req.DisplayName != nil {
		selection.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.Description != nil {
		selection.Description = strings.TrimSpace(*req.Description)
	}
	if err := config.AddProviderModelSelection(provider.ID, selection); err != nil {
		return config.ProviderModelSelection{}, true, err
	}
	created, found := providerModelSelectionByOriginal(provider.ID, model.Model)
	if !found {
		return config.ProviderModelSelection{}, true, fmt.Errorf("saved cloud model selection was not found")
	}
	return created, true, nil
}

func findModelInfoByID(models []api.ModelInfo, modelID string) (api.ModelInfo, bool) {
	modelID = strings.TrimSpace(modelID)
	for _, model := range models {
		if strings.TrimSpace(model.Model) == modelID {
			return model, true
		}
	}
	return api.ModelInfo{}, false
}

func normalizeModelIDs(models []string) []string {
	out := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
}

func normalizeProviderTagModelSelections(models []api.ProviderTagModelSelection) []api.ProviderTagModelSelection {
	out := make([]api.ProviderTagModelSelection, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		model.Model = strings.TrimSpace(model.Model)
		model.DisplayName = strings.TrimSpace(model.DisplayName)
		model.Description = strings.TrimSpace(model.Description)
		if model.Model == "" {
			continue
		}
		if _, ok := seen[model.Model]; ok {
			continue
		}
		seen[model.Model] = struct{}{}
		out = append(out, model)
	}
	return out
}

func applyProviderModelMetadata(model api.ModelInfo, selection config.ProviderModelSelection) api.ModelInfo {
	publicModel := strings.TrimSpace(selection.Model)
	originalModel := providerModelOriginalID(selection)
	if publicModel != "" {
		model.Model = publicModel
		if publicModel != originalModel {
			model.Origin = originalModel
			model.Name = publicModel
			model.DisplayName = publicModel
			model.Label = publicModel
		}
	}
	displayName := strings.TrimSpace(selection.DisplayName)
	if displayName != "" {
		model.Name = displayName
		model.DisplayName = displayName
		model.Label = displayName
	}
	if description := strings.TrimSpace(selection.Description); description != "" {
		model.Description = description
	}
	return model
}

func providerModelOriginalID(selection config.ProviderModelSelection) string {
	if original := strings.TrimSpace(selection.OriginalModel); original != "" {
		return original
	}
	return strings.TrimSpace(selection.Model)
}

func providerModelSelectionByOriginal(providerID, modelID string) (config.ProviderModelSelection, bool) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return config.ProviderModelSelection{}, false
	}
	for _, selection := range config.GetProviderModelSelections(providerID) {
		if providerModelOriginalID(selection) == modelID {
			return selection, true
		}
	}
	return config.ProviderModelSelection{}, false
}

func modelProviderIDFromProvider(provider config.ThirdPartyProvider) string {
	return normalizeModelProvider(provider.ID)
}

func modelIDInList(models []string, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	for _, model := range models {
		if strings.TrimSpace(model) == modelID {
			return true
		}
	}
	return false
}
