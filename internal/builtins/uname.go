package builtins

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"
)

const (
	unameUnknown               = "unknown"
	unameDefaultNodename       = "gbash"
	unameSysnameEnvKey         = "GBASH_UNAME_SYSNAME"
	unameNodenameEnvKey        = "GBASH_UNAME_NODENAME"
	unameReleaseEnvKey         = "GBASH_UNAME_RELEASE"
	unameVersionEnvKey         = "GBASH_UNAME_VERSION"
	unameMachineEnvKey         = "GBASH_UNAME_MACHINE"
	unameOperatingSystemEnvKey = "GBASH_UNAME_OPERATING_SYSTEM"
)

type Uname struct{}

func NewUname() *Uname {
	return &Uname{}
}

func (c *Uname) Name() string {
	return "uname"
}

func (c *Uname) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Uname) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Print certain system information.",
		Usage: "uname [OPTION]...",
		Options: []OptionSpec{
			{Name: "all", Short: 'a', Long: "all", Help: "Behave as though all of the options -mnrsvo were specified."},
			{Name: "kernel-name", Short: 's', Long: "kernel-name", Aliases: []string{"sysname"}, Help: "print the kernel name."},
			{Name: "nodename", Short: 'n', Long: "nodename", Help: "print the nodename (the nodename may be a name that the system is known by to a communications network)."},
			{Name: "kernel-release", Short: 'r', Long: "kernel-release", Aliases: []string{"release"}, Help: "print the operating system release."},
			{Name: "kernel-version", Short: 'v', Long: "kernel-version", Help: "print the operating system version."},
			{Name: "machine", Short: 'm', Long: "machine", Help: "print the machine hardware name."},
			{Name: "operating-system", Short: 'o', Long: "operating-system", Help: "print the operating system name."},
			{Name: "processor", Short: 'p', Long: "processor", Help: "print the processor type (non-portable)", Hidden: true},
			{Name: "hardware-platform", Short: 'i', Long: "hardware-platform", Help: "print the hardware platform (non-portable)", Hidden: true},
			{Name: "version", Short: 'V', Long: "version", Help: "output version information and exit"},
		},
		Parse: ParseConfig{
			InferLongOptions:  true,
			GroupShortOptions: true,
			AutoHelp:          true,
		},
	}
}

