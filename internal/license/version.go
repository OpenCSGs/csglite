package license

import (
	"strconv"
	"strings"
)

// versionSatisfies reports whether current is at least minimum. An empty
// minimum never constrains. A current version that does not start with a
// numeric component (such as the "dev" placeholder of local builds, or a git
// describe hash) is treated as unconstrained so development builds can load
// production licenses.
func versionSatisfies(current, minimum string) bool {
	minParts, ok := parseVersion(minimum)
	if !ok {
		return true
	}
	curParts, ok := parseVersion(current)
	if !ok {
		return true
	}
	for i := 0; i < 3; i++ {
		if curParts[i] != minParts[i] {
			return curParts[i] > minParts[i]
		}
	}
	return true
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return out, false
	}
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
