package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestResolveLaunchModelUsesServerDefault(t *testing.T) {
	server := launchModelTestServer([]api.ModelInfo{
		{Model: "Qwen/Qwen3.5-2B", Source: "local"},
		{Model: "Qwen/Qwen2.5-Coder-1.5B", Source: "local"},
	})
	defer server.Close()

	got, err := resolveLaunchModel(server.URL, "Qwen/Qwen2.5-Coder-1.5B", "", true, false)
	if err != nil {
		t.Fatalf("resolveLaunchModel returned error: %v", err)
	}
	if got != "Qwen/Qwen2.5-Coder-1.5B" {
		t.Fatalf("resolveLaunchModel chose %q, want server default coder model", got)
	}
}

func TestResolveLaunchModelAcceptsCloudModelID(t *testing.T) {
	server := launchModelTestServer([]api.ModelInfo{
		{Model: "Qwen/Qwen3.5-2B", Source: "local"},
		{Model: "minimax-m2.5", DisplayName: "MiniMax M2.5", Source: "cloud"},
	})
	defer server.Close()

	got, err := resolveLaunchModel(server.URL, "Qwen/Qwen3.5-2B", "minimax-m2.5", true, true)
	if err != nil {
		t.Fatalf("resolveLaunchModel returned error: %v", err)
	}
	if got != "minimax-m2.5" {
		t.Fatalf("resolveLaunchModel chose %q, want requested cloud model", got)
	}
}

func TestResolveLaunchModelMissingCloudTokenShowsSettingsHint(t *testing.T) {
	server := launchModelTestServer([]api.ModelInfo{
		{Model: "Qwen/Qwen3.5-2B", Source: "local"},
	})
	defer server.Close()

	_, err := resolveLaunchModel(server.URL, "Qwen/Qwen3.5-2B", "afrideva/Qwen2-0.5B-Instruct-GGUF:fh23aijhzx8g", true, false)
	if err == nil {
		t.Fatal("resolveLaunchModel returned nil error, want settings hint")
	}
	if got := err.Error(); !strings.Contains(got, "open csghub-lite Settings and save an Access Token first") {
		t.Fatalf("error = %q, want settings hint for missing cloud token", got)
	}
}

func TestNormalizeLaunchModelChoicesUsesLabelField(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "Qwen/Qwen3-0.6B", Label: "Qwen/Qwen3-0.6B", Source: "local"},
		{Model: "deepseek-v3.2", Label: "deepseek-v3.2(infini-ai)", Source: "cloud"},
		{Model: "kimi-k2.6", Label: "kimi-k2.6 [kimi]", Source: "provider:abc123"},
	}
	choices := normalizeLaunchModelChoices(models)
	if len(choices) != 3 {
		t.Fatalf("choices count = %d, want 3", len(choices))
	}

	tests := []struct {
		id    string
		label string
	}{
		{"Qwen/Qwen3-0.6B", "Qwen/Qwen3-0.6B (local)"},
		{"deepseek-v3.2", "deepseek-v3.2(infini-ai) (cloud)"},
		{"kimi-k2.6", "kimi-k2.6 [kimi]"},
	}
	for i, tt := range tests {
		if choices[i].ID != tt.id {
			t.Fatalf("choices[%d].ID = %q, want %q", i, choices[i].ID, tt.id)
		}
		if choices[i].Label != tt.label {
			t.Fatalf("choices[%d].Label = %q, want %q", i, choices[i].Label, tt.label)
		}
	}
}

func TestNormalizeLaunchModelChoicesFallsBackToDisplayName(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "some-model", DisplayName: "Some Model", Source: "cloud"},
	}
	choices := normalizeLaunchModelChoices(models)
	if len(choices) != 1 {
		t.Fatalf("choices count = %d, want 1", len(choices))
	}
	if choices[0].Label != "Some Model (cloud)" {
		t.Fatalf("Label = %q, want DisplayName fallback with cloud tag", choices[0].Label)
	}
}

func TestNormalizeLaunchModelChoicesFallsBackToModelID(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "bare-model", Source: "local"},
	}
	choices := normalizeLaunchModelChoices(models)
	if len(choices) != 1 {
		t.Fatalf("choices count = %d, want 1", len(choices))
	}
	if choices[0].Label != "bare-model (local)" {
		t.Fatalf("Label = %q, want model ID fallback with local tag", choices[0].Label)
	}
}

