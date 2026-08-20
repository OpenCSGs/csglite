package model

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/modelregistry"
)

const modelPullStateFile = ".csghub-lite-pull.json"

type modelPullState struct {
	ArtifactSource string `json:"artifact_source"`
	Revision       string `json:"revision,omitempty"`
}

type Manager struct {
	cfg *config.Config
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg: cfg,
	}
}

// Pull downloads a model from CSGHub.
// quants selects GGUF weight variants when the repository exposes multiple quantizations (for example Q4_K_M or Q8_0).
// Empty quants keeps the default behavior (highest-precision GGUF variant). Non-GGUF models ignore quants.
func (m *Manager) Pull(ctx context.Context, modelID string, quants []string, progress csghub.SnapshotProgressFunc) (*LocalModel, error) {
	return m.PullFrom(ctx, modelID, string(modelregistry.SourceOpenCSG), "", quants, progress)
}

// PullFrom downloads a model from an artifact registry. Empty source preserves
// the historical OpenCSG behavior.
func (m *Manager) PullFrom(ctx context.Context, modelID, sourceValue, revision string, quants []string, progress csghub.SnapshotProgressFunc) (*LocalModel, error) {
	namespace, name, err := parseSafeLocalModelID(modelID)
	if err != nil {
		return nil, err
	}
	source, err := modelregistry.NormalizeSource(sourceValue)
	if err != nil {
		return nil, err
	}
	revision = strings.TrimSpace(revision)

	destDir := RegistryModelDir(m.cfg.ModelDir, string(source), namespace, name)
	if existing, loadErr := LoadManifestInDir(destDir); loadErr == nil {
		existingSource, sourceErr := modelregistry.NormalizeSource(existing.ArtifactSource)
		if sourceErr != nil {
			return nil, fmt.Errorf("installed model %q has invalid artifact source: %w", modelID, sourceErr)
		}
		if existingSource != source {
			return nil, fmt.Errorf("model directory %q belongs to %s, not %s", destDir, existingSource, source)
		}
		if source != modelregistry.SourceOpenCSG {
			return nil, fmt.Errorf("model %q is already installed from %s; remove it before downloading another snapshot", modelID, source)
		}
		existingRevision := strings.TrimSpace(existing.RequestedRevision)
		if existingRevision == "" {
			existingRevision = strings.TrimSpace(existing.ResolvedRevision)
		}
		if revision != "" && existingRevision != "" && revision != existingRevision {
			return nil, fmt.Errorf("model %q is already installed at revision %s; remove it before downloading revision %s", modelID, existingRevision, revision)
		}
	}
	if state, stateErr := loadModelPullState(destDir); stateErr == nil {
		stateSource, sourceErr := modelregistry.NormalizeSource(state.ArtifactSource)
		if sourceErr != nil {
			return nil, fmt.Errorf("partial download for %q has invalid artifact source: %w", modelID, sourceErr)
		}
		if stateSource != source || strings.TrimSpace(state.Revision) != revision {
			return nil, fmt.Errorf("model %q has incompatible partial download state in %s", modelID, destDir)
		}
	} else if !os.IsNotExist(stateErr) {
		return nil, fmt.Errorf("reading model pull state: %w", stateErr)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating model dir: %w", err)
	}

	registry, err := modelregistry.New(m.cfg, source)
	if err != nil {
		return nil, err
	}
	info, err := registry.GetModel(ctx, modelID, revision)
	if err != nil {
		return nil, fmt.Errorf("fetching model info: %w", err)
	}
	if err := saveModelPullState(destDir, modelPullState{ArtifactSource: string(source), Revision: revision}); err != nil {
		return nil, fmt.Errorf("saving model pull state: %w", err)
	}

	downloadedFiles, resolvedRevision, err := registry.DownloadSnapshot(ctx, modelID, revision, destDir, quants, progress)
	if err != nil {
		return nil, fmt.Errorf("downloading model into %s: %w", destDir, err)
	}

	var fileNames []string
	var fileEntries []LocalModelFile
	var totalSize int64
	for _, f := range downloadedFiles {
		relPath := cleanLocalModelPath(filepath.ToSlash(f.Path))
		if relPath == "" {
			relPath = cleanLocalModelPath(f.Name)
		}
		if relPath == "" {
			continue
		}
		fileNames = append(fileNames, relPath)
		entry := LocalModelFile{
			Path: relPath,
			Size: f.Size,
			LFS:  f.LFS,
		}
		if f.LFSSHA256 != "" {
			entry.SHA256 = f.LFSSHA256
		}
		fileEntries = append(fileEntries, entry)
		totalSize += f.Size
	}

	lm := &LocalModel{
		Namespace:         namespace,
		Name:              name,
		Format:            DetectFormat(fileNames),
		Size:              totalSize,
		Files:             fileNames,
		FileEntries:       fileEntries,
		DownloadedAt:      time.Now(),
		Origin:            LocalModelOriginMarketplace,
		PipelineTag:       DetectPipelineTag(destDir),
		Description:       info.Description,
		License:           info.License,
		ArtifactSource:    string(source),
		Repository:        modelID,
		RequestedRevision: revision,
		ResolvedRevision:  firstNonEmptyModelValue(info.Revision, resolvedRevision, revision, registry.DefaultRevision()),
	}

	if err := SaveManifestInDir(destDir, lm); err != nil {
		return nil, fmt.Errorf("saving manifest: %w", err)
	}
	_ = os.Remove(filepath.Join(destDir, modelPullStateFile))

	return lm, nil
}

