//go:build !darwin && !linux

package interp

var platformTrapSignals = []trapSignalInfo{
	{number: 1, name: "SIGHUP", aliases: []string{"HUP"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 2, name: "SIGINT", aliases: []string{"INT"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 3, name: "SIGQUIT", aliases: []string{"QUIT"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 9, name: "SIGKILL", aliases: []string{"KILL"}, catchable: false, defaultDisposition: trapDefaultTerminate},
	{number: 10, name: "SIGUSR1", aliases: []string{"USR1"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 12, name: "SIGUSR2", aliases: []string{"USR2"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 15, name: "SIGTERM", aliases: []string{"TERM"}, catchable: true, defaultDisposition: trapDefaultTerminate},
}
