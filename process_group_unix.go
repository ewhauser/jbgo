//go:build unix

package gbash

import (
	"context"

	"github.com/ewhauser/gbash/internal/shellstate"
	"golang.org/x/sys/unix"
)

func withHostProcessGroup(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return shellstate.WithProcessGroup(ctx, unix.Getpgrp())
}
