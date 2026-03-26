//go:build !linux

package cli

import "os"

func inheritedOSFilePath(file *os.File) (string, bool) {
	return "", false
}

func inheritedOSFileFlags(file *os.File) int {
	return 0
}