func (c *Uname) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}
	if positionals := matches.Positionals(); len(positionals) > 0 {
		return commandUsageError(inv, c.Name(), "extra operand %s", quoteGNUOperand(positionals[0]))
	}

	opts := unameOptions{
		all:              matches.Has("all"),
		kernelName:       matches.Has("kernel-name"),
		nodename:         matches.Has("nodename"),
		kernelRelease:    matches.Has("kernel-release"),
		kernelVersion:    matches.Has("kernel-version"),
		machine:          matches.Has("machine"),
		operatingSystem:  matches.Has("operating-system"),
		processor:        matches.Has("processor"),
		hardwarePlatform: matches.Has("hardware-platform"),
	}
	output := newUnameOutput(inv, opts)
	if _, err := fmt.Fprintln(inv.Stdout, output.display()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

type unameOptions struct {
	all              bool
	kernelName       bool
	nodename         bool
	kernelRelease    bool
	kernelVersion    bool
	machine          bool
	operatingSystem  bool
	processor        bool
	hardwarePlatform bool
}

type unameOutput struct {
	kernelName       string
	nodename         string
	kernelRelease    string
	kernelVersion    string
	machine          string
	processor        string
	hardwarePlatform string
	operatingSystem  string
}

func (o *unameOutput) display() string {
	parts := make([]string, 0, 8)
	for _, value := range []string{
		o.kernelName,
		o.nodename,
		o.kernelRelease,
		o.kernelVersion,
		o.machine,
		o.processor,
		o.hardwarePlatform,
		o.operatingSystem,
	} {
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, " ")
}

type unameInfo struct {
	kernelName      string
	nodename        string
	kernelRelease   string
	kernelVersion   string
	machine         string
	operatingSystem string
}

func newUnameOutput(inv *Invocation, opts unameOptions) unameOutput {
	info := currentUnameInfo(inv)

	none := !opts.all &&
		!opts.kernelName &&
		!opts.nodename &&
		!opts.kernelRelease &&
		!opts.kernelVersion &&
		!opts.machine &&
		!opts.operatingSystem &&
		!opts.processor &&
		!opts.hardwarePlatform

	out := unameOutput{}
	if opts.kernelName || opts.all || none {
		out.kernelName = info.kernelName
	}
	if opts.nodename || opts.all {
		out.nodename = info.nodename
	}
	if opts.kernelRelease || opts.all {
		out.kernelRelease = info.kernelRelease
	}
	if opts.kernelVersion || opts.all {
		out.kernelVersion = info.kernelVersion
	}
	if opts.machine || opts.all {
		out.machine = info.machine
	}
	if opts.processor {
		out.processor = unameUnknown
	}
	if opts.hardwarePlatform {
		out.hardwarePlatform = unameUnknown
	}
	if opts.operatingSystem || opts.all {
		out.operatingSystem = info.operatingSystem
	}
	return out
}

func currentUnameInfo(inv *Invocation) unameInfo {
	if info, complete := unameEnvInfo(inv); complete {
		return info
	}

	info := unameHostInfo()
	applyUnameEnvOverrides(inv, &info)
	if info.machine == "" {
		info.machine = archMachineFromGOARCH()
	}
	if info.operatingSystem == "" {
		info.operatingSystem = unameOperatingSystemName()
	}
	return info
}

func unameEnvInfo(inv *Invocation) (unameInfo, bool) {
	if inv == nil || inv.Env == nil {
		return unameInfo{}, false
	}

	info := unameInfo{
		kernelName:      unameEnvValue(inv.Env, unameSysnameEnvKey),
		nodename:        unameEnvValue(inv.Env, unameNodenameEnvKey),
		kernelRelease:   unameEnvValue(inv.Env, unameReleaseEnvKey),
		kernelVersion:   unameEnvValue(inv.Env, unameVersionEnvKey),
		machine:         unameEnvValue(inv.Env, unameMachineEnvKey),
		operatingSystem: unameEnvValue(inv.Env, unameOperatingSystemEnvKey),
	}
	if info.machine == "" {
		if machine := archMachine(inv); strings.TrimSpace(machine) != "" {
			info.machine = strings.TrimSpace(machine)
		}
	}
	complete := info.kernelName != "" &&
		info.nodename != "" &&
		info.kernelRelease != "" &&
		info.kernelVersion != "" &&
		info.machine != "" &&
		info.operatingSystem != ""
	return info, complete
}

func applyUnameEnvOverrides(inv *Invocation, info *unameInfo) {
	if info == nil || inv == nil || inv.Env == nil {
		return
	}
	if value := unameEnvValue(inv.Env, unameSysnameEnvKey); value != "" {
		info.kernelName = value
	}
	if value := unameEnvValue(inv.Env, unameNodenameEnvKey); value != "" {
		info.nodename = value
	}
	if value := unameEnvValue(inv.Env, unameReleaseEnvKey); value != "" {
		info.kernelRelease = value
	}
	if value := unameEnvValue(inv.Env, unameVersionEnvKey); value != "" {
		info.kernelVersion = value
	}
	if value := unameEnvValue(inv.Env, unameMachineEnvKey); value != "" {
		info.machine = value
	} else if machine := archMachine(inv); strings.TrimSpace(machine) != "" {
		info.machine = strings.TrimSpace(machine)
	}
	if value := unameEnvValue(inv.Env, unameOperatingSystemEnvKey); value != "" {
		info.operatingSystem = value
	}
}

func unameEnvValue(env map[string]string, key string) string {
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env[key])
}

func unameOperatingSystemName() string {
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

func unameKernelName() string {
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
		return unameOperatingSystemName()
	}
}

func unameHostInfo() unameInfo {
	return unameInfo{
		kernelName:      unameKernelName(),
		nodename:        unameDefaultNodename,
		kernelRelease:   unameUnknown,
		kernelVersion:   unameUnknown,
		machine:         archMachineFromGOARCH(),
		operatingSystem: unameOperatingSystemName(),
	}
}

var _ Command = (*Uname)(nil)
var _ SpecProvider = (*Uname)(nil)
var _ ParsedRunner = (*Uname)(nil)
