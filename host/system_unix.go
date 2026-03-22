//go:build unix

package host

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func systemExecutionMeta() ExecutionMeta {
	return ExecutionMeta{
		PID:          os.Getpid(),
		PPID:         os.Getppid(),
		ProcessGroup: unix.Getpgrp(),
	}
}

func systemUnameRelease() string {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		return strings.TrimSpace(unix.ByteSliceToString(uts.Release[:]))
	}
	return "unknown"
}

func systemUnameVersion() string {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		return strings.TrimSpace(unix.ByteSliceToString(uts.Version[:]))
	}
	return "unknown"
}
