package license_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/opencsgs/csglite/internal/license"
)

func TestLoadOrCreateDeviceIDPersists(t *testing.T) {
	root := t.TempDir()
	first, err := license.LoadOrCreateDeviceID(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Fatalf("device ID %q, want 32 hex characters", first)
	}
	second, err := license.LoadOrCreateDeviceID(root)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("device ID changed from %q to %q", first, second)
	}
	info, err := os.Stat(filepath.Join(root, license.DeviceIDFileName))
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not carry Unix permission bits.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("device ID file mode %v", info.Mode().Perm())
	}
}

func TestLoadOrCreateDeviceIDReplacesGarbage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, license.DeviceIDFileName)
	if err := os.WriteFile(path, []byte("not a valid id!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := license.LoadOrCreateDeviceID(root)
	if err != nil || id == "not a valid id!" || len(id) != 32 {
		t.Fatalf("device ID %q, err %v", id, err)
	}
	if _, err := license.LoadOrCreateDeviceID(""); err == nil {
		t.Fatal("an empty storage root was accepted")
	}
}

func TestDefaultOptionsLoadDeviceID(t *testing.T) {
	root := t.TempDir()
	opts, err := license.DefaultOptions(root, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	want, err := license.LoadOrCreateDeviceID(root)
	if err != nil {
		t.Fatal(err)
	}
	if opts.DeviceID != want {
		t.Fatalf("DeviceID = %q, want %q", opts.DeviceID, want)
	}
}
