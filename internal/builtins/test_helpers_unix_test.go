//go:build !windows && !js

package builtins_test

import (
	"strings"

	"golang.org/x/sys/unix"
)

func defaultArchMachine() string {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		if machine := strings.TrimSpace(unix.ByteSliceToString(uts.Machine[:])); machine != "" {
			return machine
		}
	}
	return archMachineFromGOARCH()
}
