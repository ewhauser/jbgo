package gbash

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/internal/builtins"
	internalruntime "github.com/ewhauser/gbash/internal/runtime"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
)

// Runtime executes bash-like scripts inside the configured sandbox.
//
// Use [New] to construct a runtime, [Runtime.Run] for one-shot execution, and
// [Runtime.NewSession] when you want multiple executions to share the same
// sandbox filesystem state.
type Runtime struct {
	inner *internalruntime.Runtime
}

// Session is a persistent sandbox that can execute multiple scripts against the
// same filesystem state.
//
// Sessions are created by calling [Runtime.NewSession].
type Session struct {
	inner *internalruntime.Session
}

// Method identifies an HTTP method that is allowed by the sandbox network
// policy.
type Method = network.Method

const (
	// MethodGet allows HTTP GET requests.
	MethodGet = network.MethodGet
	// MethodHead allows HTTP HEAD requests.
	MethodHead = network.MethodHead
	// MethodPost allows HTTP POST requests.
	MethodPost = network.MethodPost
	// MethodPut allows HTTP PUT requests.
	MethodPut = network.MethodPut
	// MethodDelete allows HTTP DELETE requests.
	MethodDelete = network.MethodDelete
	// MethodPatch allows HTTP PATCH requests.
	MethodPatch = network.MethodPatch
	// MethodOptions allows HTTP OPTIONS requests.
	MethodOptions = network.MethodOptions
)

const (
	// DefaultWorkspaceMountPoint is the default sandbox mount point used by
	// [WithWorkspace] and [HostDirectoryFileSystem].
	DefaultWorkspaceMountPoint = gbfs.DefaultHostVirtualRoot
	// DefaultHostFileReadBytes is the default per-file read cap used when a host
	// directory is mounted into the sandbox.
	DefaultHostFileReadBytes = gbfs.DefaultHostMaxFileReadBytes
)

// Config describes the complete gbash runtime configuration.
//
// The zero value is useful: it creates the default in-memory sandbox rooted at
// /home/agent with the default command registry and the default static policy.
//
// Most callers should prefer [New] with a small number of option helpers such
// as [WithWorkspace], [WithHTTPAccess], [WithRegistry], or [WithBaseEnv]. This
// struct is provided for callers that want to construct a configuration value
// explicitly before handing it to [WithConfig].
type Config struct {
	// FileSystem controls how each session gets its sandbox filesystem and what
	// working directory new sessions start in.
	FileSystem FileSystemConfig

	// Registry contains the commands that can be executed inside the sandbox.
	// When nil, the default built-in registry is used.
	Registry commands.CommandRegistry

	// Policy governs path access, command limits, and other sandbox checks.
	// When nil, the default static policy is used.
	Policy policy.Policy

	// BaseEnv provides the base environment visible to each execution before any
	// per-request environment overrides are applied.
	BaseEnv map[string]string

	// Host controls the host-derived platform behavior, process metadata, and
	// base environment defaults visible to the runtime. When nil, gbash uses its
	// internal virtual host adapter.
	Host host.Adapter

	// Network configures the built-in HTTP client used by the curl command. When
	// nil and NetworkClient is also nil, curl is not registered in the sandbox.
	Network *NetworkConfig

	// NetworkClient replaces the built-in HTTP client. This is the advanced
	// escape hatch for tests and custom transports.
	NetworkClient network.Client

	// Tracing controls structured execution events. Tracing is off by default.
	// When enabled, [ExecutionResult.Events] is populated for non-interactive
	// executions and OnEvent receives events for both non-interactive and
	// interactive runs.
	Tracing TraceConfig

	// Logger receives top-level execution lifecycle events. Logging is off by
	// default.
	Logger LogCallback
}

