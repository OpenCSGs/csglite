package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const ProviderModelAllowlistFile = "provider_model_allowlist.json"

type ProviderModelAllowlist struct {
	Version   int                                 `json:"version"`
	Providers map[string][]ProviderModelSelection `json:"providers"`
}

type ProviderModelSelection struct {
	Model              string   `json:"model"`
	OriginalModel      string   `json:"original_model,omitempty"`
	CatalogDisplayName string   `json:"catalog_display_name,omitempty"`
	DisplayName        string   `json:"display_name,omitempty"`
	Description        string   `json:"description,omitempty"`
	PipelineTag        string   `json:"pipeline_tag,omitempty"`
	InputModalities    []string `json:"input_modalities,omitempty"`
	OutputModalities   []string `json:"output_modalities,omitempty"`
}

var ErrProviderModelSelectionDuplicate = errors.New("provider model id already exists")

var (
	providerModelAllowlist     ProviderModelAllowlist
	providerModelAllowlistOnce sync.Once
	providerModelAllowlistMu   sync.RWMutex
)

func ProviderModelAllowlistPath() (string, error) {
	home, err := AppHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ProviderModelAllowlistFile), nil
}

func LoadProviderModelAllowlist() (ProviderModelAllowlist, error) {
	var loadErr error
	providerModelAllowlistOnce.Do(func() {
		providerModelAllowlist = ProviderModelAllowlist{
			Version:   1,
			Providers: map[string][]ProviderModelSelection{},
		}

		cfgPath, err := ProviderModelAllowlistPath()
		if err != nil {
			loadErr = err
			return
		}

		data, err := os.ReadFile(cfgPath)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			loadErr = err
			return
		}

		var loaded ProviderModelAllowlist
		if err := json.Unmarshal(data, &loaded); err != nil {
			loadErr = err
			return
		}
		providerModelAllowlist = normalizeProviderModelAllowlist(loaded)
	})
	return copyProviderModelAllowlist(providerModelAllowlist), loadErr
}

func GetProviderModelAllowlist(providerID string) []string {
	selections := GetProviderModelSelections(providerID)
	models := make([]string, 0, len(selections))
	for _, selection := range selections {
		models = append(models, selection.Model)
	}
	return models
}

func GetProviderModelSelections(providerID string) []ProviderModelSelection {
	providerModelAllowlistMu.RLock()
	if providerModelAllowlist.Providers != nil {
		models := copyProviderModelSelections(providerModelAllowlist.Providers[strings.TrimSpace(providerID)])
		providerModelAllowlistMu.RUnlock()
		return models
	}
	providerModelAllowlistMu.RUnlock()

	state, _ := LoadProviderModelAllowlist()
	return copyProviderModelSelections(state.Providers[strings.TrimSpace(providerID)])
}

func HasProviderModelSelections(providerID string) bool {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return false
	}

	providerModelAllowlistMu.RLock()
	if providerModelAllowlist.Providers != nil {
		_, ok := providerModelAllowlist.Providers[providerID]
		providerModelAllowlistMu.RUnlock()
		return ok
	}
	providerModelAllowlistMu.RUnlock()

	state, _ := LoadProviderModelAllowlist()
	_, ok := state.Providers[providerID]
	return ok
}

func ReplaceProviderModelAllowlist(providerID string, models []string) error {
	selections := make([]ProviderModelSelection, 0, len(models))
	for _, model := range models {
		selections = append(selections, ProviderModelSelection{Model: model})
	}
	return ReplaceProviderModelSelections(providerID, selections)
}

func ReplaceProviderModelSelections(providerID string, models []ProviderModelSelection) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil
	}

	providerModelAllowlistMu.Lock()
	defer providerModelAllowlistMu.Unlock()

	if providerModelAllowlist.Providers == nil {
		if _, err := LoadProviderModelAllowlist(); err != nil {
			return err
		}
	}
	providerModelAllowlist.Version = 1
	if providerModelAllowlist.Providers == nil {
		providerModelAllowlist.Providers = map[string][]ProviderModelSelection{}
	}
	providerModelAllowlist.Providers[providerID] = normalizeProviderModelSelections(models)
	return saveProviderModelAllowlistLocked()
}

