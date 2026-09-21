// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import "testing"

// A bundle names files that the receiving node will create on its own disk, so
// a member that is compromised or simply running something else must not be
// able to name a path that lands outside the model directory.
func TestBundlePathsAreRefusedWhenTheyEscape(t *testing.T) {
	for _, bad := range []string{
		"../../../../etc/passwd",
		"/etc/passwd",
		"a/../../b",
		"..",
		"",
		`dir\file`,
		"./a",
		"a//b",
	} {
		if err := checkBundlePaths(ModelBundle{Files: []BundleFile{{Path: bad, Size: 1}}}); err == nil {
			t.Fatalf("path %q was accepted", bad)
		}
		if err := checkBundlePaths(ModelBundle{Extras: []BundleFile{{Path: bad, Size: 1}}}); err == nil {
			t.Fatalf("derived path %q was accepted", bad)
		}
	}
	for _, ok := range []string{"model.gguf", "a/b/c.safetensors", "mmproj.gguf"} {
		if err := checkBundlePaths(ModelBundle{Files: []BundleFile{{Path: ok, Size: 1}}}); err != nil {
			t.Fatalf("path %q was refused: %v", ok, err)
		}
	}
	if err := checkBundlePaths(ModelBundle{Files: []BundleFile{{Path: "a", Size: -1}}}); err == nil {
		t.Fatal("a negative size was accepted")
	}
}
