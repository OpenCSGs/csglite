//go:build windows

package server

import (
	"golang.org/x/sys/windows"
)

// diskUsage reports the total and free bytes of the volume holding path.
func diskUsage(path string) (total, free uint64, err error) {
	if path == "" {
		path = "."
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var freeAvail, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeAvail, &totalBytes, &totalFree); err != nil {
		return 0, 0, err
	}
	return totalBytes, freeAvail, nil
}
