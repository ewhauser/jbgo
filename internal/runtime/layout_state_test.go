package runtime

import (
	"context"
	stdfs "io/fs"
	"sync/atomic"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestSessionExecSkipsLayoutReinitializationWhenUnchanged(t *testing.T) {
	ctx := context.Background()
	var tracked *statCountingFS

	rt := newRuntime(t, &Config{
		FileSystem: CustomFileSystem(gbfs.FactoryFunc(func(context.Context) (gbfs.FileSystem, error) {
			tracked = &statCountingFS{FileSystem: gbfs.NewMemory()}
			return tracked, nil
		}), defaultHomeDir),
	})

	session, err := rt.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	tracked.stats.Store(0)
	result, err := session.Exec(ctx, &ExecutionRequest{Script: ""})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := tracked.stats.Load(); got != 0 {
		t.Fatalf("Stat() calls = %d, want 0", got)
	}
}

func TestSessionExecRebuildsLayoutAfterCommandStubMutation(t *testing.T) {
	session := newSession(t, nil)

	result := mustExecSession(t, session, "rm /bin/echo\n")
	if result.ExitCode != 0 {
		t.Fatalf("first ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	result = mustExecSession(t, session, "echo restored\n")
	if result.ExitCode != 0 {
		t.Fatalf("second ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "restored\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/bin/echo"); err != nil {
		t.Fatalf("Stat(/bin/echo) error = %v", err)
	}
}

type statCountingFS struct {
	gbfs.FileSystem
	stats atomic.Int32
}

func (fs *statCountingFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	fs.stats.Add(1)
	return fs.FileSystem.Stat(ctx, name)
}
