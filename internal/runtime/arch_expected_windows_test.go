//go:build windows

package runtime

import (
	"runtime"
	"testing"
)

func expectedArchMachine(tb testing.TB) string {
	tb.Helper()
	return runtime.GOARCH
}
