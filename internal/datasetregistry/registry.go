package datasetregistry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	artifactregistry "github.com/opencsgs/csglite/internal/registry"
)

type Source = artifactregistry.Source

const (
	SourceOpenCSG     = artifactregistry.SourceOpenCSG
	SourceHuggingFace = artifactregistry.SourceHuggingFace
	SourceModelScope  = artifactregistry.SourceModelScope
)

type ListOptions struct {
	Search         string
	Sort           string
	Page           int
	PerPage        int
	Task           string
	Language       string
	License        string
	UpstreamSource string
}

// Registry is the provider-neutral remote dataset artifact contract.
// csghub.Dataset and csghub.RepoFile remain the normalized wire DTOs.
type Registry interface {
	Source() Source
	DefaultRevision() string
	ListDatasets(context.Context, ListOptions) ([]csghub.Dataset, int, error)
	GetDataset(context.Context, string, string) (*csghub.Dataset, error)
	ListFiles(context.Context, string, string) ([]csghub.RepoFile, string, error)
	DownloadSnapshot(context.Context, string, string, string, csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error)
}

func NormalizeSource(value string) (Source, error) {
	return artifactregistry.NormalizeSource(value)
}

func New(cfg *config.Config, source Source) (Registry, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	switch source {
	case SourceOpenCSG:
		return NewOpenCSG(cfg.ServerURL, cfg.Token), nil
	case SourceHuggingFace:
		return NewHuggingFace(
			config.ResolveHuggingFaceEndpoint(cfg.HuggingFaceEndpoint),
			firstNonEmpty(firstEnvironmentValue(config.EnvHuggingFaceToken, config.EnvHuggingFaceHubToken), cfg.HuggingFaceToken),
		), nil
	case SourceModelScope:
		return NewModelScope(
			firstNonEmpty(firstEnvironmentValue(config.EnvModelScopeEndpoint), cfg.ModelScopeEndpoint, config.DefaultModelScopeEndpoint),
			firstNonEmpty(firstEnvironmentValue(config.EnvModelScopeToken, config.EnvModelScopeAPIKey), cfg.ModelScopeToken),
		), nil
	default:
		return nil, fmt.Errorf("unsupported artifact source %q", source)
	}
}

func firstEnvironmentValue(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
