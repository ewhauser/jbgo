//go:build !windows

package interp

import (
	"fmt"
	"syscall"
)

func shellTimesUsage() (selfUser, selfSystem, childUser, childSystem string, err error) {
	var self syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &self); err != nil {
		return "", "", "", "", err
	}
	var children syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children); err != nil {
		return "", "", "", "", err
	}
	return formatTimesTimeval(self.Utime), formatTimesTimeval(self.Stime),
		formatTimesTimeval(children.Utime), formatTimesTimeval(children.Stime), nil
}

func formatTimesTimeval(tv syscall.Timeval) string {
	totalMillis := tv.Sec*1000 + int64(tv.Usec)/1000
	minutes := totalMillis / 60000
	secondsMillis := totalMillis % 60000
	return fmt.Sprintf("%dm%d.%03ds", minutes, secondsMillis/1000, secondsMillis%1000)
}
