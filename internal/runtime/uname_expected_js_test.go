//go:build js

package runtime

import "testing"

func loadExpectedUnameInfo(tb testing.TB) expectedUnameInfo {
	tb.Helper()
	return expectedUnameInfo{
		kernelName:      "JavaScript",
		nodename:        "localhost",
		kernelRelease:   "unknown",
		kernelVersion:   "unknown",
		machine:         expectedArchMachine(tb),
		operatingSystem: "JavaScript",
	}
}
