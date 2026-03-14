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
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/shell"
)

type Config struct {
	FileSystem    FileSystemConfig
	Registry      commands.CommandRegistry
	Policy        policy.Policy
	Engine        shell.Engine
	BaseEnv       map[string]string
	Network       *network.Config
	NetworkClient network.Client
}

type Runtime struct {
	cfg            Config
	sessionFactory sessionFactory
}

type Session struct {
	cfg    Config
	id     string
	fs     gbfs.FileSystem
	bootAt time.Time
	layout *sandboxLayoutState
	mu     sync.Mutex
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
	if resolved.Engine == nil {
		resolved.Engine = shell.New()
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
	if resolved.Policy == nil {
		resolved.Policy = policy.NewStatic(&policy.Config{
			AllowedCommands: resolved.Registry.Names(),
			ReadRoots:       []string{"/"},
			WriteRoots:      []string{"/"},
			Limits: policy.Limits{
				MaxCommandCount:      10000,
				MaxGlobOperations:    100000,
				MaxLoopIterations:    10000,
				MaxSubstitutionDepth: 50,
				MaxStdoutBytes:       1 << 20,
				MaxStderrBytes:       1 << 20,
				MaxFileBytes:         8 << 20,
			},
			NetworkMode: policy.NetworkDisabled,
			SymlinkMode: policy.SymlinkDeny,
		})
	}
	resolved.BaseEnv = mergeEnv(defaultBaseEnv(), resolved.BaseEnv)

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

func (r *Runtime) NewSession(ctx context.Context) (*Session, error) {
	fsys, err := r.sessionFactory.New(ctx)
	if err != nil {
		return nil, err
	}

	if !r.sessionFactory.layoutReady() {
		if err := initializeSandboxLayout(ctx, fsys, r.cfg.BaseEnv, r.cfg.FileSystem.WorkingDir, r.cfg.Registry.Names()); err != nil {
			return nil, err
		}
	}

	return &Session{
		cfg:    r.cfg,
		id:     nextTraceID("sess"),
		fs:     fsys,
		bootAt: time.Now().UTC(),
		layout: newSandboxLayoutState(r.cfg.BaseEnv, r.cfg.FileSystem.WorkingDir),
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
