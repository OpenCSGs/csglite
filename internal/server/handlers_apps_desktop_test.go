package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/apps"
	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/codexagent"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/internal/zcodeagent"
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

func TestRemoveCSGClawStaleSandboxSockets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets are not available on Windows")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home: %v", err)
	}
	testBase := filepath.Join(home, ".csghub-lite", "tmp")
	if err := os.MkdirAll(testBase, 0o700); err != nil {
		t.Fatalf("mkdir test base: %v", err)
	}
	root, err := os.MkdirTemp(testBase, "socket-test-")
	if err != nil {
		t.Fatalf("create short temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketsDir := filepath.Join(root, "agents", "sockets")
	if err := os.MkdirAll(socketsDir, 0o755); err != nil {
		t.Fatalf("mkdir sockets: %v", err)
	}
	socketPath := filepath.Join(socketsDir, "box.sock")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir temp root: %v", err)
	}
	listener, err := net.Listen("unix", filepath.Join("agents", "sockets", "box.sock"))
	if restoreErr := os.Chdir(cwd); restoreErr != nil {
		t.Fatalf("restore cwd: %v", restoreErr)
	}
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close unix socket: %v", err)
	}
	regularPath := filepath.Join(socketsDir, "keep.txt")
	if err := os.WriteFile(regularPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write regular file: %v", err)
	}

	if err := removeCSGClawStaleSandboxSockets(root); err != nil {
		t.Fatalf("remove stale sockets: %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket still exists: %v", err)
	}
	if _, err := os.Stat(regularPath); err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
}

func TestSetCSGClawDesktopSandboxProviderForcesDocker(t *testing.T) {
	input := `[sandbox]
provider = "boxlite"
home_dir_name = "boxlite"

[models]
default = "csghub-lite.test"
`
	updated := setCSGClawSandboxProvider(input, "docker")
	if !strings.Contains(updated, `[sandbox]`+"\n"+`provider = "docker"`) {
		t.Fatalf("updated config did not force docker:\n%s", updated)
	}
	if strings.Contains(updated, `provider = "boxlite"`) {
		t.Fatalf("updated config retained boxlite provider:\n%s", updated)
	}
	if !strings.Contains(updated, `home_dir_name = "boxlite"`) || !strings.Contains(updated, `[models]`) {
		t.Fatalf("updated config dropped unrelated settings:\n%s", updated)
	}
}

