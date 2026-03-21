//go:build darwin

package interp

var platformTrapSignals = []trapSignalInfo{
	{number: 1, name: "SIGHUP", aliases: []string{"HUP"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 2, name: "SIGINT", aliases: []string{"INT"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 3, name: "SIGQUIT", aliases: []string{"QUIT"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 6, name: "SIGABRT", aliases: []string{"ABRT", "IOT"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 9, name: "SIGKILL", aliases: []string{"KILL"}, catchable: false, defaultDisposition: trapDefaultTerminate},
	{number: 10, name: "SIGBUS", aliases: []string{"BUS"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 12, name: "SIGSYS", aliases: []string{"SYS"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 13, name: "SIGPIPE", aliases: []string{"PIPE"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 14, name: "SIGALRM", aliases: []string{"ALRM"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 15, name: "SIGTERM", aliases: []string{"TERM"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 16, name: "SIGURG", aliases: []string{"URG"}, catchable: true, defaultDisposition: trapDefaultIgnore},
	{number: 17, name: "SIGSTOP", aliases: []string{"STOP"}, catchable: false, defaultDisposition: trapDefaultStop},
	{number: 18, name: "SIGTSTP", aliases: []string{"TSTP"}, catchable: true, defaultDisposition: trapDefaultStop},
	{number: 19, name: "SIGCONT", aliases: []string{"CONT"}, catchable: true, defaultDisposition: trapDefaultIgnore},
	{number: 20, name: "SIGCHLD", aliases: []string{"CHLD", "CLD"}, catchable: true, defaultDisposition: trapDefaultIgnore},
	{number: 21, name: "SIGTTIN", aliases: []string{"TTIN"}, catchable: true, defaultDisposition: trapDefaultStop},
	{number: 22, name: "SIGTTOU", aliases: []string{"TTOU"}, catchable: true, defaultDisposition: trapDefaultStop},
	{number: 24, name: "SIGXCPU", aliases: []string{"XCPU"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 25, name: "SIGXFSZ", aliases: []string{"XFSZ"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 26, name: "SIGVTALRM", aliases: []string{"VTALRM"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 27, name: "SIGPROF", aliases: []string{"PROF"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 28, name: "SIGWINCH", aliases: []string{"WINCH"}, catchable: true, defaultDisposition: trapDefaultIgnore},
	{number: 30, name: "SIGUSR1", aliases: []string{"USR1"}, catchable: true, defaultDisposition: trapDefaultTerminate},
	{number: 31, name: "SIGUSR2", aliases: []string{"USR2"}, catchable: true, defaultDisposition: trapDefaultTerminate},
}
