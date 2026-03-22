//go:build !unix

package runtime

func currentVirtualProcessGroup() int {
	return 0
}
