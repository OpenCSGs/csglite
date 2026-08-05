package codexagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestMatchesManagedProviderConfigDataIgnoresUnrelatedCodexSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	catalogPath := filepath.Join(home, ".codex", "csghub-lite", "models.json")
	data := []byte(fmt.Sprintf(`
model_provider = "csghub_lite"
model = "user-selected-model"
model_catalog_json = %q
marketplaces.openai-bundled.last_updated = "2026-08-05T00:00:00Z"

[model_providers.csghub_lite]
name = "OpenCSG"
base_url = "http://127.0.0.1:11435/v1"
api_key = "test-token"
supports_websockets = false

[projects."/tmp/example"]
trust_level = "trusted"
`, catalogPath))

	if !MatchesManagedProviderConfigData(data, "http://127.0.0.1:11435", "test-token") {
		t.Fatal("unrelated Codex settings caused managed provider mismatch")
	}
	tampered := strings.Replace(string(data), "http://127.0.0.1:11435/v1", "https://example.com/v1", 1)
	if MatchesManagedProviderConfigData([]byte(tampered), "http://127.0.0.1:11435", "test-token") {
		t.Fatal("changed provider base URL was accepted")
	}
}

func TestRestoreNativeConfigDataPreservesUnrelatedChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, ".codex", "config.toml")
	original := []byte(`
model_provider = "openai"
model = "native-model"

[model_providers.openai]
name = "OpenAI"
base_url = "https://api.openai.com/v1"
`)
	current := []byte(`
model_provider = "csghub_lite"
model = "managed-model"
model_catalog_json = "/tmp/csghub-models.json"
marketplaces.openai-bundled.last_updated = "2026-08-05T00:00:00Z"

[model_providers.csghub_lite]
name = "OpenCSG"
base_url = "http://127.0.0.1:11435/v1"
api_key = "test-token"
supports_websockets = false

[projects."/tmp/new-project"]
trust_level = "trusted"
`)

	if err := RestoreNativeConfigData(current, original, true); err != nil {
		t.Fatalf("RestoreNativeConfigData() error: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]tomlValue{}
	parseTomlFile(string(data), values)
	if got := tomlString(values["model_provider"]); got != "openai" {
		t.Fatalf("model_provider = %q, want openai", got)
	}
	if got := tomlString(values["model"]); got != "native-model" {
		t.Fatalf("model = %q, want native-model", got)
	}
	if _, ok := values["model_catalog_json"]; ok {
		t.Fatal("managed model_catalog_json was not removed")
	}
	for key := range values {
		if strings.HasPrefix(key, "model_providers.csghub_lite.") {
			t.Fatalf("managed provider field %q was not removed", key)
		}
	}
	if got := tomlString(values["marketplaces.openai-bundled.last_updated"]); got != "2026-08-05T00:00:00Z" {
		t.Fatalf("marketplace update = %q, want preserved", got)
	}
	if got := tomlString(values[`projects."/tmp/new-project".trust_level`]); got != "trusted" {
		t.Fatalf("project trust = %q, want preserved", got)
	}
}

func TestParseTomlFilePreservesMCPArgsArray(t *testing.T) {
	values := map[string]tomlValue{}
	parseTomlFile(`
[mcp_servers.remotion-documentation]
command = "npx"
args = ["@remotion/mcp@latest"]
`, values)

	args, ok := values["mcp_servers.remotion-documentation.args"]
	if !ok {
		t.Fatal("missing mcp args")
	}
	if !args.isRaw {
		t.Fatalf("mcp args parsed as quoted string: %#v", args)
	}
	if got := strings.TrimSpace(formatTomlKV("args", args)); got != `args = ["@remotion/mcp@latest"]` {
		t.Fatalf("formatted args = %q", got)
	}
}

func TestParseTomlFileRepairsLegacyEncodedMCPArgsArray(t *testing.T) {
	values := map[string]tomlValue{}
	parseTomlFile(`mcp_servers.remotion-documentation.args = "[\\\"@remotion/mcp@latest\\\"]"`, values)

	args, ok := values["mcp_servers.remotion-documentation.args"]
	if !ok {
		t.Fatal("missing mcp args")
	}
	if !args.isRaw {
		t.Fatalf("legacy mcp args parsed as quoted string: %#v", args)
	}
	if got := strings.TrimSpace(formatTomlKV("args", args)); got != `args = ["@remotion/mcp@latest"]` {
		t.Fatalf("formatted repaired args = %q", got)
	}
}

func TestParseTomlFilePreservesMultilineMCPArgsArray(t *testing.T) {
	values := map[string]tomlValue{}
	parseTomlFile(`
[mcp_servers.example]
args = [
  "first",
  "second",
]
`, values)

	args, ok := values["mcp_servers.example.args"]
	if !ok {
		t.Fatal("missing mcp args")
	}
	if !args.isRaw {
		t.Fatalf("multiline args parsed as quoted string: %#v", args)
	}
	formatted := formatTomlKV("args", args)
	for _, want := range []string{`args = [`, `"first"`, `"second"`, `]`} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted multiline args missing %q:\n%s", want, formatted)
		}
	}
}

func TestParseTomlFilePreservesRawScalarAndInlineTableValues(t *testing.T) {
	values := map[string]tomlValue{}
	parseTomlFile(`
retry_delay = 1.5
release_date = 2026-05-10T11:00:00Z
headers = { Authorization = "Bearer token", Accept = "application/json" }
quoted = "value"
`, values)

	for _, key := range []string{"retry_delay", "release_date", "headers"} {
		value := values[key]
		if !value.isRaw {
			t.Fatalf("%s parsed as non-raw value: %#v", key, value)
		}
	}
	if values["quoted"].isRaw || values["quoted"].strVal != "value" {
		t.Fatalf("quoted string parsed incorrectly: %#v", values["quoted"])
	}
}

func TestLookupContextWindowUsesContainingModelID(t *testing.T) {
	got, ok := LookupContextWindow("provider/zai-org/glm-5.1-latest")
	if !ok {
		t.Fatal("LookupContextWindow did not match containing model ID")
	}
	if got != 200000 {
		t.Fatalf("context window = %d, want 200000", got)
	}
}

func TestWriteModelCatalogUsesContextWindowPresets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := writeModelCatalog([]api.ModelInfo{
		{Model: "provider/zai-org/glm-5.1-latest", Source: "provider:test"},
		{Model: "unknown-remote-model", Source: "cloud"},
	})
	if err != nil {
		t.Fatalf("writeModelCatalog returned error: %v", err)
	}
	if filepath.Dir(path) == "" {
		t.Fatalf("catalog path is empty: %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}

	var payload struct {
		Models []struct {
			Slug          string `json:"slug"`
			ContextWindow int64  `json:"context_window"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(payload.Models) != 2 {
		t.Fatalf("model count = %d, want 2", len(payload.Models))
	}
	if payload.Models[0].ContextWindow != 200000 {
		t.Fatalf("glm context_window = %d, want 200000", payload.Models[0].ContextWindow)
	}
	if payload.Models[1].ContextWindow != 200000 {
		t.Fatalf("remote default context_window = %d, want 200000", payload.Models[1].ContextWindow)
	}
}
