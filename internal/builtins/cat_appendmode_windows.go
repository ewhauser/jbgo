//go:build windows

package builtins

func catHostAppendMode(file catHostHandle) bool {
	// Windows host handles do not expose their open flags through the standard
	// library, so fall back to non-append behavior for external redirections.
	return false
}
