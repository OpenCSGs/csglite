//go:build darwin

package deviceid

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var platformUUIDPattern = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

func machineID() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", err
	}
	match := platformUUIDPattern.FindStringSubmatch(string(out))
	if len(match) < 2 {
		return "", errors.New("ioreg output has no IOPlatformUUID")
	}
	return strings.TrimSpace(match[1]), nil
}
