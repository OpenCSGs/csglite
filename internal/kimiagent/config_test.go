package kimiagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/pkg/api"
)

func TestSyncConfigRemovesStaleModelEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	first := []api.ModelInfo{{Model: "glm-5"}, {Model: "glm-5-flash"}}
	if err := SyncConfig("http://127.0.0.1:11435", "test-token", "glm-5", first); err != nil {
		t.Fatalf("first SyncConfig() error: %v", err)
	}

	// glm-5 disappears from the server model list; its entry must be removed
	// entirely instead of being left as a provider-only stub.
	second := []api.ModelInfo{{Model: "glm-5-flash"}}
	if err := SyncConfig("http://127.0.0.1:11435", "test-token", "glm-5-flash", second); err != nil {
		t.Fatalf("second SyncConfig() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "[models.glm-5]") {
		t.Fatalf("stale model entry was kept:\n%s", text)
	}
	if strings.Contains(text, `default_model = "glm-5"`) {
		t.Fatalf("stale default model was kept:\n%s", text)
	}
	if !strings.Contains(text, "[models.glm-5-flash]") ||
		!strings.Contains(text, `model = "glm-5-flash"`) {
		t.Fatalf("current model entry is missing:\n%s", text)
	}
}

func TestSyncConfigPreservesUserDefinedModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".kimi-code", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	userConfig := `[models.custom]
provider = "other"
model = "custom-model"
`
	if err := os.WriteFile(configPath, []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SyncConfig("http://127.0.0.1:11435", "test-token", "glm-5", []api.ModelInfo{{Model: "glm-5"}}); err != nil {
		t.Fatalf("SyncConfig() error: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "[models.custom]") ||
		!strings.Contains(text, `provider = "other"`) ||
		!strings.Contains(text, `model = "custom-model"`) {
		t.Fatalf("user-defined model entry was removed:\n%s", text)
	}
}
