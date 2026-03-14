package builtins_test

import (
	"strings"
	"testing"
)

type expectedUnameInfo struct {
	kernelName      string
	nodename        string
	kernelRelease   string
	kernelVersion   string
	machine         string
	operatingSystem string
}

func (u *expectedUnameInfo) all() string {
	return strings.Join([]string{
		u.kernelName,
		u.nodename,
		u.kernelRelease,
		u.kernelVersion,
		u.machine,
		u.operatingSystem,
	}, " ")
}

func (u *expectedUnameInfo) allWithCompatibility() string {
	return strings.Join([]string{
		u.kernelName,
		u.nodename,
		u.kernelRelease,
		u.kernelVersion,
		u.machine,
		"unknown",
		"unknown",
		u.operatingSystem,
	}, " ")
}

func expectedUname(tb testing.TB) expectedUnameInfo {
	tb.Helper()
	info := loadExpectedUnameInfo(tb)
	env := defaultBaseEnv()
	if value := strings.TrimSpace(env["GBASH_UNAME_SYSNAME"]); value != "" {
		info.kernelName = value
	}
	if value := strings.TrimSpace(env["GBASH_UNAME_NODENAME"]); value != "" {
		info.nodename = value
	}
	if value := strings.TrimSpace(env["GBASH_UNAME_RELEASE"]); value != "" {
		info.kernelRelease = value
	}
	if value := strings.TrimSpace(env["GBASH_UNAME_VERSION"]); value != "" {
		info.kernelVersion = value
	}
	if value := strings.TrimSpace(env["GBASH_UNAME_MACHINE"]); value != "" {
		info.machine = value
	} else if value := strings.TrimSpace(env["GBASH_ARCH"]); value != "" {
		info.machine = value
	}
	if value := strings.TrimSpace(env["GBASH_UNAME_OPERATING_SYSTEM"]); value != "" {
		info.operatingSystem = value
	}
	return info
}
