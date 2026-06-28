package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/opencsgs/csglite/internal/csghub"
)

// InferenceModelID returns the public model identifier used for local inference APIs.
func InferenceModelID(lm *LocalModel) string {
	if lm == nil {
		return ""
	}
	return strings.TrimSpace(lm.Name)
}

// PublicModelIDs returns stable short model IDs keyed by each model's full
// namespace/name ID. When names collide, the first downloaded model keeps the
// bare name and later colliding models get a stable hash suffix.
func PublicModelIDs(models []*LocalModel) map[string]string {
	groups := make(map[string][]*LocalModel, len(models))
	baseNames := make(map[string]struct{}, len(models))
	for _, lm := range models {
		if lm == nil {
			continue
		}
		name := InferenceModelID(lm)
		if name == "" {
			continue
		}
		groups[name] = append(groups[name], lm)
		baseNames[name] = struct{}{}
	}

	out := make(map[string]string, len(models))
	used := make(map[string]string, len(models))
	baseModels := make([]*LocalModel, 0, len(groups))
	suffixedModels := make([]*LocalModel, 0, len(models))
	for _, group := range groups {
		sortLocalModelsByPublicIDPriority(group)
		baseModels = append(baseModels, group[0])
		if len(group) > 1 {
			suffixedModels = append(suffixedModels, group[1:]...)
		}
	}

	sort.SliceStable(baseModels, func(i, j int) bool {
		return baseModels[i].FullName() < baseModels[j].FullName()
	})
	for _, lm := range baseModels {
		publicID := InferenceModelID(lm)
		out[lm.FullName()] = publicID
		used[publicID] = lm.FullName()
	}

	sort.SliceStable(suffixedModels, func(i, j int) bool {
		return suffixedModels[i].FullName() < suffixedModels[j].FullName()
	})
	for _, lm := range suffixedModels {
		publicID := uniqueSuffixedPublicModelID(lm, baseNames, used)
		out[lm.FullName()] = publicID
		used[publicID] = lm.FullName()
	}

	return out
}

func sortLocalModelsByPublicIDPriority(models []*LocalModel) {
	sort.SliceStable(models, func(i, j int) bool {
		if !models[i].DownloadedAt.Equal(models[j].DownloadedAt) {
			return models[i].DownloadedAt.Before(models[j].DownloadedAt)
		}
		return models[i].FullName() < models[j].FullName()
	})
}

func uniqueSuffixedPublicModelID(lm *LocalModel, baseNames map[string]struct{}, used map[string]string) string {
	name := InferenceModelID(lm)
	fullName := lm.FullName()
	sum := sha256.Sum256([]byte(fullName))
	hash := hex.EncodeToString(sum[:])
	for length := 6; length <= len(hash); length += 2 {
		candidate := name + "-" + hash[:length]
		if owner, ok := used[candidate]; ok && owner != fullName {
			continue
		}
		if _, ok := baseNames[candidate]; ok {
			continue
		}
		return candidate
	}
	return name + "-" + hash
}

// ResolveLocalModel resolves a downloaded local model by full ID (namespace/name)
// or by short name (name only).
func (m *Manager) ResolveLocalModel(modelID string) (*LocalModel, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, fmt.Errorf("model ID is required")
	}

	if strings.Contains(modelID, "/") {
		lm, err := m.Get(modelID)
		if err != nil {
			return nil, fmt.Errorf("model %q not found locally", modelID)
		}
		return lm, nil
	}

	models, err := m.List()
	if err != nil {
		return nil, err
	}

	publicIDs := PublicModelIDs(models)
	var matches []*LocalModel
	for _, lm := range models {
		if lm != nil && publicIDs[lm.FullName()] == modelID {
			matches = append(matches, lm)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("model %q not found locally", modelID)
	case 1:
		return matches[0], nil
	default:
		fullNames := make([]string, 0, len(matches))
		for _, lm := range matches {
			fullNames = append(fullNames, lm.FullName())
		}
		sort.Strings(fullNames)
		return nil, fmt.Errorf("ambiguous local model name %q: %s", modelID, strings.Join(fullNames, ", "))
	}
}

// PublicModelID resolves a local model ID and returns its public inference ID.
func (m *Manager) PublicModelID(modelID string) (string, error) {
	lm, err := m.ResolveLocalModel(modelID)
	if err != nil {
		return "", err
	}
	models, err := m.List()
	if err != nil {
		return "", err
	}
	if publicID := PublicModelIDs(models)[lm.FullName()]; publicID != "" {
		return publicID, nil
	}
	return InferenceModelID(lm), nil
}

// ResolveLocalModelID returns the on-disk namespace/name identifier for a local model.
func (m *Manager) ResolveLocalModelID(modelID string) (string, error) {
	lm, err := m.ResolveLocalModel(modelID)
	if err != nil {
		return "", err
	}
	if _, _, err := csghub.ParseModelID(lm.FullName()); err != nil {
		return "", err
	}
	return lm.FullName(), nil
}
