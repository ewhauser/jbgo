//go:build linux

package builtins

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shellstate"
)

func bashInteractiveJobControlWarning(ctx context.Context, name string, inv *Invocation) string {
	if _, ok := ttyTerminalPath(inv); ok {
		return ""
	}
	shellName := strings.TrimSpace(name)
	if shellName == "" {
		return ""
	}
	pgrp := bashInteractiveJobControlPID(ctx)
	return fmt.Sprintf("%s: cannot set terminal process group (%d): Inappropriate ioctl for device\n", shellName, pgrp)
}

func bashInteractiveJobControlPID(ctx context.Context) int {
	if lookup := shellstate.ShellVarLookupFromContext(ctx); lookup != nil {
		if raw, ok := lookup("BASHPID"); ok {
			if pid, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && pid > 0 {
				return pid
			}
		}
	}
	if family, ok := shellstate.SignalFamilyFromContext(ctx); ok {
		if family.ParentBASHPID > 0 {
			return family.ParentBASHPID
		}
		if family.StablePID > 0 {
			return family.StablePID
		}
	}
	return 1
}