func loadModelPullState(modelDir string) (modelPullState, error) {
	var state modelPullState
	body, err := os.ReadFile(filepath.Join(modelDir, modelPullStateFile))
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return state, err
	}
	return state, nil
}

func saveModelPullState(modelDir string, state modelPullState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(modelDir, modelPullStateFile), body, 0o600)
}

func firstNonEmptyModelValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// List returns all locally downloaded models.
func (m *Manager) List() ([]*LocalModel, error) {
	topLevel, err := os.ReadDir(m.cfg.ModelDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var models []*LocalModel
	for _, top := range topLevel {
		if !top.IsDir() || top.Name() == registryModelsDir {
			continue
		}
		topDir := filepath.Join(m.cfg.ModelDir, top.Name())
		secondLevel, readErr := os.ReadDir(topDir)
		if readErr != nil {
			continue
		}
		for _, second := range secondLevel {
			if !second.IsDir() {
				continue
			}
			secondDir := filepath.Join(topDir, second.Name())
			if lm, loadErr := LoadManifestInDir(secondDir); loadErr == nil {
				models = append(models, lm)
			}
		}
	}

	registryRoot := filepath.Join(m.cfg.ModelDir, registryModelsDir)
	sourceDirs, err := os.ReadDir(registryRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, sourceDir := range sourceDirs {
		if !sourceDir.IsDir() {
			continue
		}
		source, sourceErr := modelregistry.NormalizeSource(sourceDir.Name())
		if sourceErr != nil || source == modelregistry.SourceOpenCSG {
			continue
		}
		namespaceRoot := filepath.Join(registryRoot, sourceDir.Name())
		namespaceDirs, readErr := os.ReadDir(namespaceRoot)
		if readErr != nil {
			continue
		}
		for _, namespaceDir := range namespaceDirs {
			if !namespaceDir.IsDir() {
				continue
			}
			modelRoot := filepath.Join(namespaceRoot, namespaceDir.Name())
			modelDirs, readErr := os.ReadDir(modelRoot)
			if readErr != nil {
				continue
			}
			for _, modelEntry := range modelDirs {
				if !modelEntry.IsDir() {
					continue
				}
				modelDir := filepath.Join(modelRoot, modelEntry.Name())
				lm, loadErr := LoadManifestInDir(modelDir)
				if loadErr != nil {
					continue
				}
				manifestSource, manifestErr := modelregistry.NormalizeSource(lm.ArtifactSource)
				if manifestErr == nil && manifestSource == source {
					models = append(models, lm)
				}
			}
		}
	}
	return models, nil
}

// Get returns a locally downloaded model by ID.
func (m *Manager) Get(modelID string) (*LocalModel, error) {
	source, namespace, name, err := parseStorageModelID(modelID)
	if err != nil {
		return nil, err
	}
	return LoadManifestInDir(RegistryModelDir(m.cfg.ModelDir, string(source), namespace, name))
}

// GetWithFileEntries returns a local model with file-level metadata filled in.
func (m *Manager) GetWithFileEntries(modelID string) (*LocalModel, error) {
	lm, err := m.Get(modelID)
	if err != nil {
		return nil, err
	}

	modelDir, err := m.ModelPath(modelID)
	if err != nil {
		return nil, err
	}

	changed, err := EnsureLocalModelFiles(modelDir, lm)
	if err != nil {
		return nil, fmt.Errorf("ensuring file entries: %w", err)
	}
	if changed {
		if err := SaveManifestInDir(modelDir, lm); err != nil {
			return nil, fmt.Errorf("saving manifest: %w", err)
		}
	}

	return lm, nil
}

// Remove deletes a locally downloaded model.
func (m *Manager) Remove(modelID string) error {
	source, namespace, name, err := parseStorageModelID(modelID)
	if err != nil {
		return err
	}

	modelDir := RegistryModelDir(m.cfg.ModelDir, string(source), namespace, name)
	if _, err := LoadManifestInDir(modelDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("model %q not found locally", modelID)
		}
		return err
	}

	return RemoveRegistryModelDir(m.cfg.ModelDir, string(source), namespace, name)
}

// ModelPath returns the directory path for a model.
func (m *Manager) ModelPath(modelID string) (string, error) {
	source, namespace, name, err := parseStorageModelID(modelID)
	if err != nil {
		return "", err
	}
	dir := RegistryModelDir(m.cfg.ModelDir, string(source), namespace, name)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("model %q not found locally", modelID)
	}
	return dir, nil
}

