package server

import (
	"path/filepath"
	"testing"
)

// The second lock on the same door: even if an unsafe path reached this far,
// nothing may be written outside the directory the copy belongs in.
func TestResolveUnderRefusesPathsThatEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "model")
	for _, bad := range []string{
		"../evil", "../../etc/passwd", "a/../../evil", "/etc/passwd", "",
		// A bundle path is relative and forward-slash by definition, so a
		// leading slash and a backslash are refused on every platform rather
		// than meaning different things on each.
		`\evil`, `a\b`, `C:\evil`,
	} {
		if got, err := resolveUnder(root, bad); err == nil {
			t.Fatalf("path %q resolved to %q instead of being refused", bad, got)
		}
	}
	got, err := resolveUnder(root, "a/b.gguf")
	if err != nil {
		t.Fatalf("a plain path was refused: %v", err)
	}
	if want := filepath.Join(root, "a", "b.gguf"); got != want {
		t.Fatalf("resolveUnder = %q, want %q", got, want)
	}
}
