package builtins

import (
	"strings"

	"github.com/ewhauser/gbash/host"
)

const hostOSEnvKey = "GBASH_HOST_OS"

func bashHelpPlatform(inv *Invocation) string {
	arch := archMachine(inv)
	goos := helpHostOS(inv)
	switch goos {
	case host.OSDarwin:
		release := unameEnvValue(nil, unameReleaseEnvKey)
		if inv != nil {
			release = unameEnvValue(inv.Env, unameReleaseEnvKey)
		}
		return arch + "-apple-darwin" + release
	case host.OSLinux:
		return arch + "-unknown-linux-gnu"
	case host.OSFreeBSD:
		return arch + "-unknown-freebsd"
	case host.OSOpenBSD:
		return arch + "-unknown-openbsd"
	case host.OSNetBSD:
		return arch + "-unknown-netbsd"
	case host.OSJS:
		return arch + "-unknown-js"
	default:
		return arch + "-unknown-" + goos.String()
	}
}

func helpHostOS(inv *Invocation) host.OS {
	if inv != nil && inv.Env != nil {
		if goos := strings.TrimSpace(inv.Env[hostOSEnvKey]); goos != "" {
			return host.OS(goos)
		}
	}
	return host.CurrentOS()
}
