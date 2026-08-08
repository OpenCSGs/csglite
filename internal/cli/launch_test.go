package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveLaunchTarget(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "claude-code", want: "claude-code"},
		{input: "claude", want: "claude-code"},
		{input: "open-code", want: "open-code"},
		{input: "opencode", want: "open-code"},
		{input: "open-code-review", want: "open-code-review"},
		{input: "ocr", want: "open-code-review"},
		{input: "codex", want: "codex"},
		{input: "codex-app", want: "codex-app"},
		{input: "zcode", want: "zcode"},
		{input: "pi", want: "pi"},
		{input: "openclaw", want: "openclaw"},
		{input: "dify", want: "dify"},
		{input: "anythingllm", want: "anythingllm"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			target, err := resolveLaunchTarget(tt.input)
			if err != nil {
				t.Fatalf("resolveLaunchTarget(%q) error: %v", tt.input, err)
			}
			if target.AppID != tt.want {
				t.Fatalf("resolveLaunchTarget(%q) = %q, want %q", tt.input, target.AppID, tt.want)
			}
		})
	}
}

func TestResolveLaunchTargetUnknown(t *testing.T) {
	_, err := resolveLaunchTarget("unknown-app")
	if err == nil {
		t.Fatal("resolveLaunchTarget(unknown-app) expected error")
	}
	if !strings.Contains(err.Error(), "csghub-lite launch --help") {
		t.Fatalf("unknown app error = %q, want help hint", err)
	}
}

func TestLaunchCmdHelpListsSupportedAppsAndExamples(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"launch", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	output := buf.String()
	for _, want := range []string{
		"Supported apps:",
		"claude-code, open-code, open-code-review/ocr, codex, codex-app, zcode, pi, openclaw, dify, anythingllm",
		"csghub-lite launch zcode --model deepseek-v4-flash --provider <provider-id-or-name>",
		"csghub-lite launch zcode --pool <pool-id-or-name>",
		"csghub-lite launch ocr --model glm-5.1-1",
		"csghub-lite launch open-code-review -- review --format json",
		"csghub-lite launch pi",
		"csghub-lite launch open-code -- --help",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("launch help output missing %q: %q", want, output)
		}
	}
}

func TestLaunchCmdRequiresArgShowsHelpHint(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"launch"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when launch app is missing")
	}
	if !strings.Contains(err.Error(), "csghub-lite launch --help") {
		t.Fatalf("launch error = %q, want help hint", err)
	}
}

func TestRequestAIAppOpenIncludesModelSource(t *testing.T) {
	var got struct {
		AppID   string `json:"app_id"`
		ModelID string `json:"model_id"`
		Source  string `json:"source"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"desktop"}`))
	}))
	defer server.Close()

	if err := requestAIAppOpen(server.URL, "zcode", "deepseek-v4-flash", "provider:provider-1"); err != nil {
		t.Fatalf("requestAIAppOpen() error: %v", err)
	}
	if got.AppID != "zcode" || got.ModelID != "deepseek-v4-flash" || got.Source != "provider:provider-1" {
		t.Fatalf("request = %#v", got)
	}
}

func TestLaunchProviderScopedBaseURLUsesCSGHubForCloud(t *testing.T) {
	got, err := launchProviderScopedBaseURL("http://localhost:11435", "cloud")
	if err != nil {
		t.Fatalf("launchProviderScopedBaseURL() error: %v", err)
	}
	if got != "http://localhost:11435/providers/csghub" {
		t.Fatalf("launchProviderScopedBaseURL() = %q", got)
	}
}

func TestLaunchProviderScopedBaseURLKeepsPoolUnscoped(t *testing.T) {
	got, err := launchProviderScopedBaseURL("http://localhost:11435/", "pool:production")
	if err != nil {
		t.Fatalf("launchProviderScopedBaseURL() error: %v", err)
	}
	if got != "http://localhost:11435" {
		t.Fatalf("launchProviderScopedBaseURL() = %q", got)
	}
}

func TestGetLaunchProviderPoolFindsEnabledPoolByName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pools":[
			{"id":"disabled","name":"Disabled","model":"disabled-model","enabled":false,"members":[]},
			{"id":"production","name":"Production Chat","model":"production-chat","enabled":true,"members":[]}
		]}`))
	}))
	defer server.Close()

	pool, err := getLaunchProviderPool(server.URL, "production chat")
	if err != nil {
		t.Fatalf("getLaunchProviderPool() error: %v", err)
	}
	if pool.ID != "production" || pool.Model != "production-chat" {
		t.Fatalf("pool = %#v", pool)
	}
}