// FileSystemConfig describes how gbash provisions a session filesystem.
//
// Callers rarely need to populate this struct directly. Prefer the helper
// constructors [InMemoryFileSystem], [SeededInMemoryFileSystem],
// [HostDirectoryFileSystem], [MountableFileSystem],
// [ReadWriteDirectoryFileSystem], and [CustomFileSystem], and then apply the
// result with [WithFileSystem].
type FileSystemConfig struct {
	// Factory builds the filesystem instance for a new session.
	Factory gbfs.Factory

	// WorkingDir is the directory new sessions start in.
	WorkingDir string
}

// MountableFileSystemOptions configures a multi-mount sandbox filesystem.
type MountableFileSystemOptions struct {
	// Base provisions the base filesystem used for unmounted paths. When nil, a
	// fresh in-memory filesystem is used.
	Base gbfs.Factory

	// Mounts configures the mounted filesystems visible inside the sandbox.
	Mounts []gbfs.MountConfig

	// WorkingDir is the directory new sessions start in. When empty,
	// /home/agent is used.
	WorkingDir string
}

// HostDirectoryOptions controls how a real host directory is mounted into the
// sandbox.
//
// The mounted directory is always read-only from the host's perspective. gbash
// layers an in-memory writable upper filesystem on top so the shell can create,
// overwrite, and delete files without mutating the host tree.
type HostDirectoryOptions struct {
	// MountPoint is the sandbox path where the host directory should appear.
	// When empty, [DefaultWorkspaceMountPoint] is used.
	MountPoint string

	// MaxFileReadBytes limits the size of individual regular files that may be
	// read from the host directory. When zero or negative, the default host read
	// cap is used.
	MaxFileReadBytes int64
}

// ReadWriteDirectoryOptions controls how a real host directory is mounted as a
// mutable sandbox root.
//
// Unlike [HostDirectoryOptions], this mode writes directly back to the host
// directory instead of using an in-memory overlay. It is intended for opt-in
// compatibility harnesses and advanced embedding scenarios.
type ReadWriteDirectoryOptions struct {
	// MaxFileReadBytes limits the size of individual regular files that may be
	// read from the host directory. When zero or negative, the default host read
	// cap is used.
	MaxFileReadBytes int64
}

// NetworkConfig controls the built-in HTTP client that powers curl inside the
// sandbox.
//
// All fields are optional except that some form of URL allowlist is required at
// runtime. Empty AllowedMethods defaults to GET and HEAD. Zero-valued limits use
// the network package defaults.
type NetworkConfig struct {
	// AllowedURLPrefixes is the URL allowlist for sandbox HTTP access.
	AllowedURLPrefixes []string

	// AllowedMethods restricts which HTTP methods may be used. When empty, GET
	// and HEAD are allowed.
	AllowedMethods []Method

	// MaxRedirects limits how many redirects a request may follow.
	MaxRedirects int

	// Timeout is the default request timeout.
	Timeout time.Duration

	// MaxResponseBytes caps the response body size.
	MaxResponseBytes int64

	// DenyPrivateRanges blocks requests to private, loopback, link-local, and
	// similar address ranges.
	DenyPrivateRanges bool
}

// Option mutates a [Config] before [New] constructs the runtime.
//
// Options are applied in order, so later options can intentionally override
// earlier ones.
type Option func(*Config) error

// New constructs a runtime from the provided options.
//
// When called with no options, New returns the default sandbox runtime:
//
//   - an isolated in-memory filesystem rooted at /home/agent
//   - the built-in command registry
//   - the default in-tree shell core
//   - the default static policy and resource limits
//   - no network access
//
// Use [WithWorkspace] to mount a real host directory, [WithHTTPAccess] or
// [WithNetwork] to enable curl, and [WithRegistry], [WithPolicy], or
// [WithFileSystem] when you need lower-level control.
func New(opts ...Option) (*Runtime, error) {
	cfg, err := resolveConfig(opts)
	if err != nil {
		return nil, err
	}

	rt, err := internalruntime.New(internalruntime.WithConfig(cfg.runtimeConfig()))
	if err != nil {
		return nil, err
	}
	return &Runtime{inner: rt}, nil
}

