package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/opencsgs/csghub-lite/internal/config"
	"github.com/opencsgs/csghub-lite/internal/model"
	"github.com/opencsgs/csghub-lite/pkg/api"
)

func TestHandleAppsIncludesPreferredModelIDWithoutResolvingModels(t *testing.T) {
	cfg := &config.Config{
		ModelDir:   t.TempDir(),
		ListenAddr: ":11435",
		AIAppPreferredModels: map[string]string{
			"claude-code": "Qwen/Qwen2.5-Coder-1.5B",
		},
	}
	for _, item := range []*model.LocalModel{
		{
			Namespace:    "Qwen",
			Name:         "Qwen3.5-2B",
			Format:       model.FormatGGUF,
			Size:         4_000_000_000,
			Files:        []string{"model.gguf"},
			DownloadedAt: time.Unix(123, 0),
		},
		{
			Namespace:    "Qwen",
			Name:         "Qwen2.5-Coder-1.5B",
			Format:       model.FormatGGUF,
			Size:         1_500_000_000,
			Files:        []string{"model.gguf"},
			DownloadedAt: time.Unix(124, 0),
		},
	} {
		if err := model.SaveManifest(cfg.ModelDir, item); err != nil {
			t.Fatalf("save model manifest: %v", err)
		}
	}

	addFakeAppBinary(t, "claude")

	s := New(cfg, "test")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	s.handleApps(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleApps status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp api.AIAppsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode apps response: %v", err)
	}

	info := findAIAppInfo(t, resp.Apps, "claude-code")
	if info.ModelID != "Qwen/Qwen2.5-Coder-1.5B" {
		t.Fatalf("claude-code model_id = %q, want preferred coder model", info.ModelID)
	}
}

func TestHandleAppsDoesNotResolveDefaultModelIDWithoutPreference(t *testing.T) {
	cfg := &config.Config{
		ModelDir:             t.TempDir(),
		ListenAddr:           ":11435",
		AIAppPreferredModels: map[string]string{},
	}
	if err := model.SaveManifest(cfg.ModelDir, &model.LocalModel{
		Namespace:    "Qwen",
		Name:         "Qwen3.5-2B",
		Format:       model.FormatGGUF,
		Size:         4_000_000_000,
		Files:        []string{"model.gguf"},
		DownloadedAt: time.Unix(123, 0),
	}); err != nil {
		t.Fatalf("save model manifest: %v", err)
	}

	addFakeAppBinary(t, "claude")

	s := New(cfg, "test")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	s.handleApps(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleApps status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp api.AIAppsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode apps response: %v", err)
	}

	info := findAIAppInfo(t, resp.Apps, "claude-code")
	if info.ModelID != "" {
		t.Fatalf("claude-code model_id = %q, want empty without saved preference", info.ModelID)
	}
}

func TestHandleAppsReturnsBeforeLatestVersionLookup(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(homeDir, "AppData", "Roaming"))
	}

	addFakeAppBinary(t, "claude")
	markerPath := filepath.Join(homeDir, ".csghub-lite", "apps", "managed", "claude-code.installed")
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(markerPath, []byte("managed\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	latestRequested := make(chan struct{}, 1)
	releaseLatest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseLatest)
		})
	}
	latestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			t.Errorf("latest path = %q, want /latest", r.URL.Path)
		}
		select {
		case latestRequested <- struct{}{}:
		default:
		}
		<-releaseLatest
		_, _ = w.Write([]byte("9.9.9\n"))
	}))
	defer latestServer.Close()
	defer release()
	t.Setenv("CSGHUB_LITE_CLAUDE_DIST_BASE_URL", latestServer.URL)

	s := New(&config.Config{
		ModelDir:             t.TempDir(),
		ListenAddr:           ":11435",
		AIAppPreferredModels: map[string]string{},
	}, "test")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/apps", nil)

	done := make(chan struct{})
	go func() {
		s.handleApps(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(750 * time.Millisecond):
		release()
		<-done
		t.Fatal("handleApps blocked on latest version lookup")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("handleApps status = %d, want %d", rec.Code, http.StatusOK)
	}
	select {
	case <-latestRequested:
	case <-time.After(time.Second):
		t.Fatal("expected latest version lookup to start in the background")
	}
}

func addFakeAppBinary(t *testing.T, name string) {
	t.Helper()

	binDir := t.TempDir()
	commandPath := filepath.Join(binDir, name)
	content := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo test-version; fi\nexit 0\n"
	if runtime.GOOS == "windows" {
		commandPath = filepath.Join(binDir, name+".cmd")
		content = "@echo off\r\nif \"%1\"==\"--version\" echo test-version\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(commandPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func findAIAppInfo(t *testing.T, apps []api.AIAppInfo, appID string) api.AIAppInfo {
	t.Helper()
	for _, info := range apps {
		if info.ID == appID {
			return info
		}
	}
	t.Fatalf("AI app %q not found in response", appID)
	return api.AIAppInfo{}
}
