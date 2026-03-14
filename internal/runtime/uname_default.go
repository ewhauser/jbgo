package runtime

import goruntime "runtime"

const (
	defaultUnameNodename = "gbash"
	defaultUnameRelease  = "unknown"
	defaultUnameVersion  = "unknown"
)

func defaultUnameKernelName() string {
	switch goruntime.GOOS {
	case "android", "linux":
		return "Linux"
	case "darwin", "ios":
		return "Darwin"
	case "windows":
		return "Windows_NT"
	case "plan9":
		return "Plan 9"
	default:
		return defaultUnameOperatingSystem()
	}
}

func defaultUnameOperatingSystem() string {
	switch goruntime.GOOS {
	case "aix":
		return "AIX"
	case "android":
		return "Android"
	case "darwin":
		return "Darwin"
	case "dragonfly":
		return "DragonFly"
	case "freebsd":
		return "FreeBSD"
	case "fuchsia":
		return "Fuchsia"
	case "illumos":
		return "illumos"
	case "ios":
		return "Darwin"
	case "js":
		return "JavaScript"
	case "linux":
		return "GNU/Linux"
	case "netbsd":
		return "NetBSD"
	case "openbsd":
		return "OpenBSD"
	case "plan9":
		return "Plan 9"
	case "redox":
		return "Redox"
	case "solaris":
		return "SunOS"
	case "windows":
		return "MS/Windows"
	default:
		return goruntime.GOOS
	}
}
