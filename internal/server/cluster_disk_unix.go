//go:build !windows

package server

import (
	"golang.org/x/sys/unix"
)

// diskUsage reports the total and free bytes of the volume holding path.
func diskUsage(path string) (total, free uint64, err error) {
	if path == "" {
		path = "."
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return uint64(st.Blocks) * bsize, uint64(st.Bavail) * bsize, nil
}
