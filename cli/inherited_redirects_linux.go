//go:build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func inheritedOSFilePath(file *os.File) (string, bool) {
	if file == nil {
		return "", false
	}
	info, err := file.Stat()
	if err != nil || info == nil || !info.Mode().IsRegular() {
		return "", false
	}

	target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if err != nil {
		return "", false
	}
	target = strings.TrimSuffix(target, " (deleted)")
	if !filepath.IsAbs(target) {
		return "", false
	}
	targetInfo, err := os.Stat(target)
	if err != nil || !os.SameFile(info, targetInfo) {
		return "", false
	}
	return filepath.Clean(target), true
}

func inheritedOSFileFlags(file *os.File) int {
	if file == nil {
		return 0
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return 0
	}
	return flags
}
