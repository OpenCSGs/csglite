package dataset

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LocalDatasetOrigin string

const (
	LocalDatasetOriginUpload      LocalDatasetOrigin = "upload"
	LocalDatasetOriginMarketplace LocalDatasetOrigin = "marketplace"
	LocalDatasetOriginExport      LocalDatasetOrigin = "export"
)

type LocalDataset struct {
	Namespace         string             `json:"namespace"`
	Name              string             `json:"name"`
	Size              int64              `json:"size"`
	Files             []string           `json:"files"`
	FileEntries       []LocalDatasetFile `json:"file_entries,omitempty"`
	DownloadedAt      time.Time          `json:"downloaded_at"`
	Origin            LocalDatasetOrigin `json:"origin,omitempty"`
	Description       string             `json:"description,omitempty"`
	License           string             `json:"license,omitempty"`
	ArtifactSource    string             `json:"artifact_source,omitempty"`
	Repository        string             `json:"repository,omitempty"`
	RequestedRevision string             `json:"requested_revision,omitempty"`
	ResolvedRevision  string             `json:"resolved_revision,omitempty"`
}

type LocalDatasetFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	LFS    bool   `json:"lfs,omitempty"`
}

func (d *LocalDataset) FullName() string {
	repository := strings.Trim(strings.TrimSpace(d.Repository), "/")
	if repository == "" {
		repository = strings.TrimSpace(d.Namespace) + "/" + strings.TrimSpace(d.Name)
	}
	source := strings.ToLower(strings.TrimSpace(d.ArtifactSource))
	if source == "" || source == "opencsg" {
		return repository
	}
	return source + "/" + repository
}

type FileEntry struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	IsDir      bool      `json:"is_dir"`
	ModifiedAt time.Time `json:"modified_at"`
}

func dirSize(path string) int64 {
	var size int64
	if err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	}); err != nil {
		return 0
	}
	return size
}