func AddProviderModelAllowlist(providerID, modelID string) error {
	return AddProviderModelSelection(providerID, ProviderModelSelection{Model: modelID})
}

func AddProviderModelSelection(providerID string, selection ProviderModelSelection) error {
	providerID = strings.TrimSpace(providerID)
	selection.Model = strings.TrimSpace(selection.Model)
	selection.OriginalModel = strings.TrimSpace(selection.OriginalModel)
	if selection.OriginalModel == "" {
		selection.OriginalModel = selection.Model
	}
	selection.CatalogDisplayName = strings.TrimSpace(selection.CatalogDisplayName)
	selection.DisplayName = strings.TrimSpace(selection.DisplayName)
	selection.Description = strings.TrimSpace(selection.Description)
	selection.PipelineTag = strings.TrimSpace(selection.PipelineTag)
	selection.InputModalities = normalizeModelIDList(selection.InputModalities)
	selection.OutputModalities = normalizeModelIDList(selection.OutputModalities)
	if providerID == "" || selection.Model == "" {
		return nil
	}
	models := GetProviderModelSelections(providerID)
	models = append(models, selection)
	return ReplaceProviderModelSelections(providerID, models)
}

func RemoveProviderModelAllowlist(providerID, modelID string) (bool, error) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return false, nil
	}

	models := GetProviderModelSelections(providerID)
	out := make([]ProviderModelSelection, 0, len(models))
	removed := false
	for _, model := range models {
		if model.Model == modelID {
			removed = true
			continue
		}
		out = append(out, model)
	}
	if !removed {
		return false, nil
	}
	return true, ReplaceProviderModelSelections(providerID, out)
}

func UpdateProviderModelSelection(providerID, modelID string, update ProviderModelSelectionUpdate) (ProviderModelSelection, bool, error) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return ProviderModelSelection{}, false, nil
	}

	models := GetProviderModelSelections(providerID)
	updated := ProviderModelSelection{}
	found := false
	for i := range models {
		if models[i].Model != modelID {
			continue
		}
		found = true
		if update.Model != nil {
			nextModel := strings.TrimSpace(*update.Model)
			if nextModel != "" && nextModel != models[i].Model {
				for j := range models {
					if i != j && models[j].Model == nextModel {
						return ProviderModelSelection{}, true, ErrProviderModelSelectionDuplicate
					}
				}
				models[i].Model = nextModel
			}
		}
		if update.DisplayName != nil {
			models[i].DisplayName = strings.TrimSpace(*update.DisplayName)
		}
		if update.Description != nil {
			models[i].Description = strings.TrimSpace(*update.Description)
		}
		updated = models[i]
		break
	}
	if !found {
		return ProviderModelSelection{}, false, nil
	}
	return updated, true, ReplaceProviderModelSelections(providerID, models)
}

type ProviderModelSelectionUpdate struct {
	Model       *string
	DisplayName *string
	Description *string
}

func DeleteProviderModelAllowlist(providerID string) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil
	}

	providerModelAllowlistMu.Lock()
	defer providerModelAllowlistMu.Unlock()

	if providerModelAllowlist.Providers == nil {
		if _, err := LoadProviderModelAllowlist(); err != nil {
			return err
		}
	}
	delete(providerModelAllowlist.Providers, providerID)
	return saveProviderModelAllowlistLocked()
}

func ResetProviderModelAllowlist() {
	providerModelAllowlistMu.Lock()
	defer providerModelAllowlistMu.Unlock()
	providerModelAllowlist = ProviderModelAllowlist{}
	providerModelAllowlistOnce = sync.Once{}
}

