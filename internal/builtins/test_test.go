package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestTestSupportsStringIntegerAndBooleanExpressions(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"test foo = foo && echo string\n"+
			"test 42 -eq ' 42 ' && echo integer\n"+
			"test ! '' && echo bang\n"+
			"test x -o '' -a '' && echo precedence\n"+
			"test '(' = '(' && echo paren\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "string\ninteger\nbang\nprecedence\nparen\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTestSupportsFilePredicatesAndBracketAlias(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/file.txt", []byte("payload\n"))
	if err := session.FileSystem().Symlink(context.Background(), "file.txt", "/tmp/file.link"); err != nil {
		t.Fatalf("Symlink(file.link) error = %v", err)
	}

	result := mustExecSession(t, session,
		"test -e /tmp/file.txt && echo exists\n"+
			"test -s /tmp/file.txt && echo nonempty\n"+
			"test -f /tmp/file.txt && echo regular\n"+
			"test -h /tmp/file.link && echo symlink\n"+
			"[ /tmp/file.txt -ef /tmp/file.txt ] && echo same\n"+
			"[ /tmp/file.txt -nt /tmp/missing.txt ] && echo newer\n"+
			"[ -e /tmp/file.txt ] && echo bracket\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "exists\nnonempty\nregular\nsymlink\nsame\nnewer\nbracket\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTestOwnerPredicatesUseSandboxOwnership(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"echo hi > /tmp/file.txt\n"+
			"test -O /tmp/file.txt && echo owner\n"+
			"test -G /tmp/file.txt && echo group\n"+
			"chown 123:456 /tmp/file.txt\n"+
			"test -O /tmp/file.txt && echo owner-after\n"+
			"test -G /tmp/file.txt && echo group-after\n",
	)
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "owner\ngroup\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTestReportsParseErrorsAndBracketMismatch(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "test value =\n")
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	// Accept both "missing argument after '='" (registry) and "= must be followed by a word" (interp)
	if !strings.Contains(result.Stderr, "missing argument") && !strings.Contains(result.Stderr, "must be followed by") {
		t.Fatalf("Stderr = %q, want error about missing argument after '='", result.Stderr)
	}

	result = mustExecSession(t, session, "[ 1 -eq 1\n")
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	// Accept both "missing ']'" (registry) and "missing matching ]" (interp)
	if !strings.Contains(result.Stderr, "missing") || !strings.Contains(result.Stderr, "]") {
		t.Fatalf("Stderr = %q, want missing-closing-bracket error", result.Stderr)
	}
}

func TestTestMatchesBashAmbiguousClassicForms(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"[ -a -a -a ] && echo triple\n"+
			"[ -a -a -a -a -a ] || echo quint\n"+
			"test 0 -eq 0 -a '(' = ')' && echo paren_eq\n"+
			"set -- -o; test $# -ne 0 -a \"$1\" != \"--\" && echo trailing_word\n"+
			"[ -f = ] || echo file_eq\n"+
			"[ -f == ] || echo file_eqeq\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "triple\nquint\nparen_eq\ntrailing_word\nfile_eq\nfile_eqeq\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
