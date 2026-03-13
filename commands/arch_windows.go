//go:build windows

package commands

import "runtime"

func archMachine() (string, error) {
	return runtime.GOARCH, nil
}
