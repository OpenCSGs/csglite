//go:build windows

package deviceid

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const machineGUIDPath = `SOFTWARE\Microsoft\Cryptography`

func machineID() (string, error) {
	// The 64-bit view keeps the same GUID for 32-bit and 64-bit builds of
	// CSGLite on the same machine.
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, machineGUIDPath, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("MachineGuid")
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("MachineGuid is empty")
	}
	return value, nil
}
