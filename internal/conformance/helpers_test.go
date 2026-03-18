package conformance

import (
	"context"
	"testing"

	"github.com/ewhauser/gbash/internal/testutil"
)

func TestArgvHelperScript(t *testing.T) {
	t.Parallel()

	result := runHelperCase(t, SpecCase{
		Name:   "argv",
		Script: "argv.sh 'bare' 'a b' $'-\\t-' $'-\\r-' $'-\\v-' $'-\\f-' \"'\" '\\\\'",
	})
	want := ExecutionResult{
		ExitCode: 0,
		Stdout:   "['bare', 'a b', '-\\t-', '-\\r-', '-\\x0b-', '-\\x0c-', '\\'', '\\\\\\\\']\n",
		Stderr:   "",
	}
	if result.GBash != want {
		t.Fatalf("gbash helper result = %s, want %s", formatExecutionResult(result.GBash), formatExecutionResult(want))
	}
	if result.Bash != want {
		t.Fatalf("bash helper result = %s, want %s", formatExecutionResult(result.Bash), formatExecutionResult(want))
	}
}

func TestPrintenvHelperScript(t *testing.T) {
	t.Parallel()

	result := runHelperCase(t, SpecCase{
		Name:   "printenv",
		Script: "FOO=bar printenv.sh FOO BAR",
	})
	want := ExecutionResult{
		ExitCode: 0,
		Stdout:   "bar\nNone\n",
		Stderr:   "",
	}
	if result.GBash != want {
		t.Fatalf("gbash helper result = %s, want %s", formatExecutionResult(result.GBash), formatExecutionResult(want))
	}
	if result.Bash != want {
		t.Fatalf("bash helper result = %s, want %s", formatExecutionResult(result.Bash), formatExecutionResult(want))
	}
}

func TestStdoutStderrHelperScript(t *testing.T) {
	t.Parallel()

	result := runHelperCase(t, SpecCase{
		Name:   "stdout_stderr",
		Script: "stdout_stderr.sh out err 7",
	})
	want := ExecutionResult{ExitCode: 7, Stdout: "out\n", Stderr: "err\n"}
	if result.GBash != want {
		t.Fatalf("gbash helper result = %s, want %s", formatExecutionResult(result.GBash), formatExecutionResult(want))
	}
	if result.Bash != want {
		t.Fatalf("bash helper result = %s, want %s", formatExecutionResult(result.Bash), formatExecutionResult(want))
	}
}

func TestTacHelperScript(t *testing.T) {
	t.Parallel()

	result := runHelperCase(t, SpecCase{
		Name:   "tac",
		Script: "printf '%s\\n' 1 2 3 | tac",
	})
	want := ExecutionResult{
		ExitCode: 0,
		Stdout:   "3\n2\n1\n",
		Stderr:   "",
	}
	if result.GBash != want {
		t.Fatalf("gbash helper result = %s, want %s", formatExecutionResult(result.GBash), formatExecutionResult(want))
	}
	if result.Bash != want {
		t.Fatalf("bash helper result = %s, want %s", formatExecutionResult(result.Bash), formatExecutionResult(want))
	}
}

func runHelperCase(t *testing.T, specCase SpecCase) ComparisonResult {
	t.Helper()

	bashPath := testutil.RequireConformanceBashOrSkip(t)
	result, err := RunCase(context.Background(), &SuiteConfig{
		Name:       "bash",
		BinDir:     "bin",
		OracleMode: OracleBash,
	}, bashPath, specCase)
	if err != nil {
		t.Fatalf("RunCase() error = %v", err)
	}
	return result
}
