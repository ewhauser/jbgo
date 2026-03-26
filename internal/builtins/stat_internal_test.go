package builtins

import (
	"bytes"
	"context"
	"errors"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
)

type unsupportedRealpathFS struct {
	gbfs.FileSystem
}

func (unsupportedRealpathFS) Realpath(context.Context, string) (string, error) {
	return "", errors.New("realpath unsupported")
}

func TestStatRelativeDereferenceAndTrailingSlashDoNotRequireRealpath(t *testing.T) {
	t.Parallel()

	mem := gbfs.NewMemory()
	if err := mem.MkdirAll(context.Background(), "/home/agent/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll(dir) error = %v", err)
	}
	if err := mem.Symlink(context.Background(), "dir", "/home/agent/link"); err != nil {
		t.Fatalf("Symlink(link) error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Args:       []string{"-L", "--format=%F", "link"},
		Cwd:        "/home/agent",
		FileSystem: unsupportedRealpathFS{FileSystem: mem},
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	err := NewStat().Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run(-L) error = %v; stderr=%q", err, stderr.String())
	}
	if got, want := stdout.String(), "directory\n"; got != want {
		t.Fatalf("stdout(-L) = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr(-L) = %q, want empty", got)
	}

	stdout.Reset()
	stderr.Reset()
	inv.Args = []string{"--format=%F", "link/"}

	err = NewStat().Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run(trailing slash) error = %v; stderr=%q", err, stderr.String())
	}
	if got, want := stdout.String(), "directory\n"; got != want {
		t.Fatalf("stdout(trailing slash) = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr(trailing slash) = %q, want empty", got)
	}
}
