package inference

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestLlamaBinarySiblingPath(t *testing.T) {
	wantName := "llama-server"
	if runtime.GOOS == "windows" {
		wantName += ".exe"
	}
	want := filepath.Join("opt", "csghub-lite", wantName)
	if got := llamaBinarySiblingPath(filepath.Join("opt", "csghub-lite", "csghub-lite"), runtime.GOOS); got != want {
		t.Fatalf("llamaBinarySiblingPath() = %q, want %q", got, want)
	}
	if got := llamaBinarySiblingPath("", runtime.GOOS); got != "" {
		t.Fatalf("llamaBinarySiblingPath(empty) = %q, want empty", got)
	}
}

func TestLlamaBinaryCandidatePathsIncludesInstallerLocations(t *testing.T) {
	home := filepath.Join("Users", "james")
	exePath := filepath.Join(home, ".local", "bin", "csghub-lite")
	paths := llamaBinaryCandidatePaths(home, exePath, runtime.GOOS)

	wants := []string{
		filepath.Join(home, ".local", "bin", platformLlamaServerName()),
		filepath.Join(home, "bin", platformLlamaServerName()),
		filepath.Join(filepath.Dir(exePath), platformLlamaServerName()),
	}
	for _, want := range wants {
		if !containsPath(paths, want) {
			t.Fatalf("llamaBinaryCandidatePaths() missing %q in %#v", want, paths)
		}
	}
}

func platformLlamaServerName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}
