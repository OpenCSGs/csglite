package kimiagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/safefile"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	ProviderID = "csghub_lite"

	// defaultMaxContextSize is used when the server does not report a
	// context window for a model. kimi-code requires a positive value.
	defaultMaxContextSize = 131072
)

// SyncConfig writes Kimi Code CLI configuration to ~/.kimi-code/config.toml so
// subsequent launches use csghub-lite as the model provider. Kimi Code reads
// config.toml for providers and models; the KIMI_MODEL_* environment variables
// are an alternative, but config.toml is the durable source of truth.
func SyncConfig(serverURL, apiKey, selectedModelID string, models []api.ModelInfo) error {
	if strings.TrimSpace(selectedModelID) == "" && len(models) > 0 {
		selectedModelID = models[0].Model
	}

	configPath, err := ConfigPath()
	if err != nil {
		return err
	}

	// Read existing config to preserve user settings.
	existing := make(map[string]tomlValue)
	if data, err := os.ReadFile(configPath); err == nil {
		parseTomlFile(string(data), existing)
	}

	baseURL := strings.TrimRight(serverURL, "/") + "/v1"
	selectedAlias := modelAlias(strings.TrimSpace(selectedModelID))
	existing["default_model"] = stringVal(selectedAlias)
	existing["providers."+ProviderID+".type"] = stringVal("openai")
	existing["providers."+ProviderID+".base_url"] = stringVal(baseURL)
	existing["providers."+ProviderID+".api_key"] = stringVal(strings.TrimSpace(apiKey))

	// Replace the managed model entries, preserving any user-defined models.
	for key := range existing {
		if strings.HasPrefix(key, "models.") && strings.HasSuffix(key, ".provider") &&
			tomlString(existing[key]) == ProviderID {
			modelKey := strings.TrimSuffix(key, ".provider")
			delete(existing, modelKey)
			delete(existing, modelKey+".model")
			delete(existing, modelKey+".max_context_size")
			delete(existing, modelKey+".max_output_size")
		}
	}
	for _, item := range models {
		modelID := strings.TrimSpace(item.Model)
		if modelID == "" {
			continue
		}
		alias := modelAlias(modelID)
		existing["models."+alias+".provider"] = stringVal(ProviderID)
		existing["models."+alias+".model"] = stringVal(modelID)
		// kimi-code requires a positive max_context_size for every model;
		// fall back to a sane default when the server doesn't report one.
		ctxSize := item.ContextWindow
		if ctxSize <= 0 {
			ctxSize = defaultMaxContextSize
		}
		existing["models."+alias+".max_context_size"] = rawVal(fmt.Sprintf("%d", ctxSize))
	}

	return writeConfigValues(configPath, existing)
}

// ModelAlias sanitizes a model ID into a valid TOML table key for kimi-code's
// [models.*] entries. kimi-code looks up models by this alias, so default_model
// and the --model flag must use the alias, not the raw model ID.
func ModelAlias(modelID string) string {
	alias := strings.NewReplacer("/", "_", ".", "_", ":", "_", " ", "_").Replace(modelID)
	return alias
}

// modelAlias is an alias for ModelAlias for internal use.
func modelAlias(modelID string) string {
	return ModelAlias(modelID)
}

// ConfigPath returns the path to Kimi Code config.toml.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kimi-code", "config.toml"), nil
}

// ConfigDir returns the Kimi Code data directory.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kimi-code"), nil
}

func tomlString(value tomlValue) string {
	if value.isBool || value.isRaw {
		return ""
	}
	return strings.TrimSpace(value.strVal)
}

func writeConfigValues(configPath string, existing map[string]tomlValue) error {
	// Build TOML content, grouping dotted keys into sections.
	var buf strings.Builder
	topLevel := make(map[string]tomlValue)
	sections := make(map[string]map[string]tomlValue)

	for key, value := range existing {
		parts := strings.Split(key, ".")
		if len(parts) >= 2 && (parts[0] == "providers" || parts[0] == "models") {
			sectionName := parts[0] + "." + parts[1]
			if sections[sectionName] == nil {
				sections[sectionName] = make(map[string]tomlValue)
			}
			sectionKey := strings.Join(parts[2:], ".")
			if sectionKey == "" {
				sectionKey = parts[1]
			}
			sections[sectionName][sectionKey] = value
		} else {
			topLevel[key] = value
		}
	}

	for key, value := range topLevel {
		buf.WriteString(formatTomlKV(key, value))
	}

	for sectionName, section := range sections {
		buf.WriteString(fmt.Sprintf("[%s]\n", sectionName))
		for key, value := range section {
			buf.WriteString(formatTomlKV(key, value))
		}
		buf.WriteString("\n")
	}

	data := []byte(buf.String())
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	return safefile.Write(configPath, data, 0o644)
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

// parseTomlFile is a simple TOML parser that extracts key=value pairs.
// Dotted keys like providers.csghub_lite.base_url are stored with their full
// path. This mirrors the lightweight parser used by codexagent.
func parseTomlFile(content string, kv map[string]tomlValue) {
	var currentSection string
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
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
			if value == "true" {
				kv[fullKey] = boolVal(true)
			} else if value == "false" {
				kv[fullKey] = boolVal(false)
			} else {
				kv[fullKey] = parseTomlValue(value)
			}
		}
	}
}

func parseTomlValue(value string) tomlValue {
	trimmed := strings.TrimSpace(value)
	if isRawTomlValue(trimmed) {
		return rawVal(trimmed)
	}
	if strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"") {
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
		isNumericTomlValue(value)
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
