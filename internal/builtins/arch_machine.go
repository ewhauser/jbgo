package builtins

import (
	"runtime"
	"strings"
)

const archEnvKey = "GBASH_ARCH"

func archMachine(inv *Invocation) string {
	if inv != nil && inv.Env != nil {
		if machine := strings.TrimSpace(inv.Env[archEnvKey]); machine != "" {
			return machine
		}
	}
	return archMachineFromGOARCH()
}

func archMachineFromGOARCH() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "386":
		return "i686"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}
