package runtime

import (
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

type timeoutProbe struct{}

type blockingScriptFS struct {
	gbfs.FileSystem
	path string
}

type blockingScriptFile struct {
	info   stdfs.FileInfo
	done   chan struct{}
	closer sync.Once
}

func newBlockingScriptFactory(t testing.TB, scriptPath string) gbfs.Factory {
	t.Helper()

	return gbfs.FactoryFunc(func(ctx context.Context) (gbfs.FileSystem, error) {
		mem := gbfs.NewMemory()
		file, err := mem.OpenFile(ctx, scriptPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(file, "echo ignored\n"); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		return &blockingScriptFS{FileSystem: mem, path: scriptPath}, nil
	})
}

func (fs *blockingScriptFS) Open(ctx context.Context, name string) (gbfs.File, error) {
	if gbfs.Clean(name) != gbfs.Clean(fs.path) {
		return fs.FileSystem.Open(ctx, name)
	}
	info, err := fs.Stat(ctx, name)
	if err != nil {
		return nil, err
	}
	return &blockingScriptFile{
		info: info,
		done: make(chan struct{}),
	}, nil
}

func (fs *blockingScriptFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (gbfs.File, error) {
	if gbfs.Clean(name) != gbfs.Clean(fs.path) || flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		return fs.FileSystem.OpenFile(ctx, name, flag, perm)
	}
	return fs.Open(ctx, name)
}

func (f *blockingScriptFile) Read([]byte) (int, error) {
	<-f.done
	return 0, nil
}

func (f *blockingScriptFile) Write([]byte) (int, error) {
	return 0, stdfs.ErrPermission
}

func (f *blockingScriptFile) Close() error {
	f.closer.Do(func() { close(f.done) })
	return nil
}

func (f *blockingScriptFile) Stat() (stdfs.FileInfo, error) {
	return f.info, nil
}

func (c *timeoutProbe) Name() string {
	return "timeoutprobe"
}

func (c *timeoutProbe) Run(ctx context.Context, inv *commands.Invocation) error {
	if inv.Exec == nil {
		return fmt.Errorf("subexec callback missing")
	}

	result, err := inv.Exec(ctx, &commands.ExecutionRequest{
		Command: []string{"sleep", "1"},
		Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(inv.Stdout, "exit=%d\n", result.ExitCode); err != nil {
		return err
	}
	if result.Stderr != "" {
		if _, err := io.WriteString(inv.Stderr, result.Stderr); err != nil {
			return err
		}
	}
	return nil
}

func TestExecutionTimeoutReturns124(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script:  "while true; do :; done\n",
		Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 124 {
		t.Fatalf("ExitCode = %d, want 124", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want timeout message", result.Stderr)
	}
}

func TestExecutionCancellationReturns130(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(20*time.Millisecond, cancel)

	result, err := rt.Run(ctx, &ExecutionRequest{
		Script: "while true; do :; done\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 130 {
		t.Fatalf("ExitCode = %d, want 130", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "execution canceled") {
		t.Fatalf("Stderr = %q, want cancellation message", result.Stderr)
	}
}

func TestScriptPathLoadTimeoutReturns124(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{
		FileSystem: CustomFileSystem(newBlockingScriptFactory(t, "/tmp/block.sh"), defaultHomeDir),
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		ScriptPath: "/tmp/block.sh",
		Timeout:    20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 124 {
		t.Fatalf("ExitCode = %d, want 124", result.ExitCode)
	}
	if got, want := result.ControlStderr, "execution timed out after 20ms"; got != want {
		t.Fatalf("ControlStderr = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out after 20ms") {
		t.Fatalf("Stderr = %q, want timeout message", result.Stderr)
	}
}

func TestScriptPathLoadCancellationReturns130(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{
		FileSystem: CustomFileSystem(newBlockingScriptFactory(t, "/tmp/block.sh"), defaultHomeDir),
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(20*time.Millisecond, cancel)

	result, err := rt.Run(ctx, &ExecutionRequest{ScriptPath: "/tmp/block.sh"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 130 {
		t.Fatalf("ExitCode = %d, want 130", result.ExitCode)
	}
	if got, want := result.ControlStderr, "execution canceled"; got != want {
		t.Fatalf("ControlStderr = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution canceled") {
		t.Fatalf("Stderr = %q, want cancellation message", result.Stderr)
	}
}

func TestInvocationExecTimeoutIsScopedToSubexecution(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Registry: registryWithCommands(t, &timeoutProbe{}),
		Policy: policy.NewStatic(&policy.Config{
			AllowedCommands: []string{"echo", "sleep", "timeoutprobe"},
			ReadRoots:       []string{"/"},
			WriteRoots:      []string{"/"},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeoutprobe\necho after\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "exit=124\nafter\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want nested timeout message", result.Stderr)
	}
}

func TestRedirectPolicyDenialReturns126(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{defaultHomeDir},
			WriteRoots: []string{defaultHomeDir},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo hi > /tmp/out\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, `write "/tmp/out" denied`) {
		t.Fatalf("Stderr = %q, want redirect policy denial message", result.Stderr)
	}
}

func TestCommandResolutionPolicyDenialReturns126(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{defaultHomeDir},
			WriteRoots: []string{defaultHomeDir},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "/bin/echo hi\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, `stat "/bin/echo" denied`) {
		t.Fatalf("Stderr = %q, want command-resolution policy denial message", result.Stderr)
	}
}

var _ commands.Command = (*timeoutProbe)(nil)
var _ gbfs.File = (*blockingScriptFile)(nil)
