package builtins

import (
	"runtime"
	"strings"
)

const hostOSEnvKey = "GBASH_HOST_OS"

func bashHelpPlatform(inv *Invocation) string {
	arch := archMachine(inv)
	goos := helpHostOS(inv)
	switch goos {
	case "darwin":
		release := unameEnvValue(nil, unameReleaseEnvKey)
		if inv != nil {
			release = unameEnvValue(inv.Env, unameReleaseEnvKey)
		}
		return arch + "-apple-darwin" + release
	case "linux":
		return arch + "-unknown-linux-gnu"
	case "freebsd":
		return arch + "-unknown-freebsd"
	case "openbsd":
		return arch + "-unknown-openbsd"
	case "netbsd":
		return arch + "-unknown-netbsd"
	case "js":
		return arch + "-unknown-js"
	default:
		return arch + "-unknown-" + goos
	}
}

func helpHostOS(inv *Invocation) string {
	if inv != nil && inv.Env != nil {
		if goos := strings.TrimSpace(inv.Env[hostOSEnvKey]); goos != "" {
			return goos
		}
	}
	return runtime.GOOS
}
