//go:build !unix

package host

import "os"

func systemExecutionMeta() ExecutionMeta {
	return ExecutionMeta{
		PID:  os.Getpid(),
		PPID: os.Getppid(),
	}
}

func systemUnameRelease() string {
	return "unknown"
}

func systemUnameVersion() string {
	return "unknown"
}
