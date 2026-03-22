package host

import "runtime"

// OS identifies the logical operating-system semantics exposed by a [Platform].
//
// OS values generally follow Go's runtime.GOOS spellings so adapters can mirror
// build-host values when that is appropriate. Custom adapters may also use
// other string values when they need gbash to emulate a non-standard platform.
type OS string

// Known [OS] values used by gbash's built-in platform defaults.
const (
	OSAIX       OS = "aix"
	OSAndroid   OS = "android"
	OSDarwin    OS = "darwin"
	OSDragonfly OS = "dragonfly"
	OSFreeBSD   OS = "freebsd"
	OSFuchsia   OS = "fuchsia"
	OSIllumos   OS = "illumos"
	OSIOS       OS = "ios"
	OSJS        OS = "js"
	OSLinux     OS = "linux"
	OSNetBSD    OS = "netbsd"
	OSOpenBSD   OS = "openbsd"
	OSPlan9     OS = "plan9"
	OSRedox     OS = "redox"
	OSSolaris   OS = "solaris"
	OSWasip1    OS = "wasip1"
	OSWindows   OS = "windows"
)

// PlatformDefaults describes the default shell-visible platform semantics
// associated with an [OS] value.
type PlatformDefaults struct {
	// OSType is the default shell-visible OSTYPE value.
	OSType string

	// EnvCaseInsensitive reports whether variable names should default to
	// case-insensitive matching.
	EnvCaseInsensitive bool

	// PathExtensions is the default executable-suffix list used by command
	// lookup when a command name has no explicit extension.
	PathExtensions []string

	// RequireExecutableBit reports whether regular files must have an execute bit
	// to be treated as executable command files by default.
	RequireExecutableBit bool

	// KernelName is the default uname -s value for the OS.
	KernelName string

	// OperatingSystem is the default uname -o value for the OS.
	OperatingSystem string
}

// CurrentOS returns the current build host's runtime.GOOS value as an [OS].
func CurrentOS() OS {
	return OS(runtime.GOOS)
}

// String returns the OS value as its shell-visible string spelling.
func (os OS) String() string {
	return string(os)
}

// PlatformDefaults returns the default gbash platform semantics for os.
//
// The returned defaults are a starting point for adapters and runtime
// normalization. Adapters may override any of these values explicitly.
func (os OS) PlatformDefaults() PlatformDefaults {
	defaults := PlatformDefaults{
		OSType:               os.String(),
		RequireExecutableBit: true,
		KernelName:           os.String(),
		OperatingSystem:      os.String(),
	}

	switch os {
	case OSAndroid, OSLinux:
		defaults.OSType = "linux-gnu"
		defaults.KernelName = "Linux"
		defaults.OperatingSystem = "GNU/Linux"
	case OSDarwin, OSIOS:
		defaults.OSType = "darwin"
		defaults.KernelName = "Darwin"
		defaults.OperatingSystem = "Darwin"
	case OSWindows:
		defaults.OSType = "msys"
		defaults.EnvCaseInsensitive = true
		defaults.PathExtensions = []string{".com", ".exe", ".bat", ".cmd"}
		defaults.RequireExecutableBit = false
		defaults.KernelName = "Windows_NT"
		defaults.OperatingSystem = "MS/Windows"
	case OSFreeBSD:
		defaults.OSType = "freebsd"
		defaults.KernelName = "FreeBSD"
		defaults.OperatingSystem = "FreeBSD"
	case OSOpenBSD:
		defaults.OSType = "openbsd"
		defaults.KernelName = "OpenBSD"
		defaults.OperatingSystem = "OpenBSD"
	case OSNetBSD:
		defaults.OSType = "netbsd"
		defaults.KernelName = "NetBSD"
		defaults.OperatingSystem = "NetBSD"
	case OSAIX:
		defaults.KernelName = "AIX"
		defaults.OperatingSystem = "AIX"
	case OSDragonfly:
		defaults.KernelName = "DragonFly"
		defaults.OperatingSystem = "DragonFly"
	case OSFuchsia:
		defaults.KernelName = "Fuchsia"
		defaults.OperatingSystem = "Fuchsia"
	case OSIllumos:
		defaults.KernelName = "illumos"
		defaults.OperatingSystem = "illumos"
	case OSJS:
		defaults.KernelName = "JavaScript"
		defaults.OperatingSystem = "JavaScript"
	case OSPlan9:
		defaults.KernelName = "Plan 9"
		defaults.OperatingSystem = "Plan 9"
	case OSRedox:
		defaults.KernelName = "Redox"
		defaults.OperatingSystem = "Redox"
	case OSSolaris:
		defaults.KernelName = "SunOS"
		defaults.OperatingSystem = "SunOS"
	}

	defaults.PathExtensions = append([]string(nil), defaults.PathExtensions...)
	return defaults
}
