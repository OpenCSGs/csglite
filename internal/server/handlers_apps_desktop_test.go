package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opencsgs/csghub-lite/internal/cloud"
	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/codexagent"
	"github.com/opencsgs/csghub-lite/internal/model"
)

func TestIsLocalhostBrowserAccess(t *testing.T) {
	tests := []struct {
		name string
		host string
		fwd  string
		want bool
	}{
		{name: "localhost", host: "localhost:11435", want: true},
		{name: "loopback ip", host: "127.0.0.1:11435", want: true},
		{name: "remote host", host: "192.168.1.18:11435", want: false},
		{name: "forwarded remote", host: "localhost:11435", fwd: "192.168.1.18:11435", want: false},
		{name: "forwarded localhost", host: "192.168.1.18:11435", fwd: "127.0.0.1:11435", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/apps/open", nil)
			req.Host = tt.host
			if tt.fwd != "" {
				req.Header.Set("X-Forwarded-Host", tt.fwd)
			}
			if got := isLocalhostBrowserAccess(req); got != tt.want {
				t.Fatalf("isLocalhostBrowserAccess() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleAppOpenCodexAppRequiresLocalhost(t *testing.T) {
	s := newTestServer(t)

	body := `{"app_id":"codex-app"}`
	req := httptest.NewRequest("POST", "/api/apps/open", strings.NewReader(body))
	req.Host = "192.168.1.18:11435"
	rec := httptest.NewRecorder()

	s.handleAppOpen(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "localhost") {
		t.Fatalf("body = %s, want localhost error", rec.Body.String())
	}
}

func TestCodexAppLaunchTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	appDir := filepath.Join(home, ".local", "share", "codex-app", "versions", "26.527.31326")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}

	target := filepath.Join(appDir, "Codex.app")
	if runtime.GOOS == "windows" {
		target = filepath.Join(appDir, "Codex.exe")
		if err := os.WriteFile(target, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write exe stub: %v", err)
		}
	} else if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir app bundle: %v", err)
	}

	runtimeRoot := filepath.Join(home, ".local", "share", "codex-app")
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		t.Fatalf("mkdir runtime root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "launch-target"), []byte(target+"\n"), 0o644); err != nil {
		t.Fatalf("write launch target: %v", err)
	}

	got, err := codexAppLaunchTarget()
	if err != nil {
		t.Fatalf("codexAppLaunchTarget() error: %v", err)
	}
	if got != target {
		t.Fatalf("codexAppLaunchTarget() = %q, want %q", got, target)
	}
}

func TestEnsureCodexAppLaunchConfigWritesSharedCodexConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := &config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
		Token:      "test-token",
	}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3.5-2B",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf"},
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	s := New(cfg, "test")
	s.cloud = cloud.NewService("")

	modelID, err := s.ensureCodexAppLaunchConfig(context.Background(), "", "")
	if err != nil {
		t.Fatalf("ensureCodexAppLaunchConfig() error: %v", err)
	}
	if modelID != "Qwen3.5-2B" {
		t.Fatalf("modelID = %q, want Qwen3.5-2B", modelID)
	}

	configPath, err := codexagent.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error: %v", err)
	}
	wantPath := filepath.Join(home, ".codex", "config.toml")
	if configPath != wantPath {
		t.Fatalf("ConfigPath() = %q, want %q", configPath, wantPath)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	configText := string(data)
	for _, want := range []string{
		`model = "Qwen3.5-2B"`,
		`model_provider = "csghub_lite"`,
		`base_url = "http://127.0.0.1:11435/v1"`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}
}
