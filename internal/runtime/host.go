package runtime

import (
	"context"
	"io"
	"strings"

	"github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/internal/shell/interp"
)

const (
	hostOSEnvKey     = "GBASH_HOST_OS"
	hostOSTypeEnvKey = "GBASH_OSTYPE"
)

func runtimeBaseEnv(ctx context.Context, adapter host.Adapter) (map[string]string, error) {
	if adapter == nil {
		adapter = newVirtualHost()
	}
	defaults, err := adapter.Defaults(ctx)
	if err != nil {
		return nil, err
	}
	env := copyStringMap(defaults.Env)
	if env == nil {
		env = make(map[string]string)
	}
	projectPlatformEnv(env, adapter.Platform())
	if strings.TrimSpace(env["GBASH_UMASK"]) == "" {
		env["GBASH_UMASK"] = "0022"
	}
	return env, nil
}

func hostPlatform(adapter host.Adapter) host.Platform {
	if adapter == nil {
		return normalizeHostPlatform(host.Platform{})
	}
	return normalizeHostPlatform(adapter.Platform())
}

//nolint:gocritic // host.Platform is copied intentionally because callers often construct it by value.
func projectPlatformEnv(env map[string]string, raw host.Platform) {
	if env == nil {
		return
	}
	platform := normalizeHostPlatform(raw)
	setProjectedEnv(env, "GBASH_ARCH", platform.Arch)
	setProjectedEnv(env, hostOSEnvKey, platform.OS.String())
	setProjectedEnv(env, hostOSTypeEnvKey, platform.OSType)
	setProjectedEnv(env, "OSTYPE", platform.OSType)
	setProjectedEnv(env, "GBASH_UNAME_SYSNAME", platform.Uname.SysName)
	setProjectedEnv(env, "GBASH_UNAME_NODENAME", platform.Uname.NodeName)
	setProjectedEnv(env, "GBASH_UNAME_RELEASE", platform.Uname.Release)
	setProjectedEnv(env, "GBASH_UNAME_VERSION", platform.Uname.Version)
	setProjectedEnv(env, "GBASH_UNAME_MACHINE", platform.Uname.Machine)
	setProjectedEnv(env, "GBASH_UNAME_OPERATING_SYSTEM", platform.Uname.OperatingSystem)
}

func setProjectedEnv(env map[string]string, key, value string) {
	if strings.TrimSpace(env[key]) != "" {
		return
	}
	env[key] = value
}

//nolint:gocritic // host.Platform is copied intentionally because normalization returns a derived value.
func normalizeHostPlatform(raw host.Platform) host.Platform {
	platform := raw
	if platform.OS == "" {
		platform.OS = host.CurrentOS()
	}
	defaults := platform.OS.PlatformDefaults()
	if platform.Arch == "" {
		platform.Arch = defaultArchMachine()
	}
	if platform.OSType == "" {
		platform.OSType = defaults.OSType
	}
	if defaults.EnvCaseInsensitive {
		platform.EnvCaseInsensitive = true
	}
	if len(platform.PathExtensions) == 0 {
		platform.PathExtensions = append([]string(nil), defaults.PathExtensions...)
	}
	if defaults.RequireExecutableBit {
		platform.RequireExecutableBit = true
	}
	if platform.Uname.SysName == "" {
		platform.Uname.SysName = defaults.KernelName
	}
	if platform.Uname.NodeName == "" {
		platform.Uname.NodeName = defaultUnameNodename
	}
	if platform.Uname.Release == "" {
		platform.Uname.Release = defaultUnameRelease
	}
	if platform.Uname.Version == "" {
		platform.Uname.Version = defaultUnameVersion
	}
	if platform.Uname.Machine == "" {
		platform.Uname.Machine = platform.Arch
	}
	if platform.Uname.OperatingSystem == "" {
		platform.Uname.OperatingSystem = defaults.OperatingSystem
	}
	return platform
}

type virtualHost struct {
	platform host.Platform
}

func newVirtualHost() host.Adapter {
	return &virtualHost{
		platform: normalizeHostPlatform(host.Platform{
			OS:   host.CurrentOS(),
			Arch: defaultArchMachine(),
			Uname: host.Uname{
				NodeName: defaultUnameNodename,
				Release:  defaultUnameRelease,
				Version:  defaultUnameVersion,
				Machine:  defaultArchMachine(),
			},
		}),
	}
}

func (v *virtualHost) Defaults(context.Context) (host.Defaults, error) {
	return host.Defaults{
		Env: map[string]string{
			"HOME":    defaultHomeDir,
			"PATH":    defaultPath,
			"USER":    defaultUser,
			"LOGNAME": defaultUser,
			"GROUP":   defaultUser,
			"GROUPS":  defaultGID,
			"UID":     defaultUID,
			"EUID":    defaultUID,
			"GID":     defaultGID,
			"EGID":    defaultGID,
			"SHELL":   "/bin/sh",
		},
	}, nil
}

func (v *virtualHost) Platform() host.Platform {
	return v.platform
}

func (*virtualHost) ExecutionMeta(context.Context) (host.ExecutionMeta, error) {
	return host.ExecutionMeta{
		PID:          1,
		PPID:         0,
		ProcessGroup: currentVirtualProcessGroup(),
	}, nil
}

func (*virtualHost) NewPipe() (io.ReadCloser, io.WriteCloser, error) {
	reader, writer := interp.NewVirtualPipe()
	return reader, writer, nil
}
