package host

import (
	"context"
	"io"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
)

// NewSystem returns an [Adapter] that reflects the current process and OS.
//
// The returned adapter is opt-in. gbash does not use it by default; callers
// must pass it explicitly via gbash.Config.Host or gbash.WithHost(...).
//
// NewSystem derives:
//
//   - Defaults from the current process environment, user information, and
//     common OS helpers such as os.UserHomeDir
//   - Platform from runtime.GOOS, runtime.GOARCH, hostname, and uname-style
//     OS metadata where available
//   - ExecutionMeta from the current process PID, PPID, and process group when
//     supported on the current platform
//   - NewPipe from os.Pipe
//
// This is the supported public adapter for embedders that want the shell’s
// host-facing behavior to track the underlying process and operating system.
func NewSystem() Adapter {
	return &systemAdapter{platform: systemPlatform()}
}

type systemAdapter struct {
	platform Platform
}

func (s *systemAdapter) Defaults(_ context.Context) (Defaults, error) {
	return Defaults{
		Env: systemDefaultsEnv(),
	}, nil
}

func (s *systemAdapter) Platform() Platform {
	return clonePlatform(&s.platform)
}

func (s *systemAdapter) ExecutionMeta(context.Context) (ExecutionMeta, error) {
	return systemExecutionMeta(), nil
}

func (s *systemAdapter) NewPipe() (io.ReadCloser, io.WriteCloser, error) {
	return os.Pipe()
}

func systemPlatform() Platform {
	machine := systemArchMachine()
	return Platform{
		OS:                   runtime.GOOS,
		Arch:                 machine,
		OSType:               systemOSType(runtime.GOOS),
		EnvCaseInsensitive:   runtime.GOOS == "windows",
		PathExtensions:       defaultPathExtensions(runtime.GOOS),
		RequireExecutableBit: runtime.GOOS != "windows",
		Uname: Uname{
			SysName:         systemKernelName(runtime.GOOS),
			NodeName:        systemNodeName(),
			Release:         systemUnameRelease(),
			Version:         systemUnameVersion(),
			Machine:         machine,
			OperatingSystem: systemOperatingSystem(runtime.GOOS),
		},
	}
}

func systemDefaultsEnv() map[string]string {
	env := map[string]string{}

	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		env["HOME"] = strings.TrimSpace(home)
	}
	copyIfPresent(env, "HOME")
	copyIfPresent(env, "PATH")
	copyIfPresent(env, "SHELL")
	copyIfPresent(env, "TMPDIR")
	copyIfPresent(env, "TTY")
	copyIfPresent(env, "USER")
	copyIfPresent(env, "LOGNAME")
	copyIfPresent(env, "GROUP")
	copyIfPresent(env, "GROUPS")
	copyIfPresent(env, "UID")
	copyIfPresent(env, "EUID")
	copyIfPresent(env, "GID")
	copyIfPresent(env, "EGID")

	if current, err := user.Current(); err == nil && current != nil {
		if strings.TrimSpace(env["HOME"]) == "" && strings.TrimSpace(current.HomeDir) != "" {
			env["HOME"] = strings.TrimSpace(current.HomeDir)
		}
		if strings.TrimSpace(env["USER"]) == "" && strings.TrimSpace(current.Username) != "" {
			env["USER"] = strings.TrimSpace(current.Username)
		}
		if strings.TrimSpace(env["LOGNAME"]) == "" && strings.TrimSpace(current.Username) != "" {
			env["LOGNAME"] = strings.TrimSpace(current.Username)
		}
		if _, err := strconv.ParseUint(strings.TrimSpace(current.Uid), 10, 32); err == nil {
			env["UID"] = strings.TrimSpace(current.Uid)
			if strings.TrimSpace(env["EUID"]) == "" {
				env["EUID"] = strings.TrimSpace(current.Uid)
			}
		}
		if _, err := strconv.ParseUint(strings.TrimSpace(current.Gid), 10, 32); err == nil {
			env["GID"] = strings.TrimSpace(current.Gid)
			if strings.TrimSpace(env["EGID"]) == "" {
				env["EGID"] = strings.TrimSpace(current.Gid)
			}
			if strings.TrimSpace(env["GROUPS"]) == "" {
				env["GROUPS"] = strings.TrimSpace(current.Gid)
			}
		}
		if strings.TrimSpace(env["GROUP"]) == "" && strings.TrimSpace(env["USER"]) != "" {
			env["GROUP"] = strings.TrimSpace(env["USER"])
		}
		if strings.TrimSpace(env["GROUPS"]) == "" {
			if ids, err := current.GroupIds(); err == nil && len(ids) > 0 {
				var numeric []string
				for _, id := range ids {
					id = strings.TrimSpace(id)
					if id == "" {
						continue
					}
					if _, err := strconv.ParseUint(id, 10, 32); err != nil {
						continue
					}
					numeric = append(numeric, id)
				}
				if len(numeric) > 0 {
					env["GROUPS"] = strings.Join(numeric, " ")
				}
			}
		}
	}

	if strings.TrimSpace(env["SHELL"]) == "" {
		env["SHELL"] = "/bin/sh"
	}
	if strings.TrimSpace(env["GROUP"]) == "" && strings.TrimSpace(env["USER"]) != "" {
		env["GROUP"] = strings.TrimSpace(env["USER"])
	}
	if strings.TrimSpace(env["GROUPS"]) == "" && strings.TrimSpace(env["GID"]) != "" {
		env["GROUPS"] = strings.TrimSpace(env["GID"])
	}

	return env
}

func copyIfPresent(dst map[string]string, key string) {
	if dst == nil {
		return
	}
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		dst[key] = value
	}
}

func defaultPathExtensions(goos string) []string {
	if goos != "windows" {
		return nil
	}
	return []string{".com", ".exe", ".bat", ".cmd"}
}

func systemKernelName(goos string) string {
	switch goos {
	case "android", "linux":
		return "Linux"
	case "darwin", "ios":
		return "Darwin"
	case "windows":
		return "Windows_NT"
	case "plan9":
		return "Plan 9"
	default:
		return systemOperatingSystem(goos)
	}
}

func systemOperatingSystem(goos string) string {
	switch goos {
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
		return goos
	}
}

func systemOSType(goos string) string {
	switch goos {
	case "linux":
		return "linux-gnu"
	case "darwin":
		return "darwin"
	case "windows":
		return "msys"
	case "freebsd":
		return "freebsd"
	case "openbsd":
		return "openbsd"
	case "netbsd":
		return "netbsd"
	default:
		return goos
	}
}

func systemArchMachine() string {
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

func systemNodeName() string {
	if name, err := os.Hostname(); err == nil {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	return "localhost"
}

func clonePlatform(platform *Platform) Platform {
	if platform == nil {
		return Platform{}
	}
	out := *platform
	out.PathExtensions = append([]string(nil), platform.PathExtensions...)
	return out
}
