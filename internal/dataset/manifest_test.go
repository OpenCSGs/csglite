package dataset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadManifest(t *testing.T) {
	dir := t.TempDir()

	original := &LocalDataset{
		Namespace:    "OpenCSG",
		Name:         "demo-dataset",
		Size:         1024,
		Files:        []string{"train.jsonl", "meta/info.json"},
		FileEntries:  []LocalDatasetFile{{Path: "train.jsonl", Size: 800}, {Path: "meta/info.json", Size: 224}},
		DownloadedAt: time.Now().Truncate(time.Second),
		Origin:       LocalDatasetOriginMarketplace,
		Description:  "A test dataset",
		License:      "MIT",
	}

	if err := SaveManifest(dir, original); err != nil {
		t.Fatalf("SaveManifest error: %v", err)
	}

	loaded, err := LoadManifest(dir, "OpenCSG", "demo-dataset")
	if err != nil {
		t.Fatalf("LoadManifest error: %v", err)
	}

	if loaded.Namespace != original.Namespace || loaded.Name != original.Name {
		t.Fatalf("loaded = %#v, want namespace/name preserved", loaded)
	}
	if loaded.Origin != original.Origin {
		t.Fatalf("origin = %q, want %q", loaded.Origin, original.Origin)
	}
	if len(loaded.FileEntries) != 2 {
		t.Fatalf("file_entries len = %d, want 2", len(loaded.FileEntries))
	}
	if loaded.FileEntries[0].Path != "meta/info.json" || loaded.FileEntries[1].Path != "train.jsonl" {
		t.Fatalf("file_entries = %#v, want normalized sorted paths", loaded.FileEntries)
	}
	if len(loaded.Files) != 2 || loaded.Files[0] != "meta/info.json" || loaded.Files[1] != "train.jsonl" {
		t.Fatalf("files = %#v, want normalized sorted paths", loaded.Files)
	}
}

func TestLoadManifest_NormalizesFileEntries(t *testing.T) {
	dir := t.TempDir()
	mpath := ManifestPath(dir, "OpenCSG", "normalized")
	if err := os.MkdirAll(filepath.Dir(mpath), 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}

	raw := map[string]any{
		"namespace": "OpenCSG",
		"name":      "normalized",
		"size":      42,
		"files":     []string{},
		"file_entries": []map[string]any{
			{"path": "./train/data.jsonl", "size": 21},
			{"path": "train/data.jsonl", "size": 21},
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(mpath, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	loaded, err := LoadManifest(dir, "OpenCSG", "normalized")
	if err != nil {
		t.Fatalf("LoadManifest error: %v", err)
	}
	if len(loaded.FileEntries) != 1 {
		t.Fatalf("file_entries len = %d, want 1", len(loaded.FileEntries))
	}
	if loaded.FileEntries[0].Path != "train/data.jsonl" {
		t.Fatalf("entry path = %q, want train/data.jsonl", loaded.FileEntries[0].Path)
	}
}
