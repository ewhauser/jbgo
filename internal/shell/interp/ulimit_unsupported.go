//go:build !unix || js || wasip1

package interp

func ulimitBuiltinLines(mode ulimitBuiltinMode) []string {
	return nil
}
