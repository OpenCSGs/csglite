package cli

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
)

func TestConsoleAuthConfigCommands(t *testing.T) {
	setupCLIConfigHome(t)
	t.Setenv(config.EnvConsoleAuthMode, "")
	if err := runConfigSet(nil, []string{"console_auth_mode", "required"}); err != nil {
		t.Fatal(err)
	}
	config.Reset()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Console.AuthMode != config.ConsoleAuthRequired {
		t.Fatal("mode not persisted")
	}
	if err := runConfigSet(nil, []string{"console_auth_mode", "off"}); err == nil {
		t.Fatal("invalid policy accepted")
	}
	t.Setenv(config.EnvConsoleAuthMode, "auto")
	output := captureCLIStdout(t, func() {
		if err := runConfigGet(nil, []string{"console_auth_mode"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(output) != "auto" {
		t.Fatal(output)
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	config.Reset()
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Console.AuthMode != config.ConsoleAuthRequired {
		t.Fatal("override changed persisted setting")
	}
	if err := runConfigUnset(nil, []string{"console_auth_mode"}); err != nil {
		t.Fatal(err)
	}
	config.Reset()
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvConsoleAuthMode, "")
	if mode, err := cfg.EffectiveConsoleAuthMode(); err != nil || mode != config.ConsoleAuthAuto {
		t.Fatalf("%q %v", mode, err)
	}
}

func TestRunConfigSetStorageDir(t *testing.T) {
	home := setupCLIConfigHome(t)
	root := filepath.Join(home, "shared-storage")

	if err := runConfigSet(nil, []string{"storage_dir", root}); err != nil {
		t.Fatalf("runConfigSet(storage_dir) error: %v", err)
	}

	config.Reset()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}

	wantModelDir := filepath.Join(root, config.ModelsDir)
	wantDatasetDir := filepath.Join(root, config.DatasetsDir)
	if cfg.ModelDir != wantModelDir {
		t.Fatalf("ModelDir = %q, want %q", cfg.ModelDir, wantModelDir)
	}
	if cfg.DatasetDir != wantDatasetDir {
		t.Fatalf("DatasetDir = %q, want %q", cfg.DatasetDir, wantDatasetDir)
	}
	if _, err := os.Stat(wantModelDir); err != nil {
		t.Fatalf("model dir not created: %v", err)
	}
	if _, err := os.Stat(wantDatasetDir); err != nil {
		t.Fatalf("dataset dir not created: %v", err)
	}
}

func TestRunConfigShowAndGetIncludeStorageDir(t *testing.T) {
	home := setupCLIConfigHome(t)
	root := filepath.Join(home, "shared-storage")

	if err := runConfigSet(nil, []string{"storage_dir", root}); err != nil {
		t.Fatalf("runConfigSet(storage_dir) error: %v", err)
	}

	showOutput := captureCLIStdout(t, func() {
		if err := runConfigShow(nil, nil); err != nil {
			t.Fatalf("runConfigShow() error: %v", err)
		}
	})
	if !strings.Contains(showOutput, "storage_dir:") || !strings.Contains(showOutput, root) {
		t.Fatalf("config show output missing storage_dir: %q", showOutput)
	}
	if !strings.Contains(showOutput, "dataset_dir:") || !strings.Contains(showOutput, filepath.Join(root, config.DatasetsDir)) {
		t.Fatalf("config show output missing dataset_dir: %q", showOutput)
	}

	getOutput := captureCLIStdout(t, func() {
		if err := runConfigGet(nil, []string{"storage_dir"}); err != nil {
			t.Fatalf("runConfigGet(storage_dir) error: %v", err)
		}
	})
	if strings.TrimSpace(getOutput) != root {
		t.Fatalf("config get storage_dir = %q, want %q", strings.TrimSpace(getOutput), root)
	}
}

func TestRunConfigShowAndGetIncludeDefaultAIGatewayURL(t *testing.T) {
	setupCLIConfigHome(t)

	showOutput := captureCLIStdout(t, func() {
		if err := runConfigShow(nil, nil); err != nil {
			t.Fatalf("runConfigShow() error: %v", err)
		}
	})
	if !strings.Contains(showOutput, "ai_gateway_url:") || !strings.Contains(showOutput, cloud.DefaultBaseURL) {
		t.Fatalf("config show output missing ai_gateway_url default: %q", showOutput)
	}

	getOutput := captureCLIStdout(t, func() {
		if err := runConfigGet(nil, []string{"ai_gateway_url"}); err != nil {
			t.Fatalf("runConfigGet(ai_gateway_url) error: %v", err)
		}
	})
	if strings.TrimSpace(getOutput) != cloud.DefaultBaseURL {
		t.Fatalf("config get ai_gateway_url = %q, want %q", strings.TrimSpace(getOutput), cloud.DefaultBaseURL)
	}
}

func TestRunConfigSetServerURLClearsTokenWhenChanged(t *testing.T) {
	setupCLIConfigHome(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	cfg.Token = "existing-token"
	cfg.OpenCSGAPIKey = "old-manual-key"
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save() error: %v", err)
	}

	if err := runConfigSet(nil, []string{"server_url", "https://opencsg-stg.com"}); err != nil {
		t.Fatalf("runConfigSet(server_url) error: %v", err)
	}

	config.Reset()
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("config.Load() after set error: %v", err)
	}
	if cfg.ServerURL != "https://opencsg-stg.com" {
		t.Fatalf("ServerURL = %q, want staging URL", cfg.ServerURL)
	}
	if cfg.OpenCSGAPIKey != "" {
		t.Fatal("old manual API key survived server_url change")
	}
	if cfg.Token != "" {
		t.Fatalf("Token = %q, want empty after server_url change", cfg.Token)
	}
}

func TestRunConfigSetServerURLKeepsTokenWhenUnchanged(t *testing.T) {
	setupCLIConfigHome(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	cfg.ServerURL = "https://opencsg-stg.com"
	cfg.Token = "existing-token"
	cfg.OpenCSGAPIKey = "old-manual-key"
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save() error: %v", err)
	}

	if err := runConfigSet(nil, []string{"server_url", " https://opencsg-stg.com "}); err != nil {
		t.Fatalf("runConfigSet(server_url) error: %v", err)
	}

	config.Reset()
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("config.Load() after set error: %v", err)
	}
	if cfg.OpenCSGAPIKey != "old-manual-key" {
		t.Fatal("unchanged server_url cleared the manual API key")
	}
	if cfg.Token != "existing-token" {
		t.Fatalf("Token = %q, want existing-token", cfg.Token)
	}
}

func TestRunConfigUnsetServerURLRestoresDefaultAndClearsToken(t *testing.T) {
	setupCLIConfigHome(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	cfg.ServerURL = "https://opencsg-stg.com"
	cfg.Token = "existing-token"
	cfg.OpenCSGAPIKey = "old-manual-key"
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save() error: %v", err)
	}

	if err := runConfigUnset(nil, []string{"server_url"}); err != nil {
		t.Fatalf("runConfigUnset(server_url) error = %v", err)
	}

	config.Reset()
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("config.Load() after unset error: %v", err)
	}
	if cfg.ServerURL != config.DefaultServerURL {
		t.Fatalf("ServerURL = %q, want %q", cfg.ServerURL, config.DefaultServerURL)
	}
	if cfg.OpenCSGAPIKey != "" {
		t.Fatal("old manual API key survived server_url change")
	}
	if cfg.Token != "" {
		t.Fatalf("Token = %q, want cleared token", cfg.Token)
	}
}

