//go:build !unix

package interp_test

type hostWaitStatus struct{}

func (hostWaitStatus) Signaled() bool { return false }
func (hostWaitStatus) Signal() int    { return 0 }
