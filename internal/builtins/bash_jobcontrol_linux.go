//go:build linux

package builtins

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

func bashInteractiveJobControlWarning(name string, inv *Invocation) string {
	if _, ok := ttyTerminalPath(inv); ok {
		return ""
	}
	shellName := strings.TrimSpace(name)
	if shellName == "" {
		return ""
	}
	pgrp := unix.Getpgrp()
	if pgrp <= 0 {
		return ""
	}
	return fmt.Sprintf("%s: cannot set terminal process group (%d): Inappropriate ioctl for device\n", shellName, pgrp)
}
