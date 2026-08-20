package dataset

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/datasetregistry"
)

const datasetPullStateFile = ".csghub-lite-pull.json"

type datasetPullState struct {
	ArtifactSource string `json:"artifact_source"`
	Revision       string `json:"revision,omitempty"`
}

type Manager struct {
	cfg *config.Config
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

func (m *Manager) Pull(ctx context.Context, datasetID string, progress csghub.SnapshotProgressFunc) (*LocalDataset, error) {
	return m.PullFrom(ctx, datasetID, string(datasetregistry.SourceOpenCSG), "", progress)
}

// PullFrom downloads a dataset through a source-specific registry.
func (m *Manager) PullFrom(ctx context.Context, datasetID, sourceValue, revision string, progress csghub.SnapshotProgressFunc) (*LocalDataset, error) {
	namespace, name, err := parseSafeLocalDatasetID(datasetID)
	if err != nil {
		return nil, err
	}
	source, err := datasetregistry.NormalizeSource(sourceValue)
	if err != nil {
		return nil, err
	}
	revision = strings.TrimSpace(revision)
	destDir := RegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name)

	if existing, loadErr := LoadManifestInDir(destDir); loadErr == nil {
		existingSource, sourceErr := datasetregistry.NormalizeSource(existing.ArtifactSource)
		if sourceErr != nil {
			return nil, fmt.Errorf("installed dataset %q has invalid artifact source: %w", datasetID, sourceErr)
		}
		if existingSource != source {
			return nil, fmt.Errorf("dataset directory %q belongs to %s, not %s", destDir, existingSource, source)
		}
		if source != datasetregistry.SourceOpenCSG {
			return nil, fmt.Errorf("dataset %q is already installed from %s; remove it before downloading another snapshot", datasetID, source)
		}
		existingRevision := strings.TrimSpace(existing.RequestedRevision)
		if existingRevision == "" {
			existingRevision = strings.TrimSpace(existing.ResolvedRevision)
		}
		if revision != "" && existingRevision != "" && revision != existingRevision {
			return nil, fmt.Errorf("dataset %q is already installed at revision %s; remove it before downloading revision %s", datasetID, existingRevision, revision)
		}
	}
	if state, stateErr := loadDatasetPullState(destDir); stateErr == nil {
		stateSource, sourceErr := datasetregistry.NormalizeSource(state.ArtifactSource)
		if sourceErr != nil {
			return nil, fmt.Errorf("partial download for %q has invalid artifact source: %w", datasetID, sourceErr)
		}
		if stateSource != source || strings.TrimSpace(state.Revision) != revision {
			return nil, fmt.Errorf("dataset %q has incompatible partial download state in %s", datasetID, destDir)
		}
	} else if !os.IsNotExist(stateErr) {
		return nil, fmt.Errorf("reading dataset pull state: %w", stateErr)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating dataset dir: %w", err)
	}

	registry, err := datasetregistry.New(m.cfg, source)
	if err != nil {
		return nil, err
	}
	info, err := registry.GetDataset(ctx, datasetID, revision)
	if err != nil {
		return nil, fmt.Errorf("fetching dataset info: %w", err)
	}
	if err := saveDatasetPullState(destDir, datasetPullState{ArtifactSource: string(source), Revision: revision}); err != nil {
		return nil, fmt.Errorf("saving dataset pull state: %w", err)
	}
	downloadedFiles, resolvedRevision, err := registry.DownloadSnapshot(ctx, datasetID, revision, destDir, progress)
	if err != nil {
		return nil, fmt.Errorf("downloading dataset into %s: %w", destDir, err)
	}

	var fileNames []string
	var fileEntries []LocalDatasetFile
	var totalSize int64
	for _, f := range downloadedFiles {
		relPath := cleanLocalDatasetPath(filepath.ToSlash(f.Path))
		if relPath == "" {
			relPath = cleanLocalDatasetPath(f.Name)
		}
		if relPath == "" {
			continue
		}
		size := f.Size
		if size <= 0 {
			if info, statErr := os.Stat(filepath.Join(destDir, filepath.FromSlash(relPath))); statErr == nil {
				size = info.Size()
			}
		}
		fileNames = append(fileNames, relPath)
		entry := LocalDatasetFile{Path: relPath, Size: size, LFS: f.LFS}
		if f.LFSSHA256 != "" {
			entry.SHA256 = f.LFSSHA256
		} else if len(strings.TrimPrefix(f.SHA, "sha256:")) == 64 {
			entry.SHA256 = strings.TrimPrefix(f.SHA, "sha256:")
		}
		fileEntries = append(fileEntries, entry)
		totalSize += size
	}
	ld := &LocalDataset{
		Namespace: namespace, Name: name, Size: totalSize, Files: fileNames,
		FileEntries: fileEntries, DownloadedAt: time.Now(),
		Origin: LocalDatasetOriginMarketplace, Description: info.Description,
		License: info.License, ArtifactSource: string(source), Repository: datasetID,
		RequestedRevision: revision,
		ResolvedRevision:  firstNonEmptyDatasetValue(info.Revision, resolvedRevision, revision, registry.DefaultRevision()),
	}
	if err := SaveManifestInDir(destDir, ld); err != nil {
		return nil, fmt.Errorf("saving manifest: %w", err)
	}
	_ = os.Remove(filepath.Join(destDir, datasetPullStateFile))
	return ld, nil
}

