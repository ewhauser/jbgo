package host

import (
	"context"
	"io"
)

// Adapter provides the shell-visible host contract consumed by gbash.
//
// Implementations may represent the real underlying process and OS, a test
// double, or any other logical host view that a caller wants gbash to expose.
//
// The runtime treats the adapter as the source of truth for platform and
// process semantics, then layers gbash-owned startup behavior on top. In
// particular:
//
//   - Defaults sits below gbash.Config.BaseEnv and per-request Env.
//   - Defaults is bypassed when an execution uses ReplaceEnv.
//   - Platform is used for shell-visible platform identity and lookup rules.
//   - ExecutionMeta is queried per execution and may vary between calls.
//   - NewPipe is used anywhere the shell needs a connected pipe pair.
type Adapter interface {
	// Defaults returns the host-derived base environment defaults contributed by
	// this adapter.
	//
	// These values are not the complete shell startup environment. gbash may
	// still project platform metadata and initialize shell-owned variables such
	// as PWD, IFS, or PS4 afterward.
	Defaults(context.Context) (Defaults, error)

	// Platform reports the logical host platform behavior exposed to the shell.
	//
	// This value should describe the platform semantics the adapter wants gbash
	// to emulate, even if that differs from the build host.
	Platform() Platform

	// ExecutionMeta reports per-execution process metadata used to seed shell
	// process identities and interactive process-group behavior.
	//
	// Unlike Platform, this method is expected to be execution-specific and may
	// return different values over time.
	ExecutionMeta(context.Context) (ExecutionMeta, error)

	// NewPipe creates the pipe primitive used for pipelines and process
	// substitutions.
	//
	// The returned endpoints must be connected to one another. The read side does
	// not need to implement any gbash-specific interface; the runtime will wrap
	// it as needed.
	NewPipe() (io.ReadCloser, io.WriteCloser, error)
}

// Defaults contains the environment defaults contributed by an [Adapter].
type Defaults struct {
	// Env is the host-provided base environment layer.
	//
	// gbash copies this map before use. Nil means that the adapter contributes no
	// base environment defaults.
	//
	// These values sit beneath gbash.Config.BaseEnv and per-request Env. They are
	// also bypassed by ReplaceEnv.
	Env map[string]string
}

// ExecutionMeta carries host process metadata used by the shell runtime.
type ExecutionMeta struct {
	// PID seeds the shell-visible process identifier reported as $$ and the
	// initial BASHPID root for the top-level execution.
	PID int

	// PPID seeds the readonly shell-visible PPID value.
	PPID int

	// ProcessGroup seeds the process-group identity used by interactive signal
	// and job-family wiring.
	//
	// A zero value means “no process-group metadata”.
	ProcessGroup int
}

// Platform describes the logical platform behavior exposed inside gbash.
//
// This is a logical shell platform, not a full host abstraction. It controls
// shell-visible OS/arch identity and selected lookup semantics, but it does not
// change gbash’s POSIX-shaped sandbox path syntax or its filesystem backend.
//
// Callers may leave fields empty and let gbash normalize them to sensible
// defaults, but explicit values are recommended for custom adapters so the
// contract remains clear and stable.
type Platform struct {
	// OS is the logical operating-system identity exposed by the shell.
	//
	// Use the typed [OS] constants such as [OSLinux], [OSDarwin], or
	// [OSWindows] when constructing platforms directly.
	OS OS

	// Arch is the shell-visible machine or architecture identifier, such as
	// "x86_64" or "aarch64".
	Arch string

	// OSType is the shell-visible value reported via OSTYPE.
	OSType string

	// EnvCaseInsensitive reports whether variable names should be matched
	// case-insensitively, as on Windows.
	EnvCaseInsensitive bool

	// PathExtensions lists executable suffixes that command lookup should try
	// when a command name has no explicit extension.
	//
	// This is typically used for Windows-style PATHEXT behavior. Nil or empty
	// means “do not apply extension-based lookup”.
	PathExtensions []string

	// RequireExecutableBit reports whether regular files must have an execute bit
	// to be treated as executable command files.
	RequireExecutableBit bool

	// Uname is the uname-style metadata projected into shell-visible commands and
	// compatibility surfaces.
	Uname Uname
}

// Uname describes the uname-style metadata surfaced by the shell runtime.
type Uname struct {
	// SysName is the kernel or system name reported by uname -s.
	SysName string

	// NodeName is the host or node name reported by uname -n and hostname.
	NodeName string

	// Release is the kernel release reported by uname -r.
	Release string

	// Version is the kernel version reported by uname -v.
	Version string

	// Machine is the machine hardware name reported by uname -m.
	Machine string

	// OperatingSystem is the operating-system name reported by uname -o.
	OperatingSystem string
}
