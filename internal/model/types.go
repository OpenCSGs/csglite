package model

import (
	"strings"
	"time"
)

type Format string

const (
	FormatGGUF        Format = "gguf"
	FormatSafeTensors Format = "safetensors"
	FormatPyTorch     Format = "pytorch"
	FormatUnknown     Format = "unknown"
)

type LocalModelOrigin string

const (
	LocalModelOriginUpload      LocalModelOrigin = "upload"
	LocalModelOriginMarketplace LocalModelOrigin = "marketplace"
)

type LocalModel struct {
	Namespace         string           `json:"namespace"`
	Name              string           `json:"name"`
	Format            Format           `json:"format"`
	Size              int64            `json:"size"`
	Files             []string         `json:"files"`
	FileEntries       []LocalModelFile `json:"file_entries,omitempty"`
	DownloadedAt      time.Time        `json:"downloaded_at"`
	Origin            LocalModelOrigin `json:"origin,omitempty"`
	Description       string           `json:"description,omitempty"`
	License           string           `json:"license,omitempty"`
	PipelineTag       string           `json:"pipeline_tag,omitempty"`
	ArtifactSource    string           `json:"artifact_source,omitempty"`
	Repository        string           `json:"repository,omitempty"`
	RequestedRevision string           `json:"requested_revision,omitempty"`
	ResolvedRevision  string           `json:"resolved_revision,omitempty"`
}

type LocalModelFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	LFS    bool   `json:"lfs,omitempty"`
}

func (m *LocalModel) FullName() string {
	repository := strings.Trim(strings.TrimSpace(m.Repository), "/")
	if repository == "" {
		repository = strings.TrimSpace(m.Namespace) + "/" + strings.TrimSpace(m.Name)
	}
	source := strings.ToLower(strings.TrimSpace(m.ArtifactSource))
	if source == "" || source == "opencsg" {
		return repository
	}
	return source + "/" + repository
}
