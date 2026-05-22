package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
)

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
	if cfg.Token != "existing-token" {
		t.Fatalf("Token = %q, want existing-token", cfg.Token)
	}
}

func setupCLIConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.Reset()
	t.Cleanup(config.Reset)
	return home
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
