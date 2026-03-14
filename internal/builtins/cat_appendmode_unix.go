//go:build !windows && !js

package builtins

import "golang.org/x/sys/unix"

func catHostAppendMode(file catHostHandle) bool {
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return false
	}
	return flags&unix.O_APPEND != 0
}
