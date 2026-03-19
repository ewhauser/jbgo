package interp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func TestRunReaderWithMetadataRunsExitTrapOnLaterParseError(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader(`
trap 'echo cleanup' EXIT
echo work
(
`), "trap-parse.sh", "", nil)
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("Run error = %v, want syntax.ParseError", err)
	}
	if got, want := stdout.String(), "work\ncleanup\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestExitTrapExitStatusDoesNotWriteStderr(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.RunReaderWithMetadata(context.Background(), strings.NewReader(`
trap 'false' EXIT
:
`), "trap-status.sh", "", nil)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}
