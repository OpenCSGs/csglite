package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const ProviderModelAllowlistFile = "provider_model_allowlist.json"

type ProviderModelAllowlist struct {
	Version   int                 `json:"version"`
	Providers map[string][]string `json:"providers"`
}

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
			Providers: map[string][]string{},
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
	providerModelAllowlistMu.RLock()
	if providerModelAllowlist.Providers != nil {
		models := append([]string{}, providerModelAllowlist.Providers[strings.TrimSpace(providerID)]...)
		providerModelAllowlistMu.RUnlock()
		return models
	}
	providerModelAllowlistMu.RUnlock()

	state, _ := LoadProviderModelAllowlist()
	return append([]string{}, state.Providers[strings.TrimSpace(providerID)]...)
}

func ReplaceProviderModelAllowlist(providerID string, models []string) error {
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
		providerModelAllowlist.Providers = map[string][]string{}
	}
	providerModelAllowlist.Providers[providerID] = normalizeModelIDList(models)
	return saveProviderModelAllowlistLocked()
}

func AddProviderModelAllowlist(providerID, modelID string) error {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return nil
	}
	models := GetProviderModelAllowlist(providerID)
	models = append(models, modelID)
	return ReplaceProviderModelAllowlist(providerID, models)
}

func RemoveProviderModelAllowlist(providerID, modelID string) (bool, error) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return false, nil
	}

	models := GetProviderModelAllowlist(providerID)
	out := make([]string, 0, len(models))
	removed := false
	for _, model := range models {
		if model == modelID {
			removed = true
			continue
		}
		out = append(out, model)
	}
	if !removed {
		return false, nil
	}
	return true, ReplaceProviderModelAllowlist(providerID, out)
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
		state.Providers = map[string][]string{}
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
		state.Providers[providerID] = normalizeModelIDList(models)
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

func copyProviderModelAllowlist(state ProviderModelAllowlist) ProviderModelAllowlist {
	state = normalizeProviderModelAllowlist(state)
	out := ProviderModelAllowlist{
		Version:   state.Version,
		Providers: make(map[string][]string, len(state.Providers)),
	}
	for providerID, models := range state.Providers {
		out.Providers[providerID] = append([]string{}, models...)
	}
	return out
}