func resolveConfig(opts []Option) (Config, error) {
	var cfg Config
	for i, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return Config{}, fmt.Errorf("gbash: apply option %d: %w", i, err)
		}
	}
	return cfg, nil
}

func (cfg *Config) runtimeConfig() *internalruntime.Config {
	if cfg == nil {
		return &internalruntime.Config{}
	}
	return &internalruntime.Config{
		FileSystem:    cfg.FileSystem.runtimeConfig(),
		Registry:      cfg.Registry,
		Policy:        cfg.Policy,
		BaseEnv:       copyStringMap(cfg.BaseEnv),
		Host:          cfg.Host,
		Network:       cfg.networkConfig(),
		NetworkClient: cfg.NetworkClient,
		Tracing:       cfg.Tracing,
		Logger:        cfg.Logger,
	}
}

func (cfg *Config) networkConfig() *network.Config {
	if cfg == nil || cfg.Network == nil {
		return nil
	}
	return &network.Config{
		AllowedURLPrefixes: append([]string(nil), cfg.Network.AllowedURLPrefixes...),
		AllowedMethods:     append([]network.Method(nil), cfg.Network.AllowedMethods...),
		MaxRedirects:       cfg.Network.MaxRedirects,
		Timeout:            cfg.Network.Timeout,
		MaxResponseBytes:   cfg.Network.MaxResponseBytes,
		DenyPrivateRanges:  cfg.Network.DenyPrivateRanges,
	}
}

func (cfg FileSystemConfig) runtimeConfig() internalruntime.FileSystemConfig {
	return internalruntime.FileSystemConfig{
		Factory:    cfg.Factory,
		WorkingDir: cfg.WorkingDir,
	}
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	maps.Copy(out, src)
	return out
}

// DefaultRegistry returns a registry populated with gbash's built-in commands.
//
// Callers can register additional custom commands onto the returned registry
// before passing it to [WithRegistry].
func DefaultRegistry() *commands.Registry {
	return builtins.DefaultRegistry()
}

// InMemoryFileSystem returns the default mutable sandbox filesystem
// configuration.
//
// This is the same filesystem layout gbash uses when [New] is called without a
// filesystem option.
func InMemoryFileSystem() FileSystemConfig {
	return FileSystemConfig{
		Factory:    gbfs.Memory(),
		WorkingDir: "/home/agent",
	}
}

// SeededInMemoryFileSystem returns an in-memory filesystem configuration
// preloaded with the provided files.
func SeededInMemoryFileSystem(files gbfs.InitialFiles) FileSystemConfig {
	return FileSystemConfig{
		Factory:    gbfs.SeededMemory(files),
		WorkingDir: InMemoryFileSystem().WorkingDir,
	}
}

// CustomFileSystem wires an arbitrary filesystem factory into the runtime.
//
// This is the low-level escape hatch for callers that want to seed a custom
// filesystem backend or provide their own persistence model.
func CustomFileSystem(factory gbfs.Factory, workingDir string) FileSystemConfig {
	return FileSystemConfig{
		Factory:    factory,
		WorkingDir: workingDir,
	}
}

// MountableFileSystem returns a multi-mount filesystem configuration.
func MountableFileSystem(opts MountableFileSystemOptions) FileSystemConfig {
	workingDir := strings.TrimSpace(opts.WorkingDir)
	if workingDir == "" {
		workingDir = InMemoryFileSystem().WorkingDir
	}
	return FileSystemConfig{
		Factory: gbfs.Mountable(gbfs.MountableOptions{
			Base:   opts.Base,
			Mounts: append([]gbfs.MountConfig(nil), opts.Mounts...),
		}),
		WorkingDir: workingDir,
	}
}

