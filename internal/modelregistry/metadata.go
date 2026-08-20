package modelregistry

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/opencsgs/csglite/internal/csghub"
)

var (
	markdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLinkPattern  = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	markdownStylePattern = regexp.MustCompile("[`*_~]+")
	htmlTagPattern       = regexp.MustCompile(`<[^>]+>`)
	paragraphPattern     = regexp.MustCompile(`\n\s*\n`)
)

func appendRegistryTag(tags []csghub.Tag, seen map[string]struct{}, name, category string) []csghub.Tag {
	name = strings.TrimSpace(name)
	category = strings.TrimSpace(category)
	if name == "" || category == "" {
		return tags
	}
	key := strings.ToLower(category) + "\x00" + strings.ToLower(name)
	if _, ok := seen[key]; ok {
		return tags
	}
	seen[key] = struct{}{}
	return append(tags, csghub.Tag{Name: name, Category: category, ShowName: name})
}

func prefixedTagValue(values []string, prefix string) string {
	prefix = strings.ToLower(prefix)
	for _, value := range values {
		if strings.HasPrefix(strings.ToLower(value), prefix) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return ""
}

func tensorTypes(parameters map[string]int64) string {
	types := make([]string, 0, len(parameters))
	for tensorType, count := range parameters {
		if strings.TrimSpace(tensorType) != "" && count > 0 {
			types = append(types, strings.ToUpper(strings.TrimSpace(tensorType)))
		}
	}
	sort.Strings(types)
	return strings.Join(types, ", ")
}

func summaryFromReadme(readme string) string {
	readme = strings.TrimSpace(readme)
	if strings.HasPrefix(readme, "---") {
		if end := strings.Index(readme[3:], "\n---"); end >= 0 {
			readme = readme[end+7:]
		}
	}
	for _, paragraph := range paragraphPattern.Split(readme, -1) {
		lines := strings.Split(strings.TrimSpace(paragraph), "\n")
		var text []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") ||
				strings.HasPrefix(line, "<") || strings.HasPrefix(line, "![") ||
				strings.HasPrefix(line, "[![") {
				continue
			}
			text = append(text, line)
		}
		candidate := strings.Join(text, " ")
		candidate = markdownImagePattern.ReplaceAllString(candidate, "")
		candidate = markdownLinkPattern.ReplaceAllString(candidate, "$1")
		candidate = htmlTagPattern.ReplaceAllString(candidate, "")
		candidate = markdownStylePattern.ReplaceAllString(candidate, "")
		candidate = strings.Join(strings.Fields(candidate), " ")
		if utf8.RuneCountInString(candidate) < 20 {
			continue
		}
		runes := []rune(candidate)
		if len(runes) > 600 {
			candidate = string(runes[:597]) + "..."
		}
		return candidate
	}
	return ""
}
