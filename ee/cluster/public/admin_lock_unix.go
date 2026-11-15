//go:build !windows

// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package public

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockAdminFile(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX) }
func unlockAdminFile(file *os.File)     { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }
