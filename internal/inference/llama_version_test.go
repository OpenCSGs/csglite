package inference

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseLlamaServerVersion(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "dev build",
			out:  "version: 0.1.2-dev (build 10549, commit b2e5e9b28)\nbuilt with AppleClang 21.0.0.21000101 for Darwin arm64\n",
			want: "0.1.2-dev (build 10549, commit b2e5e9b28)",
		},
		{
			name: "numeric build",
			out:  "version: 4868 (c2d1a7f83)\nbuilt with cc\n",
			want: "4868 (c2d1a7f83)",
		},
		{
			name: "no version line",
			out:  "llama.cpp\n",
			want: "",
		},
		{
			name: "empty output",
			out:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLlamaServerVersion(tt.out); got != tt.want {
				t.Fatalf("parseLlamaServerVersion(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}

func TestParseLlamaServerBuildID(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    int
	}{
		{
			name:    "semver with build group",
			version: "0.6.0-dev (build 11541, commit f2918cabb)",
			want:    11541,
		},
		{
			name:    "older numeric build",
			version: "4868 (c2d1a7f83)",
			want:    4868,
		},
		{
			name:    "shallow clone build",
			version: "0.1.2-dev (build 1, commit b2e5e9b28)",
			want:    1,
		},
		{
			name:    "no build group",
			version: "0.6.0-dev",
			want:    0,
		},
		{
			name:    "empty",
			version: "",
			want:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLlamaServerBuildID(tt.version); got != tt.want {
				t.Fatalf("parseLlamaServerBuildID(%q) = %d, want %d", tt.version, got, tt.want)
			}
		})
	}
}

// TestLlamaServerVersionProbeSetsLoaderPath guards the probe against a runtime
// the loader cannot resolve on its own. Linux only searches the executable's
// directory for co-located .so files when the binary carries an $ORIGIN
// RUNPATH, so a runtime installed without patchelf fails to report its version
// unless the probe passes the same search path the server gets at start-up —
// and an unreadable build id silently disables runtime alignment.
func TestLlamaServerVersionProbeSetsLoaderPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		// macOS strips DYLD_* from the environment of a shell, so a stub cannot
		// observe the path there; the Windows probe uses PATH.
		t.Skip("only Linux can observe the probe's loader path from a stub")
	}
	const loaderVar = "LD_LIBRARY_PATH"

	dir := t.TempDir()
	binary := filepath.Join(dir, "llama-server")
	stub := "#!/bin/sh\n" +
		"case \":${" + loaderVar + "}:\" in\n" +
		"  *\":$(dirname \"$0\"):\"*) ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n" +
		"echo \"version: 0.6.0-dev (build 11541, commit f2918cabb)\"\n"
	if err := os.WriteFile(binary, []byte(stub), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}

	// The stub only stands in for an unresolvable runtime when it fails without
	// the loader path; otherwise this test proves nothing.
	t.Setenv(loaderVar, "")
	bare := exec.Command(binary, "--version")
	bare.Env = os.Environ()
	if err := bare.Run(); err == nil {
		t.Fatalf("stub reported a version without %s, so it cannot detect a missing loader path", loaderVar)
	}

	t.Setenv("CSGHUB_LITE_LLAMA_SERVER", binary)
	ResetLlamaServerVersionCache()
	t.Cleanup(ResetLlamaServerVersionCache)

	const want = "0.6.0-dev (build 11541, commit f2918cabb)"
	if got := LlamaServerVersion(); got != want {
		t.Fatalf("LlamaServerVersion() = %q, want %q", got, want)
	}
}