func TestNormalizeLaunchModelChoicesProviderNoSourceSuffix(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "gpt-4o", Label: "gpt-4o [OpenAI]", Source: "provider:abc"},
	}
	choices := normalizeLaunchModelChoices(models)
	if len(choices) != 1 {
		t.Fatalf("choices count = %d, want 1", len(choices))
	}
	if choices[0].Label != "gpt-4o [OpenAI]" {
		t.Fatalf("Label = %q, want no source suffix for provider models", choices[0].Label)
	}
}

func TestNormalizeLaunchModelChoicesExcludesUnavailableAIAppCloudModels(t *testing.T) {
	models := []api.ModelInfo{
		{Model: "opus4.7", Source: "cloud", Format: "cloud"},
		{Model: "glm-5", Source: "cloud", Format: "cloud"},
		{Model: "opus4.7", Source: "local"},
	}
	choices := normalizeLaunchModelChoices(models)
	if len(choices) != 2 {
		t.Fatalf("choices count = %d, want 2", len(choices))
	}
	if choices[0].ID != "glm-5" || choices[1].ID != "opus4.7" {
		t.Fatalf("choices = %#v, want cloud glm-5 and local opus4.7", choices)
	}
}

func TestPrependArgsIfMissing(t *testing.T) {
	args := prependArgsIfMissing([]string{"run", "hello"}, []string{"--model", "demo"}, "--model", "-m")
	if len(args) != 4 || args[0] != "--model" || args[1] != "demo" {
		t.Fatalf("prependArgsIfMissing prepended unexpected args: %#v", args)
	}

	unchanged := prependArgsIfMissing([]string{"--model", "other", "run"}, []string{"--model", "demo"}, "--model", "-m")
	if len(unchanged) != 3 || unchanged[0] != "--model" || unchanged[1] != "other" {
		t.Fatalf("prependArgsIfMissing should not duplicate model flags: %#v", unchanged)
	}
}

func TestLaunchUserArgsPassesUserRequestedClaudeDangerouslySkipPermissions(t *testing.T) {
	args, err := launchUserArgs(launchTarget{AppID: "claude-code"}, nil, true)
	if err != nil {
		t.Fatalf("launchUserArgs returned error: %v", err)
	}
	if !hasAnyFlag(args, "--dangerously-skip-permissions") {
		t.Fatalf("launchUserArgs missing Claude Code permissions flag: %#v", args)
	}
}

func TestLaunchUserArgsDoesNotAddClaudeDangerouslySkipPermissionsByDefault(t *testing.T) {
	args, err := launchUserArgs(launchTarget{AppID: "claude-code"}, nil, false)
	if err != nil {
		t.Fatalf("launchUserArgs returned error: %v", err)
	}
	if hasAnyFlag(args, "--dangerously-skip-permissions") {
		t.Fatalf("launchUserArgs added Claude Code permissions flag by default: %#v", args)
	}
}

func TestLaunchCommandAcceptsClaudeDangerouslySkipPermissionsFlag(t *testing.T) {
	cmd := newLaunchCmd()
	if err := cmd.ParseFlags([]string{"claude", "--model", "glm-5(infini-ai)", "--dangerously-skip-permissions"}); err != nil {
		t.Fatalf("ParseFlags returned error: %v", err)
	}
	requested, err := userRequestedClaudeSkipPermissions(cmd)
	if err != nil {
		t.Fatalf("userRequestedClaudeSkipPermissions returned error: %v", err)
	}
	if !requested {
		t.Fatal("userRequestedClaudeSkipPermissions = false, want true")
	}
}

func TestLaunchUserArgsDoesNotDuplicateClaudeDangerouslySkipPermissions(t *testing.T) {
	args, err := launchUserArgs(launchTarget{AppID: "claude-code"}, []string{"--dangerously-skip-permissions"}, true)
	if err != nil {
		t.Fatalf("launchUserArgs returned error: %v", err)
	}
	if len(args) != 1 || args[0] != "--dangerously-skip-permissions" {
		t.Fatalf("launchUserArgs duplicated Claude Code permissions flag: %#v", args)
	}
}

func TestLaunchUserArgsRejectsClaudeDangerouslySkipPermissionsForOtherApps(t *testing.T) {
	_, err := launchUserArgs(launchTarget{AppID: "codex", DisplayName: "Codex"}, nil, true)
	if err == nil {
		t.Fatal("launchUserArgs returned nil error, want unsupported app error")
	}
}

func TestWriteOpenCodeLaunchConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := writeOpenCodeLaunchConfig("http://127.0.0.1:11435", "Qwen/Qwen3.5-2B")
	if err != nil {
		t.Fatalf("writeOpenCodeLaunchConfig returned error: %v", err)
	}
	if filepath.Base(path) != "opencode.json" {
		t.Fatalf("unexpected config filename: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	if payload["model"] != "csghub-lite/Qwen/Qwen3.5-2B" {
		t.Fatalf("unexpected model field: %#v", payload["model"])
	}

	providers, ok := payload["provider"].(map[string]interface{})
	if !ok {
		t.Fatalf("provider field missing or invalid: %#v", payload["provider"])
	}
	provider, ok := providers["csghub-lite"].(map[string]interface{})
	if !ok {
		t.Fatalf("csghub-lite provider missing: %#v", providers)
	}
	options, ok := provider["options"].(map[string]interface{})
	if !ok || options["baseURL"] != "http://127.0.0.1:11435/v1" {
		t.Fatalf("unexpected provider options: %#v", provider["options"])
	}
}

func TestPrependDefaultCodexProviderConfig(t *testing.T) {
	args := prependDefaultCodexProviderConfig([]string{"--model", "Qwen/Qwen3.5-2B"}, "http://127.0.0.1:11435")

	for _, want := range []string{
		`model_provider="csghub_lite"`,
		`model_providers.csghub_lite.name="OpenCSG"`,
		`model_providers.csghub_lite.base_url="http://127.0.0.1:11435/v1"`,
		`model_providers.csghub_lite.supports_websockets=false`,
	} {
		if !hasConfigOverride(args, want) {
			t.Fatalf("missing Codex config override %q in args %#v", want, args)
		}
	}
	if hasConfigPrefix(args, "openai_base_url=") {
		t.Fatalf("args = %#v, want custom provider config instead of openai_base_url", args)
	}
}

