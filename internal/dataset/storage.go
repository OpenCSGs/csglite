package dataset

import (
	"os"
	"path/filepath"
	"strings"
)

const registryDatasetsDir = ".registries"

func DatasetDir(baseDir, namespace, name string) string {
	return filepath.Join(baseDir, namespace, name)
}

func RegistryDatasetDir(baseDir, artifactSource, namespace, name string) string {
	source := strings.ToLower(strings.TrimSpace(artifactSource))
	if source == "" || source == "opencsg" {
		return DatasetDir(baseDir, namespace, name)
	}
	return filepath.Join(baseDir, registryDatasetsDir, source, namespace, name)
}

func ManifestPath(baseDir, namespace, name string) string {
	return filepath.Join(DatasetDir(baseDir, namespace, name), "manifest.json")
}

func EnsureDatasetDir(baseDir, namespace, name string) error {
	return os.MkdirAll(DatasetDir(baseDir, namespace, name), 0o755)
}

func RemoveDatasetDir(baseDir, namespace, name string) error {
	dir := DatasetDir(baseDir, namespace, name)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	nsDir := filepath.Join(baseDir, namespace)
	entries, err := os.ReadDir(nsDir)
	if err == nil && len(entries) == 0 {
		os.Remove(nsDir)
	}
	return nil
}

func RemoveRegistryDatasetDir(baseDir, artifactSource, namespace, name string) error {
	source := strings.ToLower(strings.TrimSpace(artifactSource))
	if source == "" || source == "opencsg" {
		return RemoveDatasetDir(baseDir, namespace, name)
	}
	dir := RegistryDatasetDir(baseDir, source, namespace, name)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, parent := range []string{
		filepath.Join(baseDir, registryDatasetsDir, source, namespace),
		filepath.Join(baseDir, registryDatasetsDir, source),
		filepath.Join(baseDir, registryDatasetsDir),
	} {
		entries, err := os.ReadDir(parent)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(parent)
		}
	}
	return nil
}

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

func ListDatasetsInNamespace(baseDir, namespace string) ([]string, error) {
	nsDir := filepath.Join(baseDir, namespace)
	entries, err := os.ReadDir(nsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var datasets []string
	for _, e := range entries {
		if e.IsDir() {
			datasets = append(datasets, e.Name())
		}
	}
	return datasets, nil
}
