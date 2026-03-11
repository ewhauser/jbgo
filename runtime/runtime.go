package runtime

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/ewhauser/jbgo/commands"
	jbfs "github.com/ewhauser/jbgo/fs"
	"github.com/ewhauser/jbgo/network"
	"github.com/ewhauser/jbgo/policy"
	"github.com/ewhauser/jbgo/shell"
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
	cfg Config
}

type Session struct {
	cfg Config
	id  string
	fs  jbfs.FileSystem
	mu  sync.Mutex
}

type ExecutionRequest = commands.ExecutionRequest
type ExecutionResult = commands.ExecutionResult

func New(cfg *Config) (*Runtime, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	resolved := *cfg
	resolved.FileSystem = resolved.FileSystem.resolved()
	if resolved.Registry == nil {
		resolved.Registry = commands.DefaultRegistry()
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
		if _, ok := resolved.Registry.Lookup("curl"); !ok {
			if err := resolved.Registry.Register(commands.NewCurl()); err != nil {
				return nil, err
			}
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

	return &Runtime{cfg: resolved}, nil
}

func (r *Runtime) NewSession(ctx context.Context) (*Session, error) {
	fsys, err := r.cfg.FileSystem.Factory.New(ctx)
	if err != nil {
		return nil, err
	}

	if err := initializeSandboxLayout(ctx, fsys, r.cfg.BaseEnv, r.cfg.FileSystem.WorkingDir, r.cfg.Registry.Names()); err != nil {
		return nil, err
	}

	return &Session{
		cfg: r.cfg,
		id:  nextTraceID("sess"),
		fs:  fsys,
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
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func stdinOrEmpty(reader io.Reader) io.Reader {
	if reader == nil {
		return strings.NewReader("")
	}
	return reader
}
