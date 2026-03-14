//go:build !windows

package builtins_test

import (
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func expectedArchMachine(tb testing.TB) string {
	tb.Helper()

	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		tb.Fatalf("unix.Uname() error = %v", err)
	}
	return strings.TrimSpace(unix.ByteSliceToString(uts.Machine[:]))
}