func saveProviderModelAllowlistLocked() error {
	cfgPath, err := ProviderModelAllowlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(providerModelAllowlist, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, data, 0o600)
}

func normalizeProviderModelAllowlist(state ProviderModelAllowlist) ProviderModelAllowlist {
	if state.Version == 0 {
		state.Version = 1
	}
	if state.Providers == nil {
		state.Providers = map[string][]ProviderModelSelection{}
	}
	for rawProviderID, models := range state.Providers {
		providerID := strings.TrimSpace(rawProviderID)
		if providerID == "" {
			delete(state.Providers, rawProviderID)
			continue
		}
		if providerID != rawProviderID {
			delete(state.Providers, rawProviderID)
		}
		state.Providers[providerID] = normalizeProviderModelSelections(models)
	}
	return state
}

func normalizeModelIDList(models []string) []string {
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

func normalizeProviderModelSelections(models []ProviderModelSelection) []ProviderModelSelection {
	out := make([]ProviderModelSelection, 0, len(models))
	seen := map[string]struct{}{}
	seenOriginal := map[string]struct{}{}
	for _, model := range models {
		model.Model = strings.TrimSpace(model.Model)
		model.OriginalModel = strings.TrimSpace(model.OriginalModel)
		if model.OriginalModel == "" {
			model.OriginalModel = model.Model
		}
		model.CatalogDisplayName = strings.TrimSpace(model.CatalogDisplayName)
		model.DisplayName = strings.TrimSpace(model.DisplayName)
		model.Description = strings.TrimSpace(model.Description)
		model.PipelineTag = strings.TrimSpace(model.PipelineTag)
		model.InputModalities = normalizeModelIDList(model.InputModalities)
		model.OutputModalities = normalizeModelIDList(model.OutputModalities)
		if model.Model == "" {
			continue
		}
		if _, ok := seen[model.Model]; ok {
			continue
		}
		if _, ok := seenOriginal[model.OriginalModel]; ok {
			continue
		}
		seen[model.Model] = struct{}{}
		seenOriginal[model.OriginalModel] = struct{}{}
		out = append(out, model)
	}
	return out
}

func copyProviderModelSelections(models []ProviderModelSelection) []ProviderModelSelection {
	return append([]ProviderModelSelection{}, models...)
}

func copyProviderModelAllowlist(state ProviderModelAllowlist) ProviderModelAllowlist {
	state = normalizeProviderModelAllowlist(state)
	out := ProviderModelAllowlist{
		Version:   state.Version,
		Providers: make(map[string][]ProviderModelSelection, len(state.Providers)),
	}
	for providerID, models := range state.Providers {
		out.Providers[providerID] = copyProviderModelSelections(models)
	}
	return out
}

func (s *ProviderModelSelection) UnmarshalJSON(data []byte) error {
	var model string
	if err := json.Unmarshal(data, &model); err == nil {
		s.Model = strings.TrimSpace(model)
		s.OriginalModel = s.Model
		s.CatalogDisplayName = ""
		s.DisplayName = ""
		s.Description = ""
		s.PipelineTag = ""
		s.InputModalities = nil
		s.OutputModalities = nil
		return nil
	}
	type alias ProviderModelSelection
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	s.Model = strings.TrimSpace(decoded.Model)
	s.OriginalModel = strings.TrimSpace(decoded.OriginalModel)
	if s.OriginalModel == "" {
		s.OriginalModel = s.Model
	}
	s.CatalogDisplayName = strings.TrimSpace(decoded.CatalogDisplayName)
	s.DisplayName = strings.TrimSpace(decoded.DisplayName)
	s.Description = strings.TrimSpace(decoded.Description)
	s.PipelineTag = strings.TrimSpace(decoded.PipelineTag)
	s.InputModalities = normalizeModelIDList(decoded.InputModalities)
	s.OutputModalities = normalizeModelIDList(decoded.OutputModalities)
	return nil
}