func loadDatasetPullState(datasetDir string) (datasetPullState, error) {
	var state datasetPullState
	body, err := os.ReadFile(filepath.Join(datasetDir, datasetPullStateFile))
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return state, err
	}
	return state, nil
}

func saveDatasetPullState(datasetDir string, state datasetPullState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(datasetDir, datasetPullStateFile), body, 0o600)
}

func firstNonEmptyDatasetValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (m *Manager) List() ([]*LocalDataset, error) {
	topLevel, err := os.ReadDir(m.cfg.DatasetDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var datasets []*LocalDataset
	for _, namespace := range topLevel {
		if !namespace.IsDir() || namespace.Name() == registryDatasetsDir {
			continue
		}
		namespaceDir := filepath.Join(m.cfg.DatasetDir, namespace.Name())
		entries, readErr := os.ReadDir(namespaceDir)
		if readErr != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if ld, loadErr := LoadManifestInDir(filepath.Join(namespaceDir, entry.Name())); loadErr == nil {
				manifestSource, sourceErr := datasetregistry.NormalizeSource(ld.ArtifactSource)
				if sourceErr == nil && manifestSource == datasetregistry.SourceOpenCSG {
					datasets = append(datasets, ld)
				}
			}
		}
	}

	registryRoot := filepath.Join(m.cfg.DatasetDir, registryDatasetsDir)
	sourceDirs, err := os.ReadDir(registryRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, sourceDir := range sourceDirs {
		if !sourceDir.IsDir() {
			continue
		}
		source, sourceErr := datasetregistry.NormalizeSource(sourceDir.Name())
		if sourceErr != nil || source == datasetregistry.SourceOpenCSG {
			continue
		}
		namespaceDirs, readErr := os.ReadDir(filepath.Join(registryRoot, sourceDir.Name()))
		if readErr != nil {
			continue
		}
		for _, namespaceDir := range namespaceDirs {
			if !namespaceDir.IsDir() {
				continue
			}
			datasetRoot := filepath.Join(registryRoot, sourceDir.Name(), namespaceDir.Name())
			datasetDirs, readErr := os.ReadDir(datasetRoot)
			if readErr != nil {
				continue
			}
			for _, datasetEntry := range datasetDirs {
				if !datasetEntry.IsDir() {
					continue
				}
				ld, loadErr := LoadManifestInDir(filepath.Join(datasetRoot, datasetEntry.Name()))
				if loadErr != nil {
					continue
				}
				manifestSource, manifestErr := datasetregistry.NormalizeSource(ld.ArtifactSource)
				if manifestErr == nil && manifestSource == source {
					datasets = append(datasets, ld)
				}
			}
		}
	}
	return datasets, nil
}

func (m *Manager) Get(datasetID string) (*LocalDataset, error) {
	source, namespace, name, err := parseStorageDatasetID(datasetID)
	if err != nil {
		return nil, err
	}
	return LoadManifestInDir(RegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name))
}

func (m *Manager) GetWithFileEntries(datasetID string) (*LocalDataset, error) {
	ld, err := m.Get(datasetID)
	if err != nil {
		return nil, err
	}

	datasetDir, err := m.DatasetPath(datasetID)
	if err != nil {
		return nil, err
	}

	changed, err := EnsureLocalDatasetFiles(datasetDir, ld)
	if err != nil {
		return nil, fmt.Errorf("ensuring file entries: %w", err)
	}
	if changed {
		if err := SaveManifestInDir(datasetDir, ld); err != nil {
			return nil, fmt.Errorf("saving manifest: %w", err)
		}
	}

	return ld, nil
}

func (m *Manager) Remove(datasetID string) error {
	source, namespace, name, err := parseStorageDatasetID(datasetID)
	if err != nil {
		return err
	}

	if _, err := LoadManifestInDir(RegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("dataset %q not found locally", datasetID)
		}
		return err
	}

	return RemoveRegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name)
}