func TestRunConfigUnsetAIGatewayURLUsesDefault(t *testing.T) {
	setupCLIConfigHome(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	cfg.AIGatewayURL = "https://gateway.example.com"
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save() error: %v", err)
	}

	if err := runConfigUnset(nil, []string{"ai_gateway_url"}); err != nil {
		t.Fatalf("runConfigUnset(ai_gateway_url) error = %v", err)
	}

	config.Reset()
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("config.Load() after unset error: %v", err)
	}
	if cfg.AIGatewayURL != "" {
		t.Fatalf("AIGatewayURL = %q, want empty custom value", cfg.AIGatewayURL)
	}
	getOutput := captureCLIStdout(t, func() {
		if err := runConfigGet(nil, []string{"ai_gateway_url"}); err != nil {
			t.Fatalf("runConfigGet(ai_gateway_url) error: %v", err)
		}
	})
	if strings.TrimSpace(getOutput) != cloud.DefaultBaseURL {
		t.Fatalf("config get ai_gateway_url = %q, want %q", strings.TrimSpace(getOutput), cloud.DefaultBaseURL)
	}
}

func TestRunConfigURLCredentialChanges(t *testing.T) {
	const customServer = "https://community.example.com"
	const customGateway = "https://gateway.example.com"
	for _, tt := range []struct {
		name      string
		key       string
		before    string
		after     string
		unset     bool
		token     string
		wantToken string
		wantKey   string
	}{
		{name: "server change with manual key only", key: "server_url", before: config.DefaultServerURL, after: customServer},
		{name: "server unset with manual key only", key: "server_url", before: customServer, unset: true},
		{name: "unchanged default server", key: "server_url", before: config.DefaultServerURL, unset: true, token: "token", wantToken: "token", wantKey: "manual-key"},
		{name: "empty server means same default", key: "server_url", before: config.DefaultServerURL, after: " ", token: "token", wantToken: "token", wantKey: "manual-key"},
		{name: "changed gateway", key: "ai_gateway_url", before: customGateway, after: cloud.DefaultBaseURL, token: "token", wantToken: "token"},
		{name: "unchanged gateway", key: "ai_gateway_url", before: customGateway, after: " " + customGateway + " ", token: "token", wantToken: "token", wantKey: "manual-key"},
		{name: "unset custom gateway", key: "ai_gateway_url", before: customGateway, unset: true, token: "token", wantToken: "token"},
		{name: "unchanged implicit gateway", key: "ai_gateway_url", unset: true, token: "token", wantToken: "token", wantKey: "manual-key"},
		{name: "set gateway with manual key only", key: "ai_gateway_url", after: customGateway},
		{name: "unset gateway with manual key only", key: "ai_gateway_url", before: customGateway, unset: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setupCLIConfigHome(t)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if tt.key == "server_url" {
				cfg.ServerURL = tt.before
			} else {
				cfg.AIGatewayURL = tt.before
			}
			cfg.Token = tt.token
			cfg.OpenCSGAPIKey = "manual-key"
			if err := config.Save(cfg); err != nil {
				t.Fatal(err)
			}
			if tt.unset {
				err = runConfigUnset(nil, []string{tt.key})
			} else {
				err = runConfigSet(nil, []string{tt.key, tt.after})
			}
			if err != nil {
				t.Fatal(err)
			}
			config.Reset()
			cfg, err = config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Token != tt.wantToken || cfg.OpenCSGAPIKey != tt.wantKey {
				t.Fatalf("credential reset mismatch: token retained = %t, manual key retained = %t", cfg.Token != "", cfg.OpenCSGAPIKey != "")
			}
		})
	}
}

func setupCLIConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.Reset()
	t.Cleanup(config.Reset)
	isolateFromRunningServer(t)
	return home
}

// isolateFromRunningServer points the temporary config at a closed local port.
// Commands sync their changes to the running server at listen_addr, and the
// default address is the developer's own csghub-lite: a test run there once
// logged that server out and shut it down.
func isolateFromRunningServer(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve closed port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved port: %v", err)
	}
	saveListenAddr(t, addr)
}

func captureCLIStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	os.Stdout = writePipe

	defer func() {
		os.Stdout = oldStdout
	}()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(readPipe)
		done <- string(data)
	}()

	fn()

	if err := writePipe.Close(); err != nil {
		t.Fatalf("writePipe.Close() error: %v", err)
	}
	return <-done
}

func captureCLIStderr(t *testing.T, fn func()) string {
	t.Helper()

	oldStderr := os.Stderr
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	os.Stderr = writePipe

	defer func() {
		os.Stderr = oldStderr
	}()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(readPipe)
		done <- string(data)
	}()

	fn()

	if err := writePipe.Close(); err != nil {
		t.Fatalf("writePipe.Close() error: %v", err)
	}
	return <-done
}
