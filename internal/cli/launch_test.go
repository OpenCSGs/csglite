package cli

import (
	"bytes"
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
		{input: "codex", want: "codex"},
		{input: "antigravity", want: "antigravity"},
		{input: "agy", want: "antigravity"},
		{input: "pi", want: "pi"},
		{input: "openclaw", want: "openclaw"},
		{input: "csgclaw", want: "csgclaw"},
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
		"claude-code, open-code, codex, antigravity, pi, openclaw, csgclaw, dify, anythingllm",
		"csghub-lite launch pi",
		"csghub-lite launch csgclaw",
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
