package api

import (
	"fmt"
	"strings"
	"time"
)

const KeepAliveForever = time.Duration(-1)

// ParseKeepAlive parses the API/CLI keep-alive string.
// Empty input means "use the server default".
func ParseKeepAlive(raw string) (time.Duration, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false, nil
	}
	if value == "-1" {
		return KeepAliveForever, true, nil
	}

	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, true, fmt.Errorf("must be a duration like 30s, 5m, or 1h, or -1 for forever")
	}
	if d < 0 {
		return 0, true, fmt.Errorf("only -1 is supported for infinite keep-alive")
	}
	return d, true, nil
}

// FormatKeepAlive renders a keep-alive duration the way ParseKeepAlive accepts
// it, so a value read back from the API can be sent straight back.
func FormatKeepAlive(d time.Duration) string {
	if d < 0 {
		return "-1"
	}
	return d.String()
}
