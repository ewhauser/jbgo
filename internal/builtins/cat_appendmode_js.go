//go:build js

package builtins

func catHostAppendMode(file catHostHandle) bool {
	// The browser/wasm target does not expose host fd flags, so treat redirected
	// handles as non-append.
	return false
}
