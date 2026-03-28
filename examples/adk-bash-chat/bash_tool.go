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
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/contrib/bashtool"
	"github.com/ewhauser/gbash/contrib/extras"
	gbfs "github.com/ewhauser/gbash/fs"
	"google.golang.org/adk/tool"
)

const (
	labDir         = "/home/agent/lab"
	workDir        = "/home/agent/work"
	defaultToolDir = labDir
)

type persistentBashTool struct {
	gb         *gbash.Runtime
	fixtureDir string

	mu      sync.Mutex
	session *gbash.Session
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

func newBashRegistry() commands.CommandRegistry {
	return extras.FullRegistry()
}

func newChatBashToolContract(registry commands.CommandRegistry) *bashtool.Tool {
	return bashtool.New(bashtool.Config{
		Profile:  bashtool.CommandProfileExtras,
		Registry: registry,
		CommandNotes: []string{
			"Files, the working directory, and exported environment variables persist across calls within the current chat session",
			"The seeded dataset lives in /home/agent/lab and reusable artifacts belong in /home/agent/work",
		},
		SystemPromptAppend: "This sandbox is persistent across tool calls: files, the current working directory, and exported environment variables carry forward within the current chat session. " +
			"The seeded dataset lives in /home/agent/lab and reusable artifacts belong in /home/agent/work.",
	})
}

func newPersistentBashTool(ctx context.Context, registry commands.CommandRegistry) (*persistentBashTool, error) {
	gb, err := gbash.New(gbash.WithRegistry(registry)) //nolint:contextcheck // constructor does not accept context
	if err != nil {
		return nil, fmt.Errorf("create runtime: %w", err)
	}

	bt := &persistentBashTool{
		gb:         gb,
		fixtureDir: mustFixtureDir(),
	}
	if err := bt.resetLocked(ctx); err != nil {
		return nil, err
	}
	return bt, nil
}

func (t *persistentBashTool) Run(ctx tool.Context, input bashtool.Request) (bashtool.Response, error) {
	return t.runScript(ctx, input), nil
}

func (t *persistentBashTool) runScript(ctx context.Context, input bashtool.Request) bashtool.Response {
	commandsText := input.ResolvedCommands()
	if strings.TrimSpace(commandsText) == "" {
		return bashtool.Response{
			Stdout:   "",
			Stderr:   "`commands` or `script` is required",
			ExitCode: 1,
			Error:    "parse_error",
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	result, err := t.session.Exec(ctx, &gbash.ExecutionRequest{
		Name:       "adk-bash",
		Script:     commandsText,
		Env:        cloneMap(t.state.env),
		WorkDir:    t.state.workDir,
		Timeout:    input.Timeout(),
		ReplaceEnv: t.state.env != nil,
	})
	if err != nil {
		return bashToolErrorResponse(ctx, err, input.Timeout())
	}

	t.state = nextBashState(t.state, result)

	return bashtool.Response{
		ExitCode:        result.ExitCode,
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		StdoutTruncated: result.StdoutTruncated,
		StderrTruncated: result.StderrTruncated,
		FinalEnv:        cloneMap(t.state.env),
	}
}

func (t *persistentBashTool) Reset(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resetLocked(ctx)
}

func (t *persistentBashTool) resetLocked(ctx context.Context) error {
	session, err := t.gb.NewSession(ctx)
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

func nextBashState(current bashState, result *gbash.ExecutionResult) bashState {
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

func seedLab(ctx context.Context, session *gbash.Session, fixtureDir string) error {
	if session == nil {
		return errors.New("session is nil")
	}

	fsys := session.FileSystem()
	if err := fsys.MkdirAll(ctx, labDir, 0o755); err != nil { //nolint:nilaway // session is non-nil (checked above) so FileSystem() returns a valid fs
		return fmt.Errorf("create lab dir: %w", err)
	}
	if err := fsys.MkdirAll(ctx, workDir, 0o755); err != nil { //nolint:nilaway // same fsys guarded by session non-nil check above
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

	result, err := session.Exec(ctx, &gbash.ExecutionRequest{
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
	if err := fsys.MkdirAll(ctx, path.Dir(name), 0o755); err != nil { //nolint:nilaway // callers ensure fsys is non-nil before passing it here
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

func bashToolErrorResponse(ctx context.Context, err error, requestTimeout time.Duration) bashtool.Response {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return bashToolTimeoutResponse(contextTimeout(ctx))
		}
		return bashToolTimeoutResponse(requestTimeout)
	case errors.Is(err, context.Canceled):
		return bashtool.Response{
			Stdout:   "",
			Stderr:   err.Error(),
			ExitCode: 1,
			Error:    "canceled",
		}
	default:
		return bashtool.Response{
			Stdout:   "",
			Stderr:   err.Error(),
			ExitCode: 1,
			Error:    "execution_error",
		}
	}
}

func bashToolTimeoutResponse(timeout time.Duration) bashtool.Response {
	seconds := timeout.Seconds()
	if seconds <= 0 {
		seconds = 0
	}
	return bashtool.Response{
		Stdout:   "",
		Stderr:   fmt.Sprintf("bash: execution timed out after %.1fs\n", seconds),
		ExitCode: 124,
		Error:    "timeout",
	}
}

func contextTimeout(ctx context.Context) time.Duration {
	if ctx == nil {
		return 0
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	timeout := time.Until(deadline)
	if timeout < 0 {
		return 0
	}
	return timeout
}
