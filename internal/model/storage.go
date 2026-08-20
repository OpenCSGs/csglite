package model

import (
	"os"
	"path/filepath"
	"strings"
)

const registryModelsDir = ".registries"

// ModelDir returns the directory for a specific model.
func ModelDir(baseDir, namespace, name string) string {
	return filepath.Join(baseDir, namespace, name)
}

// RegistryModelDir keeps historical OpenCSG/import paths stable while
// namespacing external registries so identical repository IDs can coexist.
func RegistryModelDir(baseDir, artifactSource, namespace, name string) string {
	source := strings.ToLower(strings.TrimSpace(artifactSource))
	if source == "" || source == "opencsg" {
		return ModelDir(baseDir, namespace, name)
	}
	return filepath.Join(baseDir, registryModelsDir, source, namespace, name)
}

// ManifestPath returns the path to the manifest file for a model.
func ManifestPath(baseDir, namespace, name string) string {
	return filepath.Join(ModelDir(baseDir, namespace, name), "manifest.json")
}

// EnsureModelDir creates the directory for a model if it doesn't exist.
func EnsureModelDir(baseDir, namespace, name string) error {
	return os.MkdirAll(ModelDir(baseDir, namespace, name), 0o755)
}

// RemoveModelDir removes the directory for a model.
func RemoveModelDir(baseDir, namespace, name string) error {
	dir := ModelDir(baseDir, namespace, name)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// Clean up empty parent namespace directory
	nsDir := filepath.Join(baseDir, namespace)
	entries, err := os.ReadDir(nsDir)
	if err == nil && len(entries) == 0 {
		os.Remove(nsDir)
	}
	return nil
}

func RemoveRegistryModelDir(baseDir, artifactSource, namespace, name string) error {
	source := strings.ToLower(strings.TrimSpace(artifactSource))
	if source == "" || source == "opencsg" {
		return RemoveModelDir(baseDir, namespace, name)
	}
	dir := RegistryModelDir(baseDir, source, namespace, name)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, parent := range []string{
		filepath.Join(baseDir, registryModelsDir, source, namespace),
		filepath.Join(baseDir, registryModelsDir, source),
		filepath.Join(baseDir, registryModelsDir),
	} {
		entries, err := os.ReadDir(parent)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(parent)
		}
	}
	return nil
}

// ListNamespaces returns all namespace directories under the model base dir.
func ListNamespaces(baseDir string) ([]string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var namespaces []string
	for _, e := range entries {
		if e.IsDir() {
			namespaces = append(namespaces, e.Name())
		}
	}
	return namespaces, nil
}

// ListModelsInNamespace returns all model directories under a namespace.
func ListModelsInNamespace(baseDir, namespace string) ([]string, error) {
	nsDir := filepath.Join(baseDir, namespace)
	entries, err := os.ReadDir(nsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var models []string
	for _, e := range entries {
		if e.IsDir() {
			models = append(models, e.Name())
		}
	}
	return models, nil
}
