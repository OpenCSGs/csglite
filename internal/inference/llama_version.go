package inference

import (
	"os/exec"
	"strings"
	"sync"
)

var (
	llamaVersionOnce sync.Once
	llamaVersionStr  string
)

// LlamaServerVersion returns the version reported by the installed
// llama-server binary, for example "0.1.2-dev (build 10549, commit b2e5e9b28)".
// It is read once and cached; an empty string means no llama-server could be
// resolved or its version could not be determined.
func LlamaServerVersion() string {
	llamaVersionOnce.Do(func() {
		llamaVersionStr = resolveLlamaServerVersion()
	})
	return llamaVersionStr
}

func resolveLlamaServerVersion() string {
	binary := findLlamaBinary()
	if binary == "" {
		return ""
	}
	out, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return parseLlamaServerVersion(string(out))
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
