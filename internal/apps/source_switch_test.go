package apps

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceSwitchSnapshotsAndRestoresOriginalFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "user", "config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("model_provider = \"openai\"\n")
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}

	manager := NewSourceSwitchManager(filepath.Join(root, "storage"))
	isManaged := func(data []byte) bool { return string(data) == "managed\n" }
	status, err := manager.UseOpenCSG("codex", target, isManaged, func() error {
		return os.WriteFile(target, []byte("managed\n"), 0o640)
	})
	if err != nil {
		t.Fatalf("UseOpenCSG() error: %v", err)
	}
	if status.Mode != ProviderModeOpenCSG || status.Drifted {
		t.Fatalf("UseOpenCSG() status = %#v", status)
	}

	status, err = manager.RestoreNative("codex", target, isManaged, func() error {
		t.Fatal("legacy restore should not run when a snapshot exists")
		return nil
	})
	if err != nil {
		t.Fatalf("RestoreNative() error: %v", err)
	}
	if status.Mode != ProviderModeNative {
		t.Fatalf("RestoreNative() mode = %q", status.Mode)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("restored config = %q, want %q", got, original)
	}
}

func TestSourceSwitchRefusesToOverwriteDriftedConfig(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "config.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewSourceSwitchManager(filepath.Join(root, "storage"))
	isManaged := func(data []byte) bool { return string(data) == "managed\n" }
	if _, err := manager.UseOpenCSG("claude-code", target, isManaged, func() error {
		return os.WriteFile(target, []byte("managed\n"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("user changed this\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := manager.RestoreNative("claude-code", target, isManaged, func() error {
		return nil
	})
	if err == nil {
		t.Fatal("RestoreNative() should reject drift")
	}
	if !status.Drifted {
		t.Fatalf("RestoreNative() status = %#v, want drifted", status)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "user changed this\n" {
		t.Fatalf("drifted config was overwritten: %q", got)
	}
}

func TestSourceSwitchLegacyManagedConfigUsesBestEffortRestore(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "config.toml")
	if err := os.WriteFile(target, []byte("legacy-managed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewSourceSwitchManager(filepath.Join(root, "storage"))
	isManaged := func(data []byte) bool { return string(data) == "legacy-managed\n" }
	restored := false
	status, err := manager.RestoreNative("codex", target, isManaged, func() error {
		restored = true
		return os.WriteFile(target, []byte("native-defaults\n"), 0o600)
	})
	if err != nil {
		t.Fatalf("RestoreNative() error: %v", err)
	}
	if !restored || status.Mode != ProviderModeNative {
		t.Fatalf("RestoreNative() restored=%v status=%#v", restored, status)
	}
}

func TestSourceSwitchRestoresOriginallyMissingFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "settings.json")
	manager := NewSourceSwitchManager(filepath.Join(root, "storage"))
	isManaged := func(data []byte) bool { return len(data) > 0 }

	if _, err := manager.UseOpenCSG("claude-code", target, isManaged, func() error {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, []byte("managed\n"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RestoreNative("claude-code", target, isManaged, func() error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("originally missing file still exists, stat error=%v", err)
	}
}

func TestSourceSwitchRollsBackWhenApplyFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "config.toml")
	original := []byte("native\n")
	if err := os.WriteFile(target, original, 0o640); err != nil {
		t.Fatal(err)
	}

	manager := NewSourceSwitchManager(filepath.Join(root, "storage"))
	_, err := manager.UseOpenCSG("codex", target, func([]byte) bool { return false }, func() error {
		if err := os.WriteFile(target, []byte("partial managed config\n"), 0o600); err != nil {
			return err
		}
		return errors.New("apply failed")
	})
	if err == nil {
		t.Fatal("UseOpenCSG() should return the apply error")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("config after rollback = %q, want %q", got, original)
	}
}
