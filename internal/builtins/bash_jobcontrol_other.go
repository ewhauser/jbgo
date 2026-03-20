//go:build !linux

package builtins

func bashInteractiveJobControlWarning(_ string, _ *Invocation) string {
	return ""
}
