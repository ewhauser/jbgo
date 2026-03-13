//go:build !windows

package commands

import (
	"strings"

	"golang.org/x/sys/unix"
)

func archMachine() (string, error) {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return "", err
	}
	return strings.TrimSpace(unix.ByteSliceToString(uts.Machine[:])), nil
}
