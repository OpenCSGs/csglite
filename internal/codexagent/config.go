package codexagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
)

const (
	ProviderID = "csghub_lite"
)

// SyncConfig writes Codex configuration to ~/.codex/config.toml
// so subsequent launches use csghub-lite as the model provider.
func SyncConfig(serverURL, apiKey, selectedModelID string, models []api.ModelInfo) error {
	if strings.TrimSpace(selectedModelID) == "" && len(models) > 0 {
		selectedModelID = models[0].Model
	}

	configPath, err := ConfigPath()
	if err != nil {
		return err
	}

	// Read existing config to preserve user settings like trust_level
	existing := make(map[string]tomlValue)
	if data, err := os.ReadFile(configPath); err == nil {
		parseTomlFile(string(data), existing)
	}

	// Update provider settings
	baseURL := strings.TrimRight(serverURL, "/") + "/v1"
	existing["model_provider"] = stringVal(ProviderID)
	existing["model"] = stringVal(strings.TrimSpace(selectedModelID))
	existing["model_providers."+ProviderID+".name"] = stringVal("OpenCSG")
	existing["model_providers."+ProviderID+".base_url"] = stringVal(baseURL)
	existing["model_providers."+ProviderID+".api_key"] = stringVal(strings.TrimSpace(apiKey))
	existing["model_providers."+ProviderID+".supports_websockets"] = boolVal(false)

	// Write model catalog
	modelCatalogPath, err := writeModelCatalog(models)
	if err != nil {
		return err
	}
	existing["model_catalog_json"] = stringVal(modelCatalogPath)

	// Build TOML content, grouping dotted keys into sections
	var buf strings.Builder
	topLevel := make(map[string]tomlValue)
	sections := make(map[string]map[string]tomlValue)

	for key, value := range existing {
		// Check if this is a dotted key like "model_providers.csghub_lite.name"
		parts := strings.Split(key, ".")
		if len(parts) >= 2 && (parts[0] == "model_providers" || parts[0] == "projects") {
			sectionName := parts[0]
			if len(parts) >= 2 {
				// For model_providers.X.name, section is [model_providers.X]
				sectionName = parts[0] + "." + parts[1]
			}
			if sections[sectionName] == nil {
				sections[sectionName] = make(map[string]tomlValue)
			}
			// Key within section is the remaining parts
			sectionKey := strings.Join(parts[2:], ".")
			if len(parts) == 2 {
				// For model_providers.X, the key is just parts[1], but this shouldn't happen
				// Actually for model_provider (single key), it's top-level
				sectionKey = parts[1]
			}
			sections[sectionName][sectionKey] = value
		} else {
			topLevel[key] = value
		}
	}

	// Write top-level keys
	for key, value := range topLevel {
		buf.WriteString(formatTomlKV(key, value))
	}

	// Write sections
	for sectionName, section := range sections {
		buf.WriteString(fmt.Sprintf("[%s]\n", sectionName))
		for key, value := range section {
			buf.WriteString(formatTomlKV(key, value))
		}
	}

	data := []byte(buf.String())
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0o644)
}

type tomlValue struct {
	isBool  bool
	boolVal bool
	isRaw   bool
	rawVal  string
	strVal  string
}

func stringVal(s string) tomlValue {
	return tomlValue{strVal: s}
}

func boolVal(b bool) tomlValue {
	return tomlValue{isBool: true, boolVal: b}
}

func rawVal(s string) tomlValue {
	return tomlValue{isRaw: true, rawVal: s}
}

func formatTomlKV(key string, value tomlValue) string {
	if value.isBool {
		return fmt.Sprintf("%s = %v\n", key, value.boolVal)
	}
	if value.isRaw {
		return fmt.Sprintf("%s = %s\n", key, value.rawVal)
	}
	return fmt.Sprintf("%s = %q\n", key, value.strVal)
}

// ConfigPath returns the path to Codex config.toml.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func writeModelCatalog(models []api.ModelInfo) (string, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	catalogDir := filepath.Join(configDir, "csghub-lite")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		return "", err
	}

	entries := make([]modelCatalogEntry, 0, len(models))
	for _, m := range models {
		modelID := strings.TrimSpace(m.Model)
		if modelID == "" {
			continue
		}
		entries = append(entries, modelCatalogEntry{
			Slug:                       modelID,
			DisplayName:                modelID,
			Description:                "Model served by csghub-lite.",
			SupportedReasoningLevels:   []reasoningEffortPreset{},
			ShellType:                  "shell_command",
			Visibility:                 "list",
			SupportedInAPI:             true,
			Priority:                   len(entries),
			BaseInstructions:           "You are Codex, a coding agent. You and the user share the same workspace and collaborate to achieve the user's goals. Focus on practical, safe, concise help for software tasks.",
			SupportsReasoningSummaries: false,
			SupportVerbosity:           false,
			TruncationPolicy: truncationPolicy{
				Mode:  "bytes",
				Limit: 10000,
			},
			SupportsParallelToolCalls:  false,
			ExperimentalSupportedTools: []string{},
			InputModalities:            []string{"text"},
			ContextWindow:              ContextWindowForModel(m, DefaultLocalContextWindow, DefaultRemoteContextWindowFallback),
		})
	}

	catalog := modelCatalog{Models: entries}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return "", err
	}

	path := filepath.Join(catalogDir, "models.json")
	return path, os.WriteFile(path, data, 0o644)
}

func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

type modelCatalog struct {
	Models []modelCatalogEntry `json:"models"`
}

type modelCatalogEntry struct {
	Slug                       string                  `json:"slug"`
	DisplayName                string                  `json:"display_name"`
	Description                string                  `json:"description"`
	SupportedReasoningLevels   []reasoningEffortPreset `json:"supported_reasoning_levels"`
	ShellType                  string                  `json:"shell_type"`
	Visibility                 string                  `json:"visibility"`
	SupportedInAPI             bool                    `json:"supported_in_api"`
	Priority                   int                     `json:"priority"`
	BaseInstructions           string                  `json:"base_instructions"`
	SupportsReasoningSummaries bool                    `json:"supports_reasoning_summaries"`
	SupportVerbosity           bool                    `json:"support_verbosity"`
	TruncationPolicy           truncationPolicy        `json:"truncation_policy"`
	SupportsParallelToolCalls  bool                    `json:"supports_parallel_tool_calls"`
	ExperimentalSupportedTools []string                `json:"experimental_supported_tools"`
	InputModalities            []string                `json:"input_modalities,omitempty"`
	ContextWindow              int64                   `json:"context_window,omitempty"`
}

type reasoningEffortPreset struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type truncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

// parseTomlFile is a simple TOML parser that extracts key=value pairs.
// Dotted keys like model_providers.X.name are stored with their full path.
func parseTomlFile(content string, kv map[string]tomlValue) {
	var currentSection string
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			for needsMoreTomlValueLines(value) && i+1 < len(lines) {
				i++
				value += "\n" + strings.TrimSpace(lines[i])
			}
			fullKey := key
			if currentSection != "" {
				fullKey = currentSection + "." + key
			}
			// Parse the value
			if value == "true" {
				kv[fullKey] = boolVal(true)
			} else if value == "false" {
				kv[fullKey] = boolVal(false)
			} else {
				kv[fullKey] = parseTomlValue(fullKey, value)
			}
		}
	}
}

func parseTomlValue(key, value string) tomlValue {
	trimmed := strings.TrimSpace(value)
	if isRawTomlValue(trimmed) {
		return rawVal(trimmed)
	}

	if strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"") {
		if unquoted, err := strconv.Unquote(trimmed); err == nil {
			if raw, ok := legacyEncodedRawValue(key, unquoted); ok {
				return rawVal(raw)
			}
			return stringVal(unquoted)
		}
		return stringVal(strings.Trim(trimmed, "\""))
	}
	if strings.HasPrefix(trimmed, "'") && strings.HasSuffix(trimmed, "'") {
		return stringVal(strings.Trim(trimmed, "'"))
	}
	return rawVal(trimmed)
}

func isRawTomlValue(value string) bool {
	return strings.HasPrefix(value, "[") ||
		strings.HasPrefix(value, "{") ||
		strings.HasPrefix(value, `"""`) ||
		strings.HasPrefix(value, `'''`) ||
		strings.Contains(value, "\n") ||
		hasInlineTomlComment(value) ||
		isNumericTomlValue(value) ||
		isUnquotedSpecialTomlValue(value)
}

func isNumericTomlValue(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && ch != '_' && ch != '.' && ch != '-' && ch != '+' {
			return false
		}
	}
	return true
}

func isUnquotedSpecialTomlValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "inf", "+inf", "-inf", "nan", "+nan", "-nan":
		return true
	default:
		return false
	}
}

func hasInlineTomlComment(value string) bool {
	inBasicString := false
	inLiteralString := false
	escaped := false
	for _, ch := range value {
		if escaped {
			escaped = false
			continue
		}
		if inBasicString && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' && !inLiteralString {
			inBasicString = !inBasicString
			continue
		}
		if ch == '\'' && !inBasicString {
			inLiteralString = !inLiteralString
			continue
		}
		if ch == '#' && !inBasicString && !inLiteralString {
			return true
		}
	}
	return false
}

func needsMoreTomlValueLines(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, `"""`) && strings.Count(trimmed, `"""`) < 2 {
		return true
	}
	if strings.HasPrefix(trimmed, `'''`) && strings.Count(trimmed, `'''`) < 2 {
		return true
	}
	return tomlDelimiterBalance(trimmed) > 0
}

func tomlDelimiterBalance(value string) int {
	balance := 0
	inBasicString := false
	inLiteralString := false
	escaped := false
	for _, ch := range value {
		if escaped {
			escaped = false
			continue
		}
		if inBasicString && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' && !inLiteralString {
			inBasicString = !inBasicString
			continue
		}
		if ch == '\'' && !inBasicString {
			inLiteralString = !inLiteralString
			continue
		}
		if ch == '#' && !inBasicString && !inLiteralString {
			break
		}
		if inBasicString || inLiteralString {
			continue
		}
		switch ch {
		case '[', '{':
			balance++
		case ']', '}':
			if balance > 0 {
				balance--
			}
		}
	}
	return balance
}

func legacyEncodedRawValue(key, value string) (string, bool) {
	if !strings.HasPrefix(key, "mcp_servers.") || !strings.HasSuffix(key, ".args") {
		return "", false
	}
	decoded := strings.TrimSpace(value)
	for {
		next := strings.ReplaceAll(decoded, `\\`, `\`)
		if next == decoded {
			break
		}
		decoded = next
	}
	decoded = strings.ReplaceAll(decoded, `\"`, `"`)
	if strings.HasPrefix(decoded, "[") && strings.HasSuffix(decoded, "]") {
		return decoded, true
	}
	return "", false
}