// Exists checks if a model is downloaded locally.
func (m *Manager) Exists(modelID string) bool {
	source, namespace, name, err := parseStorageModelID(modelID)
	if err != nil {
		return false
	}
	_, err = LoadManifestInDir(RegistryModelDir(m.cfg.ModelDir, string(source), namespace, name))
	return err == nil
}

// RemovePartial removes an incomplete registry download without touching an
// installed model. It returns the removed directory for user-facing diagnostics.
func (m *Manager) RemovePartial(modelID, sourceValue string) (string, error) {
	namespace, name, err := parseSafeLocalModelID(modelID)
	if err != nil {
		return "", err
	}
	source, err := modelregistry.NormalizeSource(sourceValue)
	if err != nil {
		return "", err
	}
	modelDir := RegistryModelDir(m.cfg.ModelDir, string(source), namespace, name)
	if _, err := LoadManifestInDir(modelDir); err == nil {
		return "", fmt.Errorf("model %q is already installed", modelID)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking installed model: %w", err)
	}
	statePath := filepath.Join(modelDir, modelPullStateFile)
	if _, err := os.Stat(statePath); err != nil {
		if os.IsNotExist(err) {
			return modelDir, nil
		}
		return "", fmt.Errorf("checking partial download: %w", err)
	}
	if err := RemoveRegistryModelDir(m.cfg.ModelDir, string(source), namespace, name); err != nil {
		return "", fmt.Errorf("removing partial download: %w", err)
	}
	return modelDir, nil
}

func parseStorageModelID(modelID string) (modelregistry.Source, string, string, error) {
	trimmed := strings.Trim(strings.TrimSpace(modelID), "/")
	parts := strings.Split(trimmed, "/")
	source := modelregistry.SourceOpenCSG
	repository := trimmed
	if len(parts) == 3 {
		var err error
		source, err = modelregistry.NormalizeSource(parts[0])
		if err != nil {
			return "", "", "", err
		}
		repository = strings.Join(parts[1:], "/")
	} else if len(parts) != 2 {
		return "", "", "", fmt.Errorf("invalid model ID %q: expected namespace/name or source/namespace/name", modelID)
	}
	namespace, name, err := parseSafeLocalModelID(repository)
	if err != nil {
		return "", "", "", err
	}
	return source, namespace, name, nil
}

// Client returns the underlying CSGHub client.
func (m *Manager) Client() *csghub.Client {
	return csghub.NewClient(m.cfg.ServerURL, m.cfg.Token)
}
