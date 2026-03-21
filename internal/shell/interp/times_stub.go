//go:build windows

package interp

import (
	"fmt"
	"runtime"
)

func shellTimesUsage() (selfUser, selfSystem, childUser, childSystem string, err error) {
	return "", "", "", "", fmt.Errorf("unsupported on %s", runtime.GOOS)
}
