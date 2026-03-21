//go:build !windows && !js

package builtins

import "runtime"

func bashHelpPlatform(inv *Invocation) string {
	arch := archMachine(inv)
	switch runtime.GOOS {
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
	default:
		return arch + "-unknown-" + runtime.GOOS
	}
}
