//go:build !darwin && !windows && !linux

package deviceid

import "errors"

func machineID() (string, error) {
	return "", errors.New("no machine id source for this platform")
}
