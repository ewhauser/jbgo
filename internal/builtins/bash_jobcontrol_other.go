//go:build !linux

package builtins

import "context"

func bashInteractiveJobControlWarning(_ context.Context, _ string, _ *Invocation) string {
	return ""
}
