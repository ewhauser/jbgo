package runtime

import (
	"runtime"
	"testing"
)

func TestArithmCommandRegressionIncludesStandaloneExpression(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "(( '1' ))\n")
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", got, want, result.Stdout, result.Stderr)
	}
	if got, want := result.Stderr, ""; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestArithmForLoopRegressionUsesArithmeticCommandPrefixForInit(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "for ((i='1'; i<2; i++)); do break; done\n")
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", got, want, result.Stdout, result.Stderr)
	}
	if got, want := result.Stderr, ""; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestArithmForLoopRegressionUsesArithmeticCommandPrefixForCond(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "for ((i=0; i<'2'; i++)); do :; done\n")
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", got, want, result.Stdout, result.Stderr)
	}
	if got, want := result.Stderr, ""; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestArithmForLoopRegressionUsesArithmeticCommandPrefixForPost(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "for ((i=0; i<1; '1')); do i=1; done\n")
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", got, want, result.Stdout, result.Stderr)
	}
	if got, want := result.Stderr, ""; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestArithmCommandRegressionPreservesReadonlyVariableError(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "readonly x=1\n((x=2))\n")
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stderr, "x: readonly variable\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestArithmForLoopRegressionPreservesReadonlyVariableError(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "readonly x=1\nfor ((x=2; 0; x++)); do :; done\n")
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stderr, "x: readonly variable\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestArithmExpansionNounsetIndexedRefUsesBaseName(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "set -o nounset\necho $(( undef[0] ))\n")
	if got, want := result.ExitCode, 127; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := result.Stdout, ""; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "undef: unbound variable\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestArithmCommandRegressionPreservesParenAmbiguityParseError(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "(( echo 1\necho 2\n(( x ))\n: $(( x ))\necho 3\n))\n")
	if got, want := result.ExitCode, 1; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	want := "echo 1\necho 2\n(( x ))\n: 0\necho 3\n: syntax error in expression (error token is \"1\necho 2\n(( x ))\n: 0\necho 3\n\")\n"
	if runtime.GOOS != "darwin" {
		want = "((: " + want
	}
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestArithmCommandRegressionDoesNotAbortFollowingCommands(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "(( echo 1\necho 2\n(( x ))\n: $(( x ))\necho 3\n))\necho after\n")
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", got, want, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "after\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got == "" {
		t.Fatal("Stderr = empty, want arithmetic diagnostic")
	}
}
