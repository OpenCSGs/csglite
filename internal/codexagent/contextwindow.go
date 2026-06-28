package codexagent

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/opencsgs/csglite/pkg/api"
)

const (
	DefaultLocalContextWindow          int64 = 8192
	DefaultRemoteContextWindowFallback int64 = 200000
)

//go:embed context_windows.json
var contextWindowsData []byte

type contextWindowConfig struct {
	RemoteDefaultContextWindow int64                 `json:"remote_default_context_window"`
	Models                     []contextWindowPreset `json:"models"`
}

type contextWindowPreset struct {
	ID            string   `json:"id"`
	ContextWindow int64    `json:"context_window"`
	Aliases       []string `json:"aliases,omitempty"`
}

var (
	contextWindowOnce sync.Once
	contextWindowCfg  contextWindowConfig
	contextWindowMap  map[string]int64
	contextWindowKeys []string
)

func RemoteDefaultContextWindow(fallback int64) int64 {
	cfg := loadContextWindowConfig()
	if cfg.RemoteDefaultContextWindow > 0 {
		return cfg.RemoteDefaultContextWindow
	}
	return fallback
}

func ContextWindowForModel(item api.ModelInfo, localFallback, remoteFallback int64) int64 {
	if item.ContextWindow > 0 {
		return item.ContextWindow
	}
	if strings.EqualFold(strings.TrimSpace(item.Source), "local") {
		return localFallback
	}
	if value, ok := LookupContextWindow(item.Model, item.Name, item.DisplayName, item.Label); ok {
		return value
	}
	return RemoteDefaultContextWindow(remoteFallback)
}

func LookupContextWindow(modelIDs ...string) (int64, bool) {
	loadContextWindowConfig()
	for _, modelID := range modelIDs {
		for _, key := range contextWindowLookupKeys(modelID) {
			if value, ok := contextWindowMap[key]; ok && value > 0 {
				return value, true
			}
		}
	}
	for _, modelID := range modelIDs {
		for _, candidate := range contextWindowLookupKeys(modelID) {
			for _, presetKey := range contextWindowKeys {
				if strings.Contains(candidate, presetKey) || strings.Contains(presetKey, candidate) {
					if value := contextWindowMap[presetKey]; value > 0 {
						return value, true
					}
				}
			}
		}
	}
	return 0, false
}

func loadContextWindowConfig() contextWindowConfig {
	contextWindowOnce.Do(func() {
		_ = json.Unmarshal(contextWindowsData, &contextWindowCfg)
		contextWindowMap = make(map[string]int64)
		for _, preset := range contextWindowCfg.Models {
			if preset.ContextWindow <= 0 {
				continue
			}
			for _, modelID := range append([]string{preset.ID}, preset.Aliases...) {
				for _, key := range contextWindowLookupKeys(modelID) {
					if key == "" {
						continue
					}
					if _, ok := contextWindowMap[key]; !ok {
						contextWindowKeys = append(contextWindowKeys, key)
					}
					contextWindowMap[key] = preset.ContextWindow
				}
			}
		}
		sort.SliceStable(contextWindowKeys, func(i, j int) bool {
			if len(contextWindowKeys[i]) == len(contextWindowKeys[j]) {
				return contextWindowKeys[i] < contextWindowKeys[j]
			}
			return len(contextWindowKeys[i]) > len(contextWindowKeys[j])
		})
	})
	return contextWindowCfg
}

func contextWindowLookupKeys(modelID string) []string {
	normalized := normalizeContextWindowModelID(modelID)
	if normalized == "" {
		return nil
	}
	keys := []string{normalized}
	if idx := strings.LastIndex(normalized, "/"); idx >= 0 && idx+1 < len(normalized) {
		keys = append(keys, normalized[idx+1:])
	}
	return keys
}

func normalizeContextWindowModelID(modelID string) string {
	modelID = strings.TrimSpace(strings.ToLower(modelID))
	if modelID == "" {
		return ""
	}
	if idx := strings.LastIndex(modelID, " ["); idx > 0 && strings.HasSuffix(modelID, "]") {
		modelID = strings.TrimSpace(modelID[:idx])
	}
	return strings.Join(strings.Fields(modelID), "")
}
