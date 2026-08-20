package datasetregistry

import (
	"strings"

	"github.com/opencsgs/csglite/internal/csghub"
)

func appendTag(tags []csghub.Tag, seen map[string]struct{}, name, category string) []csghub.Tag {
	name = strings.TrimSpace(name)
	category = strings.TrimSpace(category)
	if name == "" || category == "" {
		return tags
	}
	key := strings.ToLower(category) + "\x00" + strings.ToLower(name)
	if _, exists := seen[key]; exists {
		return tags
	}
	seen[key] = struct{}{}
	return append(tags, csghub.Tag{Name: name, Category: category, ShowName: name})
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return []string{typed}
		}
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func prefixedValue(values []string, prefix string) string {
	for _, value := range values {
		if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return ""
}

func gated(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.TrimSpace(typed) != "" && !strings.EqualFold(strings.TrimSpace(typed), "false")
	default:
		return false
	}
}
