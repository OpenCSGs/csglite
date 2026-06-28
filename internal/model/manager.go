package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
)

type Manager struct {
	cfg    *config.Config
	client *csghub.Client
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg:    cfg,
		client: csghub.NewClient(cfg.ServerURL, cfg.Token),
	}
}

// Pull downloads a model from CSGHub.
// quant selects a GGUF weight variant when the repository exposes multiple quantizations (for example Q4_K_M or Q8_0).
// Empty quant keeps the default behavior (highest-precision GGUF variant). Non-GGUF models ignore quant.
func (m *Manager) Pull(ctx context.Context, modelID string, quant string, progress csghub.SnapshotProgressFunc) (*LocalModel, error) {
	namespace, name, err := csghub.ParseModelID(modelID)
	if err != nil {
		return nil, err
	}

	destDir := ModelDir(m.cfg.ModelDir, namespace, name)
	if err := EnsureModelDir(m.cfg.ModelDir, namespace, name); err != nil {
		return nil, fmt.Errorf("creating model dir: %w", err)
	}

	client := csghub.NewClient(m.cfg.ServerURL, m.cfg.Token)
	info, err := client.GetModel(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("fetching model info: %w", err)
	}

	downloadedFiles, err := client.SnapshotDownload(ctx, namespace, name, destDir, quant, progress)
	if err != nil {
		return nil, fmt.Errorf("downloading model: %w", err)
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
		Namespace:    namespace,
		Name:         name,
		Format:       DetectFormat(fileNames),
		Size:         totalSize,
		Files:        fileNames,
		FileEntries:  fileEntries,
		DownloadedAt: time.Now(),
		Origin:       LocalModelOriginMarketplace,
		PipelineTag:  DetectPipelineTag(destDir),
		Description:  info.Description,
		License:      info.License,
	}

	if err := SaveManifest(m.cfg.ModelDir, lm); err != nil {
		return nil, fmt.Errorf("saving manifest: %w", err)
	}

	return lm, nil
}

// List returns all locally downloaded models.
func (m *Manager) List() ([]*LocalModel, error) {
	namespaces, err := ListNamespaces(m.cfg.ModelDir)
	if err != nil {
		return nil, err
	}

	var models []*LocalModel
	for _, ns := range namespaces {
		names, err := ListModelsInNamespace(m.cfg.ModelDir, ns)
		if err != nil {
			continue
		}
		for _, name := range names {
			lm, err := LoadManifest(m.cfg.ModelDir, ns, name)
			if err != nil {
				continue
			}
			models = append(models, lm)
		}
	}
	return models, nil
}

// Get returns a locally downloaded model by ID.
func (m *Manager) Get(modelID string) (*LocalModel, error) {
	namespace, name, err := csghub.ParseModelID(modelID)
	if err != nil {
		return nil, err
	}
	return LoadManifest(m.cfg.ModelDir, namespace, name)
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
		if err := SaveManifest(m.cfg.ModelDir, lm); err != nil {
			return nil, fmt.Errorf("saving manifest: %w", err)
		}
	}

	return lm, nil
}

// Remove deletes a locally downloaded model.
func (m *Manager) Remove(modelID string) error {
	namespace, name, err := csghub.ParseModelID(modelID)
	if err != nil {
		return err
	}

	if _, err := LoadManifest(m.cfg.ModelDir, namespace, name); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("model %q not found locally", modelID)
		}
		return err
	}

	return RemoveModelDir(m.cfg.ModelDir, namespace, name)
}

// ModelPath returns the directory path for a model.
func (m *Manager) ModelPath(modelID string) (string, error) {
	namespace, name, err := csghub.ParseModelID(modelID)
	if err != nil {
		return "", err
	}
	dir := ModelDir(m.cfg.ModelDir, namespace, name)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("model %q not found locally", modelID)
	}
	return dir, nil
}

// Exists checks if a model is downloaded locally.
func (m *Manager) Exists(modelID string) bool {
	namespace, name, err := csghub.ParseModelID(modelID)
	if err != nil {
		return false
	}
	_, err = LoadManifest(m.cfg.ModelDir, namespace, name)
	return err == nil
}

// Client returns the underlying CSGHub client.
func (m *Manager) Client() *csghub.Client {
	return csghub.NewClient(m.cfg.ServerURL, m.cfg.Token)
}
