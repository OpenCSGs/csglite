//go:build linux

package deviceid

import (
	"errors"
	"os"
	"strings"
)

// machineIDFiles are tried in order. A container may expose the host id in the
// first file; the DMI UUID is the last resort because some distributions
// restrict it to root.
var machineIDFiles = []string{
	"/etc/machine-id",
	"/var/lib/dbus/machine-id",
	"/sys/class/dmi/id/product_uuid",
}

func machineID() (string, error) {
	var lastErr error = errors.New("no machine id file is readable")
	for _, path := range machineIDFiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			continue
		}
		value := strings.TrimSpace(string(raw))
		if value == "" {
			lastErr = errors.New(path + " is empty")
			continue
		}
		return value, nil
	}
	return "", lastErr
}
