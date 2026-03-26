package builtins_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ewhauser/gbash/internal/builtins"
)

type errWriter struct {
	err error
}

func (w errWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func TestPrintfSupportsBashNumericCharConstants(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"single=\"'A\"\n"+
			"double='\"B'\n"+
			"printf '%d|%i|%o|%u|%x|%X|%.1f|%g\\n' \"$single\" \"$single\" \"$single\" \"$single\" \"$single\" \"$single\" \"$single\" \"$double\"\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "65|65|101|65|41|41|65.0|66\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPrintfCharacterFormatUsesFirstCharacter(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"quoted=\"'B\"\n"+
			"printf '%c%c%c%c' A 65 \"$quoted\" '' | od -An -tx1 -v\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 41 36 27 00\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPrintfWriteFailureReturnsExitStatusOne(t *testing.T) {
	t.Parallel()

	cmd := builtins.NewPrintf()
	err := cmd.Run(context.Background(), &builtins.Invocation{
		Args:   []string{"%s", "hi"},
		Stdout: errWriter{err: errors.New("sink failed")},
	})
	var exitErr *builtins.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want *ExitError", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("ExitError.Code = %d, want 1", exitErr.Code)
	}
}

func TestPrintfNoArgsMatchesGNUOperandDiagnostic(t *testing.T) {
	t.Parallel()

	cmd := builtins.NewPrintf()
	var stderr bytes.Buffer
	err := cmd.Run(context.Background(), &builtins.Invocation{Stderr: &stderr})
	var exitErr *builtins.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want *ExitError", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("ExitError.Code = %d, want 1", exitErr.Code)
	}
	if got, want := stderr.String(), "printf: missing operand\nTry 'printf --help' for more information.\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestPrintfTreatsDashVAsLiteralGNUFormat(t *testing.T) {
	t.Parallel()

	cmd := builtins.NewPrintf()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := cmd.Run(context.Background(), &builtins.Invocation{
		Args:   []string{"-v", "foo", "%s", "hi"},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), "-v"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "printf: warning: ignoring excess arguments, starting with 'foo'\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestPrintfRejectsShellOnlyGNUConversion(t *testing.T) {
	t.Parallel()

	cmd := builtins.NewPrintf()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := cmd.Run(context.Background(), &builtins.Invocation{
		Args:   []string{"%(%F)T", "-1"},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	var exitErr *builtins.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want *ExitError", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("ExitError.Code = %d, want 1", exitErr.Code)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "printf: %(: invalid conversion specification\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestPrintfStopsOnMalformedGNUPercentBEscape(t *testing.T) {
	t.Parallel()

	cmd := builtins.NewPrintf()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := cmd.Run(context.Background(), &builtins.Invocation{
		Args:   []string{"%b|%s", "A\\xZ", "B"},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	var exitErr *builtins.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want *ExitError", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("ExitError.Code = %d, want 1", exitErr.Code)
	}
	if got, want := stdout.String(), "A"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "printf: missing hexadecimal number in escape\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestPrintfBuiltinAndBinPrintfSplitDashVMode(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "foo=old\n" +
			"printf -v foo %s hi\n" +
			"printf 'builtin=<%s>\\n' \"$foo\"\n" +
			"/bin/printf -v foo %s hi\n" +
			"printf '\\n/bin=<%s>\\n' \"$foo\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "builtin=<hi>\n-v\n/bin=<hi>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "printf: warning: ignoring excess arguments, starting with 'foo'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestPrintfBuiltinAndBinPrintfSplitQQuoting(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'builtin=[%q]\\n' 'a b'\n" +
			"/bin/printf 'bin=[%q]\\n' 'a b'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "builtin=[a\\ b]\nbin=['a b']\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestPrintfBuiltinAndBinPrintfSplitTimeConversion(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "export TZ=UTC\n" +
			"printf 'builtin=[%(%F)T]\\n' 0\n" +
			"/bin/printf '%(%F)T' 0\n" +
			"printf '\\nstatus=%s\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "builtin=[1970-01-01]\n\nstatus=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "printf: %(: invalid conversion specification\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBinPrintfSupportsGNUIndexedFormats(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "/bin/printf '%2$s%1$s\\n' 1 2\n" +
			"/bin/printf '%1$*2$.*3$d\\n' 1 3 2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "21\n 01\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}