func TestPrepareCodexLaunchIncludesModelCatalog(t *testing.T) {
	server := launchModelTestServer([]api.ModelInfo{
		{Model: "Qwen/Qwen3.5-2B", DisplayName: "Qwen 3.5 2B", Source: "local"},
		{Model: "afrideva/Qwen2-0.5B-Instruct-GGUF:fh23aijhzx8g", DisplayName: "Qwen2-0.5B-Instruct-GGUF", Source: "cloud"},
	})
	defer server.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "codex")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "codex.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	prepared, err := prepareCodexLaunch(launchTarget{
		AppID:       "codex",
		DisplayName: "Codex",
		Binaries:    []string{"codex"},
	}, server.URL, "Qwen/Qwen3.5-2B", nil)
	if err != nil {
		t.Fatalf("prepareCodexLaunch returned error: %v", err)
	}

	if len(prepared.Args) != 2 || prepared.Args[0] != "--model" || prepared.Args[1] != "Qwen/Qwen3.5-2B" {
		t.Fatalf("prepared args = %#v, want only --model override", prepared.Args)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	configText := string(data)
	for _, want := range []string{
		`model_provider = "csghub_lite"`,
		`[model_providers.csghub_lite]`,
		`name = "OpenCSG"`,
		`base_url = ` + strconv.Quote(server.URL+"/v1"),
		`supports_websockets = false`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("codex config missing %q:\n%s", want, configText)
		}
	}
	catalogValue := configValueFromConfig(configText, "model_catalog_json")
	if catalogValue == "" {
		t.Fatalf("missing model_catalog_json config:\n%s", configText)
	}
	catalogPath, err := strconv.Unquote(catalogValue)
	if err != nil {
		t.Fatalf("unquote model_catalog_json %q: %v", catalogValue, err)
	}
	data, err = os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read model catalog: %v", err)
	}
	var payload struct {
		Models []struct {
			Slug          string   `json:"slug"`
			DisplayName   string   `json:"display_name"`
			Description   string   `json:"description"`
			Visibility    string   `json:"visibility"`
			ShellType     string   `json:"shell_type"`
			InputModes    []string `json:"input_modalities"`
			ContextWindow int64    `json:"context_window"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode model catalog: %v", err)
	}
	if len(payload.Models) != 2 {
		t.Fatalf("model catalog count = %d, want 2", len(payload.Models))
	}
	if payload.Models[0].Slug != "Qwen/Qwen3.5-2B" {
		t.Fatalf("unexpected first model entry: %#v", payload.Models[0])
	}
	if payload.Models[1].Slug != "afrideva/Qwen2-0.5B-Instruct-GGUF:fh23aijhzx8g" {
		t.Fatalf("unexpected second model entry: %#v", payload.Models[1])
	}
	if payload.Models[0].Visibility != "list" || payload.Models[1].Visibility != "list" {
		t.Fatalf("unexpected model visibility: %#v", payload.Models)
	}
	if payload.Models[0].ShellType != "shell_command" || payload.Models[1].ShellType != "shell_command" {
		t.Fatalf("unexpected model shell type: %#v", payload.Models)
	}
	if !containsAll(payload.Models[0].InputModes, []string{"text"}) || !containsAll(payload.Models[1].InputModes, []string{"text"}) {
		t.Fatalf("unexpected input modalities: %#v", payload.Models)
	}
	if payload.Models[1].ContextWindow != 200000 {
		t.Fatalf("cloud context_window = %d, want remote default 200000", payload.Models[1].ContextWindow)
	}
}

func TestPreparePiLaunchSyncsConfigAndSetsProviderArgs(t *testing.T) {
	server := launchModelTestServer([]api.ModelInfo{
		{Model: "Qwen/Qwen3.5-2B", Source: "local"},
		{Model: "minimax-m2.5", Source: "cloud"},
	})
	defer server.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "pi")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "pi.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	prepared, err := preparePiLaunch(launchTarget{
		AppID:       "pi",
		DisplayName: "Pi",
		Binaries:    []string{"pi"},
	}, server.URL, "minimax-m2.5", nil)
	if err != nil {
		t.Fatalf("preparePiLaunch returned error: %v", err)
	}
	if got := argValue(prepared.Args, "--provider"); got != "csghub-lite" {
		t.Fatalf("--provider = %q, want csghub-lite in args %#v", got, prepared.Args)
	}
	if got := argValue(prepared.Args, "--model"); got != "minimax-m2.5" {
		t.Fatalf("--model = %q, want selected model in args %#v", got, prepared.Args)
	}

	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatalf("read Pi settings: %v", err)
	}
	var settings map[string]string
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("decode Pi settings: %v", err)
	}
	if settings["defaultProvider"] != "csghub-lite" || settings["defaultModel"] != "minimax-m2.5" {
		t.Fatalf("unexpected Pi settings: %#v", settings)
	}
}

func TestOpenClawProfileMatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".openclaw-"+openClawLaunchProfile)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configJSON := `{
  "models": {
    "providers": {
      "opencsg": {
        "baseUrl": "http://127.0.0.1:11435/v1"
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "opencsg/Qwen/Qwen3.5-2B"
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "openclaw.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ok, err := openClawProfileMatches("http://127.0.0.1:11435", "Qwen/Qwen3.5-2B")
	if err != nil {
		t.Fatalf("openClawProfileMatches returned error: %v", err)
	}
	if !ok {
		t.Fatal("openClawProfileMatches returned false, want true")
	}
}

func TestSyncOpenClawProfileRewritesStaleModelCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	profileDir := filepath.Join(home, ".openclaw-"+openClawLaunchProfile)
	agentDir := filepath.Join(profileDir, "agents", "main", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}

	staleProfile := `{
  "models": {
    "mode": "merge",
    "providers": {
      "ollama": {
        "baseUrl": "http://127.0.0.1:11436",
        "models": [{"id": "old-local"}]
      },
      "csghub-lite-2": {
        "baseUrl": "http://127.0.0.1:11435/v1",
        "models": [{"id": "old-provider"}]
      },
      "csghub": {
        "baseUrl": "http://127.0.0.1:11435/v1",
        "models": [{"id": "old-cloud"}]
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "csghub/old-cloud"
      },
      "models": {
        "csghub/old-cloud": {}
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(profileDir, "openclaw.json"), []byte(staleProfile), 0o644); err != nil {
		t.Fatalf("write stale profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"csghub":{"models":[{"id":"old-cloud"}]}}}`), 0o644); err != nil {
		t.Fatalf("write stale agent models: %v", err)
	}

	models := []api.ModelInfo{
		{Model: "minimax-m2.5", DisplayName: "MiniMax M2.5", Source: "cloud"},
		{Model: "Qwen/Qwen3.5-2B", DisplayName: "Qwen/Qwen3.5-2B", Source: "local"},
	}
	if err := syncOpenClawProfile("http://127.0.0.1:11435", "user-token", "minimax-m2.5", models); err != nil {
		t.Fatalf("syncOpenClawProfile returned error: %v", err)
	}

	var profile struct {
		Models struct {
			Providers map[string]struct {
				BaseURL string `json:"baseUrl"`
				APIKey  string `json:"apiKey"`
				Models  []struct {
					ID string `json:"id"`
				} `json:"models"`
			} `json:"providers"`
		} `json:"models"`
		Agents struct {
			Defaults struct {
				Model struct {
					Primary string `json:"primary"`
				} `json:"model"`
				Models map[string]map[string]interface{} `json:"models"`
			} `json:"defaults"`
		} `json:"agents"`
	}
	data, err := os.ReadFile(filepath.Join(profileDir, "openclaw.json"))
	if err != nil {
		t.Fatalf("read synced profile: %v", err)
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("decode synced profile: %v", err)
	}
	if len(profile.Models.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(profile.Models.Providers))
	}
	provider, ok := profile.Models.Providers[openClawLaunchProviderID]
	if !ok {
		t.Fatalf("provider %q missing after sync: %#v", openClawLaunchProviderID, profile.Models.Providers)
	}
	if provider.BaseURL != "http://127.0.0.1:11435/v1" {
		t.Fatalf("provider baseUrl = %q, want local v1 URL", provider.BaseURL)
	}
	if provider.APIKey != "user-token" {
		t.Fatalf("provider apiKey = %q, want saved user token", provider.APIKey)
	}
	if got := collectOpenClawModelIDs(provider.Models); !sameStrings(got, []string{"minimax-m2.5", "Qwen/Qwen3.5-2B"}) {
		t.Fatalf("provider model ids = %#v, want refreshed model ids", got)
	}
	if profile.Agents.Defaults.Model.Primary != "opencsg/minimax-m2.5" {
		t.Fatalf("primary model = %q, want refreshed cloud model", profile.Agents.Defaults.Model.Primary)
	}
	if got := mapKeys(profile.Agents.Defaults.Models); !sameStrings(got, []string{
		"opencsg/minimax-m2.5",
		"opencsg/Qwen/Qwen3.5-2B",
	}) {
		t.Fatalf("defaults.models = %#v, want refreshed managed models", got)
	}

	var agentModels struct {
		Providers map[string]struct {
			APIKey string `json:"apiKey"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	data, err = os.ReadFile(filepath.Join(agentDir, "models.json"))
	if err != nil {
		t.Fatalf("read synced agent models: %v", err)
	}
	if err := json.Unmarshal(data, &agentModels); err != nil {
		t.Fatalf("decode synced agent models: %v", err)
	}
	if len(agentModels.Providers) != 1 {
		t.Fatalf("agent providers len = %d, want 1", len(agentModels.Providers))
	}
	if agentModels.Providers[openClawLaunchProviderID].APIKey != "user-token" {
		t.Fatalf("agent provider apiKey = %q, want saved user token", agentModels.Providers[openClawLaunchProviderID].APIKey)
	}
	if got := collectOpenClawModelIDs(agentModels.Providers[openClawLaunchProviderID].Models); !sameStrings(got, []string{"minimax-m2.5", "Qwen/Qwen3.5-2B"}) {
		t.Fatalf("agent model ids = %#v, want refreshed model ids", got)
	}
}

func TestPrepareCSGClawLaunchWritesConfigAndDefaultsToServe(t *testing.T) {
	server := launchModelTestServer([]api.ModelInfo{
		{Model: "Qwen/Qwen3.5-2B", Source: "local"},
		{Model: "minimax-m2.5", Source: "cloud"},
	})
	defer server.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "csgclaw")
	content := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake csgclaw: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	prepared, err := prepareCSGClawLaunch(launchTarget{
		AppID:       "csgclaw",
		DisplayName: "CSGClaw",
		Binaries:    []string{"csgclaw"},
	}, server.URL, "minimax-m2.5", nil)
	if err != nil {
		t.Fatalf("prepareCSGClawLaunch returned error: %v", err)
	}
	if len(prepared.Args) != 1 || prepared.Args[0] != "serve" {
		t.Fatalf("prepared args = %#v, want csgclaw serve", prepared.Args)
	}

	data, err := os.ReadFile(filepath.Join(home, ".csgclaw", "config.toml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	configText := string(data)
	for _, want := range []string{
		`manager_image_override = ""`,
		`default = "csghub-lite.minimax-m2.5"`,
		`[models.providers.csghub-lite]`,
		`base_url = "` + server.URL + `/v1"`,
		`models = ["minimax-m2.5", "Qwen/Qwen3.5-2B"]`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}
}

func TestClaudeLaunchSettingsJSONIncludesAcceptEditsMode(t *testing.T) {
	raw := claudeLaunchSettingsJSON("http://127.0.0.1:11435")

	var payload struct {
		Env         map[string]string `json:"env"`
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode settings json: %v", err)
	}
	if payload.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:11435" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want test server URL", payload.Env["ANTHROPIC_BASE_URL"])
	}
	if payload.Env["CLAUDE_CODE_ATTRIBUTION_HEADER"] != "0" {
		t.Fatalf("CLAUDE_CODE_ATTRIBUTION_HEADER = %q, want 0", payload.Env["CLAUDE_CODE_ATTRIBUTION_HEADER"])
	}
	if _, ok := payload.Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be included with ANTHROPIC_API_KEY")
	}
	if payload.Permissions.DefaultMode != "acceptEdits" {
		t.Fatalf("permissions.defaultMode = %q, want acceptEdits", payload.Permissions.DefaultMode)
	}
}

func TestPrepareClaudeLaunchPersistsSettingsEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, "claude")
	content := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, "claude.cmd")
		content = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := prepareClaudeLaunch(launchTarget{
		AppID:       "claude-code",
		DisplayName: "Claude Code",
		Binaries:    []string{"claude"},
	}, "http://127.0.0.1:11435", "glm-5(infini-ai)", nil)
	if err != nil {
		t.Fatalf("prepareClaudeLaunch returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if settings.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:11435" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want local server URL", settings.Env["ANTHROPIC_BASE_URL"])
	}
	if settings.Model != "glm-5(infini-ai)" {
		t.Fatalf("model = %q, want glm-5(infini-ai)", settings.Model)
	}
	if settings.Env["ANTHROPIC_API_KEY"] != "csghub-lite" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want csghub-lite", settings.Env["ANTHROPIC_API_KEY"])
	}
	if settings.Env["CLAUDE_CODE_ATTRIBUTION_HEADER"] != "0" {
		t.Fatalf("CLAUDE_CODE_ATTRIBUTION_HEADER = %q, want 0", settings.Env["CLAUDE_CODE_ATTRIBUTION_HEADER"])
	}
	if _, ok := settings.Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be persisted")
	}

	claudeJSON := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatalf("read claude.json: %v", err)
	}
	var state map[string]interface{}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode claude.json: %v", err)
	}
	if v, ok := state["hasCompletedOnboarding"].(bool); !ok || !v {
		t.Fatalf("hasCompletedOnboarding = %#v, want true", state["hasCompletedOnboarding"])
	}
}

func launchModelTestServer(models []api.ModelInfo) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.TagsResponse{Models: models})
		default:
			http.NotFound(w, r)
		}
	}))
}

func collectOpenClawModelIDs(items []struct {
	ID string `json:"id"`
}) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(want))
	for _, item := range want {
		seen[item]++
	}
	for _, item := range got {
		seen[item]--
		if seen[item] < 0 {
			return false
		}
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func hasConfigOverride(args []string, want string) bool {
	for i := 0; i+1 < len(args); i++ {
		if (args[i] == "-c" || args[i] == "--config") && args[i+1] == want {
			return true
		}
	}
	return false
}

func hasConfigPrefix(args []string, prefix string) bool {
	for i := 0; i+1 < len(args); i++ {
		if (args[i] == "-c" || args[i] == "--config") && strings.HasPrefix(args[i+1], prefix) {
			return true
		}
	}
	return false
}

func configValue(args []string, prefix string) string {
	for i := 0; i+1 < len(args); i++ {
		if (args[i] == "-c" || args[i] == "--config") && strings.HasPrefix(args[i+1], prefix) {
			return strings.TrimPrefix(args[i+1], prefix)
		}
	}
	return ""
}

func configValueFromConfig(configText, key string) string {
	for _, line := range strings.Split(configText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		before, after, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(before) != key {
			continue
		}
		return strings.TrimSpace(after)
	}
	return ""
}

func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func containsAll(items, want []string) bool {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		seen[item] = struct{}{}
	}
	for _, item := range want {
		if _, ok := seen[item]; !ok {
			return false
		}
	}
	return true
}
