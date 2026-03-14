package builtins

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestTtyReportsRedirectedTTYPathAndSilentMode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	inv := &Invocation{
		Stdin:  redirectedTTYReader{path: "/dev/pts/0"},
		Stdout: &stdout,
		Stderr: &stderr,
	}

	err := NewTty().Run(context.Background(), inv)
	if code, ok := ExitCode(err); ok {
		t.Fatalf("ExitCode = %d, want success; stderr=%q", code, stderr.String())
	}
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), "/dev/pts/0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	stdout.Reset()
	stderr.Reset()
	inv.Args = []string{"-s"}

	err = NewTty().Run(context.Background(), inv)
	if code, ok := ExitCode(err); ok {
		t.Fatalf("silent ExitCode = %d, want success; stderr=%q", code, stderr.String())
	}
	if err != nil {
		t.Fatalf("Run(silent) error = %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("silent output = stdout %q stderr %q, want empty", stdout.String(), stderr.String())
	}
}

func TestTtyVersionWriteFailureReturnsExit3WithoutStderr(t *testing.T) {
	var stderr bytes.Buffer
	inv := &Invocation{
		Args:   []string{"--version"},
		Stdout: failingWriter{err: io.ErrClosedPipe},
		Stderr: &stderr,
	}

	err := NewTty().Run(context.Background(), inv)
	code, ok := ExitCode(err)
	if !ok {
		t.Fatalf("ExitCode(%v) ok = false, want true", err)
	}
	if code != 3 {
		t.Fatalf("ExitCode = %d, want 3", code)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err = %v, want wrapped closed-pipe error", err)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type redirectedTTYReader struct {
	path string
}

func (r redirectedTTYReader) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (r redirectedTTYReader) RedirectPath() string {
	return r.path
}

func (r redirectedTTYReader) RedirectFlags() int {
	return 0
}

func (r redirectedTTYReader) RedirectOffset() int64 {
	return 0
}
