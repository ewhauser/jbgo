//go:build windows

package builtins_test

import (
	"runtime"
	"testing"
)

func expectedArchMachine(tb testing.TB) string {
	tb.Helper()
	return runtime.GOARCH
}
