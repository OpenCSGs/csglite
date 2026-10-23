package inference

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var (
	llamaVersionMu    sync.Mutex
	llamaVersionStr   string
	llamaVersionReady bool

	llamaBuildInParensPattern = regexp.MustCompile(`\(build ([0-9]+)`)
	llamaBuildLeadingPattern  = regexp.MustCompile(`^([0-9]+) \(`)
)

// LlamaServerVersion returns the version reported by the installed
// llama-server binary, for example "0.1.2-dev (build 10549, commit b2e5e9b28)".
// It is read once and cached; an empty string means no llama-server could be
// resolved or its version could not be determined.
func LlamaServerVersion() string {
	llamaVersionMu.Lock()
	defer llamaVersionMu.Unlock()
	if !llamaVersionReady {
		llamaVersionStr = resolveLlamaServerVersion()
		llamaVersionReady = true
	}
	return llamaVersionStr
}

// ResetLlamaServerVersionCache drops the cached version so the next call reads
// llama-server again. Callers that replace the binary in place use it to keep
// the reported version in step with the file on disk.
func ResetLlamaServerVersionCache() {
	llamaVersionMu.Lock()
	defer llamaVersionMu.Unlock()
	llamaVersionStr = ""
	llamaVersionReady = false
}

func resolveLlamaServerVersion() string {
	binary := findLlamaBinary()
	if binary == "" {
		return ""
	}
	// Probe with the same library search path the server gets at start-up:
	// Linux does not load a co-located .so unless the binary carries an $ORIGIN
	// RUNPATH, so a runtime installed without patchelf would report no build id
	// at all and alignment would silently never run.
	cmd := exec.Command(binary, "--version")
	cmd.Env = appendLlamaLibPath(os.Environ(), filepath.Dir(binary))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return parseLlamaServerVersion(string(out))
}

// LlamaServerBuildID returns the llama.cpp build number reported by the
// installed llama-server, or 0 when it cannot be determined. Release tags use
// the same number, so "b11541" matches a build id of 11541.
func LlamaServerBuildID() int {
	return parseLlamaServerBuildID(LlamaServerVersion())
}

// parseLlamaServerBuildID extracts the build id from the identifier returned by
// parseLlamaServerVersion. Builds since b11541 print
// "0.6.0-dev (build 11541, commit f2918cabb)" and older ones print
// "4868 (c2d1a7f83)"; both carry the id in the parenthesised group. A version
// without that group yields 0, so a "0.6.0-dev" style line cannot leak a
// version digit as a build id.
func parseLlamaServerBuildID(version string) int {
	if m := llamaBuildInParensPattern.FindStringSubmatch(version); m != nil {
		if id, err := strconv.Atoi(m[1]); err == nil {
			return id
		}
	}
	if m := llamaBuildLeadingPattern.FindStringSubmatch(version); m != nil {
		if id, err := strconv.Atoi(m[1]); err == nil {
			return id
		}
	}
	return 0
}

// parseLlamaServerVersion extracts the identifier from llama-server --version
// output. The version line looks like "version: 0.1.2-dev (build 10549, commit
// b2e5e9b28)" and may be followed by build toolchain lines.
func parseLlamaServerVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "version:"); ok {
			if v := strings.TrimSpace(rest); v != "" {
				return v
			}
		}
	}
	return ""
}
