//go:build windows

package runtime

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func loadExpectedUnameInfo(tb testing.TB) expectedUnameInfo {
	tb.Helper()

	nodename, err := os.Hostname()
	if err != nil {
		tb.Fatalf("os.Hostname() error = %v", err)
	}
	version := windows.RtlGetVersion()

	return expectedUnameInfo{
		kernelName:      "Windows_NT",
		nodename:        nodename,
		kernelRelease:   fmt.Sprintf("%d.%d", version.MajorVersion, version.MinorVersion),
		kernelVersion:   fmt.Sprintf("Build %d", version.BuildNumber),
		machine:         expectedArchMachine(tb),
		operatingSystem: "MS/Windows",
	}
}
