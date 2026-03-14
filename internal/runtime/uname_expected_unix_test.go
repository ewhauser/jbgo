//go:build !windows && !js

package runtime

import (
	goruntime "runtime"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func loadExpectedUnameInfo(tb testing.TB) expectedUnameInfo {
	tb.Helper()

	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		tb.Fatalf("unix.Uname() error = %v", err)
	}

	return expectedUnameInfo{
		kernelName:      strings.TrimSpace(unix.ByteSliceToString(uts.Sysname[:])),
		nodename:        strings.TrimSpace(unix.ByteSliceToString(uts.Nodename[:])),
		kernelRelease:   strings.TrimSpace(unix.ByteSliceToString(uts.Release[:])),
		kernelVersion:   strings.TrimSpace(unix.ByteSliceToString(uts.Version[:])),
		machine:         expectedArchMachine(tb),
		operatingSystem: expectedUnameOperatingSystem(),
	}
}

func expectedUnameOperatingSystem() string {
	switch goruntime.GOOS {
	case "aix":
		return "AIX"
	case "android":
		return "Android"
	case "darwin":
		return "Darwin"
	case "dragonfly":
		return "DragonFly"
	case "freebsd":
		return "FreeBSD"
	case "fuchsia":
		return "Fuchsia"
	case "illumos":
		return "illumos"
	case "ios":
		return "Darwin"
	case "linux":
		return "GNU/Linux"
	case "netbsd":
		return "NetBSD"
	case "openbsd":
		return "OpenBSD"
	case "plan9":
		return "Plan 9"
	case "redox":
		return "Redox"
	case "solaris":
		return "SunOS"
	default:
		return goruntime.GOOS
	}
}
