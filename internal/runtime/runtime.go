package runtime

import (
	"context"
	"io"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/shell/analysis"
)

type Config struct {
	FileSystem       FileSystemConfig
	Registry         commands.CommandRegistry
	Policy           policy.Policy
	LimitOverrides   policy.Limits
	BaseEnv          map[string]string
	Host             host.Adapter
	Network          *network.Config
	NetworkClient    network.Client
	Tracing          TraceConfig
	Logger           LogCallback
	AnalysisObserver analysis.Observer
}

type Runtime struct {
	cfg            Config
	sessionFactory sessionFactory
}

type Session struct {
	cfg         Config
	id          string
	fs          gbfs.FileSystem
	bootAt      time.Time
	currentTime time.Time
	clockRealAt time.Time
	layout      *sandboxLayoutState
	mu          sync.Mutex
	clockMu     sync.RWMutex
}

func New(opts ...Option) (*Runtime, error) {
	resolved, err := resolveConfig(opts)
	if err != nil {
		return nil, err
	}
	defaultSessionFS := resolved.FileSystem.Factory == nil
	resolved.FileSystem = resolved.FileSystem.resolved()
	if resolved.Registry == nil {
		resolved.Registry = builtins.DefaultRegistry()
	}
	if resolved.NetworkClient == nil && resolved.Network != nil {
		client, err := network.New(resolved.Network)
		if err != nil {
			return nil, err
		}
		resolved.NetworkClient = client
	}
	if resolved.NetworkClient != nil {
		if err := builtins.EnsureNetworkCommands(resolved.Registry); err != nil {
			return nil, err
		}
	}
	defaultLimits := mergedLimits(policy.Limits{
		MaxCommandCount:      10000,
		MaxGlobOperations:    100000,
		MaxLoopIterations:    10000,
		MaxSubstitutionDepth: 50,
		MaxStdoutBytes:       1 << 20,
		MaxStderrBytes:       1 << 20,
		MaxFileBytes:         8 << 20,
	}, resolved.LimitOverrides)
	if resolved.Policy == nil {
		resolved.Policy = policy.NewStatic(&policy.Config{
			AllowedCommands: resolved.Registry.Names(),
			ReadRoots:       []string{"/"},
			WriteRoots:      []string{"/"},
			Limits:          defaultLimits,
			SymlinkMode:     policy.SymlinkDeny,
		})
	} else if resolved.LimitOverrides != (policy.Limits{}) {
		resolved.Policy = limitOverridePolicy{
			base:   resolved.Policy,
			limits: mergedLimits(resolved.Policy.Limits(), resolved.LimitOverrides),
		}
	}
	if resolved.Host == nil {
		resolved.Host = newVirtualHost()
	}
	hostEnv, err := runtimeBaseEnv(context.Background(), resolved.Host)
	if err != nil {
		return nil, err
	}
	resolved.BaseEnv = mergeEnv(hostEnv, resolved.BaseEnv)

	factory := sessionFactory(plainSessionFactory{base: resolved.FileSystem.Factory})
	if defaultSessionFS {
		factory = &preparedMemorySessionFactory{
			base:     resolved.FileSystem.Factory,
			env:      resolved.BaseEnv,
			workDir:  resolved.FileSystem.WorkingDir,
			commands: resolved.Registry.Names(),
		}
	}

	return &Runtime{
		cfg:            resolved,
		sessionFactory: factory,
	}, nil
}

type limitOverridePolicy struct {
	base   policy.Policy
	limits policy.Limits
}

func (p limitOverridePolicy) AllowCommand(ctx context.Context, name string, argv []string) error {
	return p.base.AllowCommand(ctx, name, argv)
}

func (p limitOverridePolicy) AllowBuiltin(ctx context.Context, name string, argv []string) error {
	return p.base.AllowBuiltin(ctx, name, argv)
}

func (p limitOverridePolicy) AllowPath(ctx context.Context, action policy.FileAction, target string) error {
	return p.base.AllowPath(ctx, action, target)
}

func (p limitOverridePolicy) Limits() policy.Limits {
	return p.limits
}

func (p limitOverridePolicy) SymlinkMode() policy.SymlinkMode {
	return p.base.SymlinkMode()
}

func mergedLimits(base, overrides policy.Limits) policy.Limits {
	if overrides.MaxCommandCount != 0 {
		base.MaxCommandCount = overrides.MaxCommandCount
	}
	if overrides.MaxGlobOperations != 0 {
		base.MaxGlobOperations = overrides.MaxGlobOperations
	}
	if overrides.MaxLoopIterations != 0 {
		base.MaxLoopIterations = overrides.MaxLoopIterations
	}
	if overrides.MaxSubstitutionDepth != 0 {
		base.MaxSubstitutionDepth = overrides.MaxSubstitutionDepth
	}
	if overrides.MaxStdoutBytes != 0 {
		base.MaxStdoutBytes = overrides.MaxStdoutBytes
	}
	if overrides.MaxStderrBytes != 0 {
		base.MaxStderrBytes = overrides.MaxStderrBytes
	}
	if overrides.MaxFileBytes != 0 {
		base.MaxFileBytes = overrides.MaxFileBytes
	}
	return base
}

func (r *Runtime) NewSession(ctx context.Context) (*Session, error) {
	fsys, err := r.sessionFactory.New(ctx)
	if err != nil {
		return nil, err
	}
	fsys = wrapSandboxFileSystem(fsys)

	if !r.sessionFactory.layoutReady() {
		if err := initializeSandboxLayout(ctx, fsys, r.cfg.BaseEnv, r.cfg.FileSystem.WorkingDir, r.cfg.Registry.Names()); err != nil {
			return nil, err
		}
	}

	now := time.Now()
	return &Session{
		cfg:         r.cfg,
		id:          nextTraceID("sess"),
		fs:          fsys,
		bootAt:      now.UTC(),
		currentTime: now.UTC(),
		clockRealAt: now,
		layout:      newSandboxLayoutState(r.cfg.BaseEnv, r.cfg.FileSystem.WorkingDir),
	}, nil
}

func (r *Runtime) Run(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	session, err := r.NewSession(ctx)
	if err != nil {
		return nil, err
	}
	return session.Exec(ctx, req)
}

func defaultName(name string) string {
	if name == "" {
		return "stdin"
	}
	return name
}

func mergeEnv(base, override map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(override))
	maps.Copy(out, base)
	maps.Copy(out, override)
	return out
}

func stdinOrEmpty(reader io.Reader) io.Reader {
	if reader == nil {
		return strings.NewReader("")
	}
	return reader
}
