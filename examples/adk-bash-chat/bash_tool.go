package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"

	"github.com/ewhauser/gbash/commands"
	contribsqlite3 "github.com/ewhauser/gbash/contrib/sqlite3"
	gbfs "github.com/ewhauser/gbash/fs"
	gbruntime "github.com/ewhauser/gbash/runtime"
	"google.golang.org/adk/tool"
)

const (
	labDir         = "/home/agent/lab"
	workDir        = "/home/agent/work"
	defaultToolDir = labDir
)

type bashToolInput struct {
	Script string `json:"script"`
}

type bashToolResult struct {
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	PWD             string `json:"pwd"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
}

type persistentBashTool struct {
	rt         *gbruntime.Runtime
	fixtureDir string

	mu      sync.Mutex
	session *gbruntime.Session
	state   bashState
}

type bashState struct {
	workDir string
	env     map[string]string
}

type fixtureSpec struct {
	Source string
	Target string
}

var labFixtures = []fixtureSpec{
	{Source: "README.md", Target: labDir + "/README.md"},
	{Source: "services.csv", Target: labDir + "/services.csv"},
	{Source: "deploys.csv", Target: labDir + "/deploys.csv"},
	{Source: "jobs.jsonl", Target: labDir + "/jobs.jsonl"},
	{Source: "incidents.sql", Target: labDir + "/incidents.sql"},
	{Source: "handoff.md", Target: labDir + "/handoff.md"},
}

func newPersistentBashTool(ctx context.Context) (*persistentBashTool, error) {
	registry := commands.DefaultRegistry()
	if err := contribsqlite3.Register(registry); err != nil {
		return nil, fmt.Errorf("register sqlite3 command: %w", err)
	}

	rt, err := gbruntime.New(&gbruntime.Config{Registry: registry})
	if err != nil {
		return nil, fmt.Errorf("create runtime: %w", err)
	}

	bt := &persistentBashTool{
		rt:         rt,
		fixtureDir: mustFixtureDir(),
	}
	if err := bt.resetLocked(ctx); err != nil {
		return nil, err
	}
	return bt, nil
}

func (t *persistentBashTool) Run(ctx tool.Context, input bashToolInput) (bashToolResult, error) {
	return t.runScript(ctx, input)
}

func (t *persistentBashTool) runScript(ctx context.Context, input bashToolInput) (bashToolResult, error) {
	if strings.TrimSpace(input.Script) == "" {
		return bashToolResult{}, errors.New("bash tool requires a non-empty script")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	result, err := t.session.Exec(ctx, &gbruntime.ExecutionRequest{
		Name:       "adk-bash",
		Script:     input.Script,
		Env:        cloneMap(t.state.env),
		WorkDir:    t.state.workDir,
		ReplaceEnv: t.state.env != nil,
	})
	if err != nil {
		return bashToolResult{}, fmt.Errorf("run bash script: %w", err)
	}

	t.state = nextBashState(t.state, result)

	return bashToolResult{
		ExitCode:        result.ExitCode,
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		PWD:             t.state.workDir,
		StdoutTruncated: result.StdoutTruncated,
		StderrTruncated: result.StderrTruncated,
	}, nil
}

func (t *persistentBashTool) Reset(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resetLocked(ctx)
}

func (t *persistentBashTool) resetLocked(ctx context.Context) error {
	session, err := t.rt.NewSession(ctx)
	if err != nil {
		return fmt.Errorf("create sandbox session: %w", err)
	}
	if err := seedLab(ctx, session, t.fixtureDir); err != nil {
		return err
	}

	t.session = session
	t.state = bashState{workDir: defaultToolDir}
	return nil
}

func nextBashState(current bashState, result *gbruntime.ExecutionResult) bashState {
	next := current
	if result == nil || result.FinalEnv == nil {
		if next.workDir == "" {
			next.workDir = defaultToolDir
		}
		return next
	}

	next.env = cloneMap(result.FinalEnv)
	if pwd := strings.TrimSpace(result.FinalEnv["PWD"]); pwd != "" {
		next.workDir = pwd
	}
	if next.workDir == "" {
		next.workDir = defaultToolDir
	}
	return next
}

func seedLab(ctx context.Context, session *gbruntime.Session, fixtureDir string) error {
	if session == nil {
		return errors.New("session is nil")
	}

	fsys := session.FileSystem()
	if err := fsys.MkdirAll(ctx, labDir, 0o755); err != nil {
		return fmt.Errorf("create lab dir: %w", err)
	}
	if err := fsys.MkdirAll(ctx, workDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	for _, fixture := range labFixtures {
		src := filepath.Join(fixtureDir, fixture.Source)
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read fixture %q: %w", src, err)
		}
		if err := writeVirtualFile(ctx, fsys, fixture.Target, data); err != nil {
			return fmt.Errorf("seed %q: %w", fixture.Target, err)
		}
	}

	result, err := session.Exec(ctx, &gbruntime.ExecutionRequest{
		Name:    "seed-lab",
		WorkDir: labDir,
		Script:  "sqlite3 incidents.db < incidents.sql" + "\n" + "mkdir -p " + workDir + "\n",
	})
	if err != nil {
		return fmt.Errorf("bootstrap lab: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("bootstrap lab failed with exit %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}

	return nil
}

func writeVirtualFile(ctx context.Context, fsys gbfs.FileSystem, name string, data []byte) error {
	if err := fsys.MkdirAll(ctx, path.Dir(name), 0o755); err != nil {
		return err
	}
	file, err := fsys.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = io.Copy(file, strings.NewReader(string(data)))
	return err
}

func cloneMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

func mustFixtureDir() string {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		panic("resolve fixture dir: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "fixtures")
}
