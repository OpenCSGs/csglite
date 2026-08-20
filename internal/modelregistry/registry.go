package modelregistry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
)

type Source string

const (
	SourceOpenCSG     Source = "opencsg"
	SourceHuggingFace Source = "huggingface"
	SourceModelScope  Source = "modelscope"
)

type ListOptions struct {
	Search         string
	Sort           string
	Page           int
	PerPage        int
	Framework      string
	Task           string
	UpstreamSource string
	ModelParamsMin string
	ModelParamsMax string
}

// Registry is the provider-neutral model artifact contract. csghub.Model and
// csghub.RepoFile remain the canonical wire DTOs for compatibility with the
// existing Marketplace API; adapters must normalize their provider payloads.
type Registry interface {
	Source() Source
	DefaultRevision() string
	ListModels(context.Context, ListOptions) ([]csghub.Model, int, error)
	GetModel(context.Context, string, string) (*csghub.Model, error)
	ListFiles(context.Context, string, string) ([]csghub.RepoFile, string, error)
	ReadFile(context.Context, string, string, string) (string, error)
	DownloadSnapshot(context.Context, string, string, string, []string, csghub.SnapshotProgressFunc) ([]csghub.RepoFile, string, error)
}

func NormalizeSource(value string) (Source, error) {
	switch Source(strings.ToLower(strings.TrimSpace(value))) {
	case "", SourceOpenCSG:
		return SourceOpenCSG, nil
	case SourceHuggingFace:
		return SourceHuggingFace, nil
	case SourceModelScope:
		return SourceModelScope, nil
	default:
		return "", fmt.Errorf("unsupported artifact source %q", value)
	}
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
			firstNonEmpty(firstEnvironmentValue(config.EnvHuggingFaceEndpoint), cfg.HuggingFaceEndpoint, config.DefaultHuggingFaceEndpoint),
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

func ResolveRevision(requested, fallback string) string {
	if value := strings.TrimSpace(requested); value != "" {
		return value
	}
	return fallback
}

func ParseRepoID(repoID string) (string, string, error) {
	return csghub.ParseRepoID(strings.TrimSpace(repoID))
}
