package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestTRSupportsLongDeleteAndSqueezeFlagsIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'aaabbbccc' | tr --squeeze-repeats abc\nprintf 'abc123' | tr --delete '[:alpha:]'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "abc123"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTRSupportsExplicitRepeatConstructsInString1(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a=c' | tr 'a[=*2][=c=]' xyyz\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "xyz"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTRRejectsStarRepeatConstructInString1(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'bb' | tr '[b*]' x\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
	if got, want := result.Stderr, "tr: the [c*] repeat construct may not appear in string1\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestTRRejectsInvalidCharacterClass(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'abc' | tr '[:bogus:]' x\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
	if got, want := result.Stderr, "tr: invalid character class 'bogus'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestTRWarnsAndFallsBackForAmbiguousOctalEscapes(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'X' | tr X '\\400'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " "; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if want := "tr: warning: the ambiguous octal escape \\400 is being interpreted as the 2-byte sequence \\040, 0\n"; result.Stderr != want {
		t.Fatalf("Stderr = %q, want %q", result.Stderr, want)
	}
}

func TestTRUsesGNUOperandDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		script     string
		wantStderr string
	}{
		{
			name:       "missing second operand for delete and squeeze",
			script:     "tr -ds a-z\n",
			wantStderr: "tr: missing operand after 'a-z'\nTwo strings must be given when both deleting and squeezing repeats.\nTry 'tr --help' for more information.\n",
		},
		{
			name:       "extra operand for delete without squeeze",
			script:     "tr -d a b\n",
			wantStderr: "tr: extra operand 'b'\nOnly one string may be given when deleting without squeezing repeats.\nTry 'tr --help' for more information.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rt := newRuntime(t, &Config{})
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tt.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got, want := result.ExitCode, 1; got != want {
				t.Fatalf("ExitCode = %d, want %d", got, want)
			}
			if got := result.Stdout; got != "" {
				t.Fatalf("Stdout = %q, want empty", got)
			}
			if got := result.Stderr; got != tt.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}

func TestTRRangeParsingWarnsOnlyOncePerAmbiguousEscape(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "tr '\\400-\\401' x\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}
	if got := strings.Count(result.Stderr, "ambiguous octal escape"); got != 2 {
		t.Fatalf("ambiguous warning count = %d, want 2; stderr=%q", got, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "range-endpoints of '0- ' are in reverse collating sequence order") {
		t.Fatalf("Stderr = %q, want reverse-range diagnostic", result.Stderr)
	}
}
