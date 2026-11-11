package deviceid

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHashNormalizesMachineID(t *testing.T) {
	want := Hash(runtime.GOOS, "abc-123")
	for _, raw := range []string{"  abc-123\n", "ABC-123", `"abc-123"`, "{ABC-123}", "abc-123  "} {
		if got := Hash(runtime.GOOS, raw); got != want {
			t.Fatalf("Hash(%q) = %q, want %q", raw, got, want)
		}
	}
	if Hash(runtime.GOOS, "abc - 123") == want {
		t.Fatal("ids that differ beyond spacing and quoting must not collapse")
	}
	if got := Hash(runtime.GOOS, "   "); got != "" {
		t.Fatalf("Hash of blank id = %q, want empty", got)
	}
	if Hash("linux", "abc") == Hash("windows", "abc") {
		t.Fatal("device id must depend on the platform")
	}
	if len(want) != 64 || strings.ToLower(want) != want {
		t.Fatalf("device id = %q, want 64 lowercase hex characters", want)
	}
}

func TestResolveUsesMachineIDWithoutWritingFallback(t *testing.T) {
	root := t.TempDir()
	id, err := resolve(func() (string, error) { return "machine-1", nil }, root)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != Hash(runtime.GOOS, "machine-1") {
		t.Fatalf("resolve = %q, want hash of the machine id", id)
	}
	if _, err := os.Stat(filepath.Join(root, fallbackFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback file should not be created when a machine id exists: %v", err)
	}
}

func TestResolveUsesSameMachineIDAcrossStorageRoots(t *testing.T) {
	read := func() (string, error) { return "machine-1", nil }
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	first, err := resolve(read, firstRoot)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	second, err := resolve(read, secondRoot)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if first != second {
		t.Fatalf("two storage roots on one machine must share a device id: %q vs %q", first, second)
	}
	for _, root := range []string{firstRoot, secondRoot} {
		if _, err := os.Stat(filepath.Join(root, fallbackFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a machine id must not create a fallback file in %s: %v", root, err)
		}
	}
}

func TestResolveFallsBackToStorageRootUUID(t *testing.T) {
	root := t.TempDir()
	read := func() (string, error) { return "", errors.New("unavailable") }

	first, err := resolve(read, root)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	second, err := resolve(read, root)
	if err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	if first != second {
		t.Fatalf("fallback id changed between calls: %q then %q", first, second)
	}
	raw, err := os.ReadFile(filepath.Join(root, fallbackFileName))
	if err != nil {
		t.Fatalf("reading fallback file: %v", err)
	}
	if !fallbackPattern.MatchString(normalize(string(raw))) {
		t.Fatalf("fallback file %q is not a valid id", raw)
	}
}

func TestResolveRegeneratesCorruptFallback(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, fallbackFileName)
	if err := os.WriteFile(path, []byte("not a uuid"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := resolve(func() (string, error) { return "", errors.New("unavailable") }, root)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fallback file: %v", err)
	}
	if normalize(string(raw)) == "not a uuid" {
		t.Fatal("corrupt fallback file was kept")
	}
	if id != Hash(runtime.GOOS, normalize(string(raw))) {
		t.Fatal("id does not match the regenerated fallback file")
	}
}

func TestResolveWithoutStorageRootFails(t *testing.T) {
	_, err := resolve(func() (string, error) { return "", errors.New("unavailable") }, "")
	if err == nil {
		t.Fatal("expected an error when neither machine id nor storage root is available")
	}
}
