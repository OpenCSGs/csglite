package registry

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/opencsgs/csglite/internal/csghub"
)

// Source identifies a remote artifact registry independently of artifact kind.
type Source string

const (
	SourceOpenCSG     Source = "opencsg"
	SourceHuggingFace Source = "huggingface"
	SourceModelScope  Source = "modelscope"
)

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

func ResolveRevision(requested, fallback string) string {
	if value := strings.TrimSpace(requested); value != "" {
		return value
	}
	return fallback
}

func ParseRepoID(repoID string) (string, string, error) {
	return csghub.ParseRepoID(strings.TrimSpace(repoID))
}

func EscapeRepoID(repoID string) string {
	namespace, name, _ := ParseRepoID(repoID)
	return url.PathEscape(namespace) + "/" + url.PathEscape(name)
}

func EscapePath(value string) string {
	parts := strings.Split(strings.TrimPrefix(value, "/"), "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func NextLink(value string) string {
	for _, link := range strings.Split(value, ",") {
		sections := strings.Split(link, ";")
		if len(sections) < 2 || !strings.Contains(sections[1], `rel="next"`) {
			continue
		}
		return strings.Trim(strings.TrimSpace(sections[0]), "<>")
	}
	return ""
}