func TestHandleAppSetPathRejectsUnsupportedApp(t *testing.T) {
	s := newTestServer(t)

	body := `{"app_id":"claude-code","path":"/tmp/claude"}`
	req := httptest.NewRequest("POST", "/api/apps/path", strings.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleAppSetPath(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleAppSetPathRequiresFields(t *testing.T) {
	s := newTestServer(t)

	body := `{"app_id":"codex-app"}`
	req := httptest.NewRequest("POST", "/api/apps/path", strings.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleAppSetPath(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleAppSetPathRejectsMissingTarget(t *testing.T) {
	s := newTestServer(t)

	body := `{"app_id":"codex-app","path":"/definitely/missing/Codex.app"}`
	req := httptest.NewRequest("POST", "/api/apps/path", strings.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleAppSetPath(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
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

func TestHandleAppOpenZCodeRequiresLocalhost(t *testing.T) {
	s := newTestServer(t)
	body := `{"app_id":"zcode"}`
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
	t.Setenv("USERPROFILE", home)

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

	got, err := apps.CodexAppLaunchTarget()
	if err != nil {
		t.Fatalf("CodexAppLaunchTarget() error: %v", err)
	}
	if got != target {
		t.Fatalf("CodexAppLaunchTarget() = %q, want %q", got, target)
	}
}

func TestZCodeLaunchTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	appDir := filepath.Join(home, ".local", "share", "zcode", "versions", "3.3.4")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	target := filepath.Join(appDir, "ZCode.AppImage")
	if runtime.GOOS == "darwin" {
		target = filepath.Join(appDir, "ZCode.app")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("mkdir app bundle: %v", err)
		}
	} else {
		if runtime.GOOS == "windows" {
			target = filepath.Join(appDir, "ZCode.exe")
		}
		if err := os.WriteFile(target, []byte("stub"), 0o755); err != nil {
			t.Fatalf("write target: %v", err)
		}
	}
	runtimeRoot := filepath.Join(home, ".local", "share", "zcode")
	if err := os.WriteFile(filepath.Join(runtimeRoot, "launch-target"), []byte(target+"\n"), 0o644); err != nil {
		t.Fatalf("write launch target: %v", err)
	}

	got, err := apps.ZCodeLaunchTarget()
	if err != nil {
		t.Fatalf("ZCodeLaunchTarget() error: %v", err)
	}
	if got != target {
		t.Fatalf("ZCodeLaunchTarget() = %q, want %q", got, target)
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
		Namespace: "Qwen",
		Name:      "Qwen3.5-2B",
		Format:    model.FormatGGUF,
		Size:      4_000_000_000,
		Files:     []string{"model.gguf"},
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	s := newTestServerWithConfig(t, cfg)
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
		`base_url = "http://127.0.0.1:11435/providers/local/v1"`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}
}

func TestEnsureZCodeLaunchConfigWritesLocalProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	originalStop := stopZCodeForConfigReloadFunc
	stopZCodeForConfigReloadFunc = func() error { return nil }
	t.Cleanup(func() { stopZCodeForConfigReloadFunc = originalStop })

	cfg := &config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
		Token:      "test-token",
	}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace: "Qwen",
		Name:      "Qwen3.5-2B",
		Format:    model.FormatGGUF,
		Size:      4_000_000_000,
		Files:     []string{"model.gguf"},
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	s := newTestServerWithConfig(t, cfg)
	s.cloud = cloud.NewService("")
	modelID, err := s.ensureZCodeLaunchConfig(context.Background(), "", "")
	if err != nil {
		t.Fatalf("ensureZCodeLaunchConfig() error: %v", err)
	}
	if modelID != "Qwen3.5-2B" {
		t.Fatalf("modelID = %q, want Qwen3.5-2B", modelID)
	}

	configPath, err := zcodeagent.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var root struct {
		Provider map[string]struct {
			Kind    string `json:"kind"`
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
			Models map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse ZCode config: %v", err)
	}
	provider, ok := root.Provider[zcodeagent.ProviderID]
	if !ok {
		t.Fatal("csghub-lite provider missing from ZCode config")
	}
	if provider.Kind != "openai-compatible" ||
		provider.Options.BaseURL != "http://127.0.0.1:11435/providers/local/v1" ||
		provider.Options.APIKey != "test-token" {
		t.Fatalf("provider = %#v", provider)
	}
	if _, ok := provider.Models["Qwen3.5-2B"]; !ok {
		t.Fatal("selected local model missing from ZCode provider")
	}
}

func TestHandleAppModelSaveImmediatelySyncsZCodeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	originalStop := stopZCodeForConfigReloadFunc
	stopZCodeForConfigReloadFunc = func() error { return nil }
	t.Cleanup(func() { stopZCodeForConfigReloadFunc = originalStop })

	cfg := &config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
		Token:      "test-token",
	}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace: "Qwen",
		Name:      "Qwen3.5-2B",
		Format:    model.FormatGGUF,
		Files:     []string{"model.gguf"},
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	s := newTestServerWithConfig(t, cfg)
	s.cloud = cloud.NewService("")
	req := httptest.NewRequest(http.MethodPost, "/api/apps/model", strings.NewReader(
		`{"app_id":"zcode","model_id":"Qwen3.5-2B","source":"local"}`,
	))
	rec := httptest.NewRecorder()
	s.handleAppModelSave(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	configPath, err := zcodeagent.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `"baseURL": "http://127.0.0.1:11435/providers/local/v1"`) ||
		!strings.Contains(string(data), `"Qwen3.5-2B"`) {
		t.Fatalf("saved model was not synced to ZCode config:\n%s", data)
	}
}
