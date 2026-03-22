//go:build unix

package runtime

import "golang.org/x/sys/unix"

//nolint:forbidigo // runtime host plumbing needs the current process group to seed interactive shell metadata.
func currentVirtualProcessGroup() int {
	return unix.Getpgrp()
}
