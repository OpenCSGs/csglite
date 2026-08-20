package dataset

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func SaveManifest(baseDir string, d *LocalDataset) error {
	return SaveManifestInDir(DatasetDir(baseDir, d.Namespace, d.Name), d)
}

// SaveManifestInDir writes a manifest into an already resolved dataset
// directory. The directory is created when necessary.
func SaveManifestInDir(datasetDir string, d *LocalDataset) error {
	normalizeLocalDataset(d)
	mpath := filepath.Join(datasetDir, "manifest.json")
	if err := os.MkdirAll(filepath.Dir(mpath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mpath, data, 0o644)
}

func LoadManifest(baseDir, namespace, name string) (*LocalDataset, error) {
	return LoadManifestInDir(DatasetDir(baseDir, namespace, name))
}

// LoadManifestInDir reads a manifest from an already resolved dataset
// directory.
func LoadManifestInDir(datasetDir string) (*LocalDataset, error) {
	data, err := os.ReadFile(filepath.Join(datasetDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var d LocalDataset
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	normalizeLocalDataset(&d)
	return &d, nil
}