// HostDirectoryFileSystem mounts a real host directory into the sandbox under a
// writable in-memory overlay.
//
// The mounted host tree is read-only. All writes, deletes, and command stubs
// live in the in-memory upper layer, so shell activity never mutates the host
// directory directly.
func HostDirectoryFileSystem(root string, opts HostDirectoryOptions) FileSystemConfig {
	mountPoint := strings.TrimSpace(opts.MountPoint)
	if mountPoint == "" {
		mountPoint = DefaultWorkspaceMountPoint
	}
	return FileSystemConfig{
		Factory: gbfs.Overlay(gbfs.Host(gbfs.HostOptions{
			Root:             root,
			VirtualRoot:      mountPoint,
			MaxFileReadBytes: opts.MaxFileReadBytes,
		})),
		WorkingDir: mountPoint,
	}
}

// ReadWriteDirectoryFileSystem mounts a real host directory as the mutable
// sandbox root.
//
// This is the closest gbash equivalent to just-bash's ReadWriteFs: sandbox
// paths are rooted at "/", sessions start at "/", and writes persist directly
// to the host directory.
func ReadWriteDirectoryFileSystem(root string, opts ReadWriteDirectoryOptions) FileSystemConfig {
	return FileSystemConfig{
		Factory: gbfs.ReadWrite(gbfs.ReadWriteOptions{
			Root:             root,
			MaxFileReadBytes: opts.MaxFileReadBytes,
		}),
		WorkingDir: "/",
	}
}

// NewSession creates a new persistent session backed by the runtime's
// configured filesystem factory and sandbox policy.
//
// Each session gets its own filesystem state. Repeated calls create isolated
// sessions, while repeated calls to [Session.Exec] on the same session share the
// same sandbox filesystem.
func (r *Runtime) NewSession(ctx context.Context) (*Session, error) {
	if r == nil || r.inner == nil {
		return nil, fmt.Errorf("gbash: runtime is nil")
	}
	session, err := r.inner.NewSession(withHostProcessGroup(ctx))
	if err != nil {
		return nil, err
	}
	return &Session{inner: session}, nil
}

// Run executes a script in a fresh session and returns the result.
//
// Use [Runtime.NewSession] when you want filesystem state to persist across
// multiple executions.
func (r *Runtime) Run(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	if r == nil || r.inner == nil {
		return nil, fmt.Errorf("gbash: runtime is nil")
	}
	result, err := r.inner.Run(withHostProcessGroup(ctx), req.runtimeRequest())
	return executionResultFromRuntime(result), err
}

// Exec runs a script inside the existing session.
//
// Session executions share filesystem state with each other, but shell-local
// state such as the working directory and environment only persists when the
// caller explicitly threads it through later requests.
func (s *Session) Exec(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	if s == nil || s.inner == nil {
		return nil, fmt.Errorf("gbash: session is nil")
	}
	result, err := s.inner.Exec(withHostProcessGroup(ctx), req.runtimeRequest())
	return executionResultFromRuntime(result), err
}

// Interact runs an interactive shell session inside the existing session.
func (s *Session) Interact(ctx context.Context, req *InteractiveRequest) (*InteractiveResult, error) {
	if s == nil || s.inner == nil {
		return nil, fmt.Errorf("gbash: session is nil")
	}
	result, err := s.inner.Interact(withHostProcessGroup(ctx), req.runtimeRequest())
	return interactiveResultFromRuntime(result), err
}

// FileSystem returns the live sandbox filesystem for the session.
//
// Most callers do not need this method. It exists as an advanced escape hatch
// for tests, bootstrapping, and integrations that need direct filesystem
// access. Callers should treat the result as the [gbfs.FileSystem] interface
// and should not rely on concrete backend types.
func (s *Session) FileSystem() gbfs.FileSystem {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.FileSystem()
}

// Limits returns the effective runtime policy limits for the session.
//
// This is mainly useful for integrations that need to align pre-execution
// behavior with the same caps the runtime enforces during command and file
// reads.
func (s *Session) Limits() policy.Limits {
	if s == nil || s.inner == nil {
		return policy.Limits{}
	}
	return s.inner.Limits()
}
