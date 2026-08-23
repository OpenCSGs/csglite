package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewRootCmd(t *testing.T) {
	cmd := NewRootCmd("test")
	if cmd.Use != "csghub-lite" {
		t.Errorf("Use = %q, want %q", cmd.Use, "csghub-lite")
	}

	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Use] = true
	}

	expected := []string{
		"serve", "apps", "run MODEL", "image MODEL", "chat MODEL", "pull NAME", "list",
		"show MODEL", "ps", "start", "stop", "restart",
		"rm NAME", "login", "search QUERY", "config", "uninstall",
	}
	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("subcommand %q not found", name)
		}
	}
}

func TestRootCmd_Help(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	output := buf.String()
	if len(output) == 0 {
		t.Error("help output should not be empty")
	}
}

func TestListCmd_Aliases(t *testing.T) {
	cmd := NewRootCmd("test")
	listCmd, _, err := cmd.Find([]string{"ls"})
	if err != nil {
		t.Fatalf("Find(ls) error: %v", err)
	}
	if listCmd.Name() != "list" {
		t.Errorf("ls alias should resolve to 'list', got %q", listCmd.Name())
	}
}

func TestPullCmd_RequiresArg(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"pull"})

	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no model arg provided")
	}
}

func TestRunCmd_RequiresArg(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"run"})

	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no model arg provided")
	}
}

func TestImageCmd_RequiresPrompt(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"image", "Qwen/Qwen-Image-2512"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when prompt is missing")
	}
	if !strings.Contains(err.Error(), "--prompt is required") {
		t.Fatalf("error = %q, want prompt requirement", err.Error())
	}
}

func TestImageOutputPath(t *testing.T) {
	tests := []struct {
		output string
		index  int
		total  int
		want   string
	}{
		{"image.png", 0, 1, "image.png"},
		{"image.png", 0, 2, "image-1.png"},
		{"image.png", 1, 2, "image-2.png"},
		{"out/image", 0, 2, "out/image-1.png"},
	}
	for _, tt := range tests {
		if got := imageOutputPath(tt.output, tt.index, tt.total); got != tt.want {
			t.Fatalf("imageOutputPath(%q, %d, %d) = %q, want %q", tt.output, tt.index, tt.total, got, tt.want)
		}
	}
}

func TestRmCmd_RequiresArg(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"rm"})

	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no model arg provided")
	}
}

func TestSearchCmd_RequiresArg(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"search"})

	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no query arg provided")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{2147483648, "2.0 GB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConfigCmd_SubCommands(t *testing.T) {
	cmd := NewRootCmd("test")
	configCmd, _, err := cmd.Find([]string{"config"})
	if err != nil {
		t.Fatalf("Find(config) error: %v", err)
	}

	subcommands := make(map[string]bool)
	for _, sub := range configCmd.Commands() {
		subcommands[sub.Name()] = true
	}

	if !subcommands["set"] {
		t.Error("config set subcommand not found")
	}
	if !subcommands["get"] {
		t.Error("config get subcommand not found")
	}
	if !subcommands["show"] {
		t.Error("config show subcommand not found")
	}
}

func TestConfigCmd_HelpListsConfigurableKeys(t *testing.T) {
	cmd := NewRootCmd("test")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"config", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	output := buf.String()
	for _, want := range []string{
		"Configurable keys:",
		"server_url",
		"ai_gateway_url",
		"storage_dir",
		"model_dir",
		"dataset_dir",
		"listen_addr",
		"token",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("config help output missing %q: %q", want, output)
		}
	}
}