func (m *Manager) DatasetPath(datasetID string) (string, error) {
	source, namespace, name, err := parseStorageDatasetID(datasetID)
	if err != nil {
		return "", err
	}
	dir := RegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("dataset %q not found locally", datasetID)
	}
	return dir, nil
}

func (m *Manager) Exists(datasetID string) bool {
	source, namespace, name, err := parseStorageDatasetID(datasetID)
	if err != nil {
		return false
	}
	_, err = LoadManifestInDir(RegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name))
	return err == nil
}

func (m *Manager) ListFiles(datasetID, subPath string) ([]FileEntry, error) {
	source, namespace, name, err := parseStorageDatasetID(datasetID)
	if err != nil {
		return nil, err
	}
	dir := RegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name)
	target := dir
	if strings.TrimSpace(subPath) != "" {
		safePath, pathErr := safeDatasetSubPath(subPath)
		if pathErr != nil {
			return nil, pathErr
		}
		target = filepath.Join(dir, safePath)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving dataset directory: %w", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}
	relativeTarget, err := filepath.Rel(resolvedDir, resolvedTarget)
	if err != nil || relativeTarget == ".." || strings.HasPrefix(relativeTarget, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("invalid dataset path %q", subPath)
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}

	var result []FileEntry
	for _, e := range entries {
		if e.Name() == "manifest.json" && subPath == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fe := FileEntry{
			Name:       e.Name(),
			IsDir:      e.IsDir(),
			Size:       info.Size(),
			ModifiedAt: info.ModTime(),
		}
		if e.IsDir() {
			fe.Size = dirSize(filepath.Join(target, e.Name()))
		}
		result = append(result, fe)
	}
	return result, nil
}

// RemovePartial removes an incomplete source-specific download without
// touching any installed dataset.
func (m *Manager) RemovePartial(datasetID, sourceValue string) (string, error) {
	namespace, name, err := parseSafeLocalDatasetID(datasetID)
	if err != nil {
		return "", err
	}
	source, err := datasetregistry.NormalizeSource(sourceValue)
	if err != nil {
		return "", err
	}
	datasetDir := RegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name)
	if _, err := LoadManifestInDir(datasetDir); err == nil {
		return "", fmt.Errorf("dataset %q is already installed", datasetID)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking installed dataset: %w", err)
	}
	if _, err := os.Stat(filepath.Join(datasetDir, datasetPullStateFile)); err != nil {
		if os.IsNotExist(err) {
			return datasetDir, nil
		}
		return "", fmt.Errorf("checking partial download: %w", err)
	}
	if err := RemoveRegistryDatasetDir(m.cfg.DatasetDir, string(source), namespace, name); err != nil {
		return "", fmt.Errorf("removing partial download: %w", err)
	}
	return datasetDir, nil
}

func parseStorageDatasetID(datasetID string) (datasetregistry.Source, string, string, error) {
	trimmed := strings.Trim(strings.TrimSpace(datasetID), "/")
	parts := strings.Split(trimmed, "/")
	source := datasetregistry.SourceOpenCSG
	repository := trimmed
	if len(parts) == 3 {
		var err error
		source, err = datasetregistry.NormalizeSource(parts[0])
		if err != nil {
			return "", "", "", err
		}
		repository = strings.Join(parts[1:], "/")
	} else if len(parts) != 2 {
		return "", "", "", fmt.Errorf("invalid dataset ID %q: expected namespace/name or source/namespace/name", datasetID)
	}
	namespace, name, err := parseSafeLocalDatasetID(repository)
	if err != nil {
		return "", "", "", err
	}
	return source, namespace, name, nil
}

func parseSafeLocalDatasetID(datasetID string) (string, string, error) {
	namespace, name, err := csghub.ParseRepoID(strings.TrimSpace(datasetID))
	if err != nil {
		return "", "", err
	}
	if !safeDatasetIDSegment(namespace) || !safeDatasetIDSegment(name) {
		return "", "", fmt.Errorf("invalid dataset ID %q", datasetID)
	}
	return namespace, name, nil
}

func safeDatasetIDSegment(segment string) bool {
	segment = strings.TrimSpace(segment)
	if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `/\`) {
		return false
	}
	return filepath.Base(segment) == segment && path.Base(segment) == segment
}

func safeDatasetSubPath(subPath string) (string, error) {
	value := filepath.FromSlash(strings.TrimSpace(subPath))
	cleaned := filepath.Clean(value)
	if value == "" || cleaned == "." || filepath.IsAbs(cleaned) || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid dataset path %q", subPath)
	}
	return cleaned, nil
}

func (m *Manager) Client() *csghub.Client {
	return csghub.NewClient(m.cfg.ServerURL, m.cfg.Token)
}
