package builtins_test

import (
	"strings"
	"testing"
)

func TestUmaskDisplayAndNumericModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "umask\numask -S\numask -p\numask 1 2\nprintf 'status=%s\\n' \"$?\"\numask\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0022\nu=rwx,g=rx,o=rx\numask 0022\nstatus=0\n0001\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestUmaskAffectsCreatedFileModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "umask 0002\necho one > /home/agent/umask-one\numask g-w,o-w\necho two > /home/agent/umask-two\nstat -c '%a' /home/agent/umask-one /home/agent/umask-two\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "664\n644\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestUmaskSupportsSymbolicModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	script := strings.Join([]string{
		"umask 0124",
		"umask a+r",
		"umask",
		"umask 0124",
		"umask a-r",
		"umask",
		"umask 0124",
		"umask a=X",
		"umask",
		"umask 0124",
		"umask a=s",
		"umask",
		"umask 0124",
		"umask +=",
		"umask",
		"umask 0124",
		"umask =+rwx+rx",
		"umask",
		"umask 0124",
		"umask a=u",
		"umask",
		"umask 0124",
		"umask a=,a=u",
		"umask",
		"umask 0124",
		"umask a+x+wr-r",
		"umask",
		"",
	}, "\n")

	result := mustExecSession(t, session, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0120\n0564\n0666\n0777\n0777\n0000\n0111\n0111\n0444\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestUmaskErrorsMatchBashAndDoNotMutate(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	script := strings.Join([]string{
		"umask 0124",
		"umask -rwx",
		"printf 'opt=%s\\n' \"$?\"",
		"umask",
		"umask b=rwx",
		"printf 'subject=%s\\n' \"$?\"",
		"umask",
		"umask 'u+r,,u-r'",
		"printf 'clause=%s\\n' \"$?\"",
		"umask",
		"umask ''",
		"printf 'empty=%s\\n' \"$?\"",
		"umask",
		"umask 089",
		"printf 'octal=%s\\n' \"$?\"",
		"umask",
		"",
	}, "\n")

	result := mustExecSession(t, session, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "opt=2\n0124\nsubject=1\n0124\nclause=1\n0124\nempty=1\n0124\noctal=1\n0124\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	wantStderr := strings.Join([]string{
		"umask: -r: invalid option",
		"umask: usage: umask [-p] [-S] [mode]",
		"umask: `b': invalid symbolic mode operator",
		"umask: `,': invalid symbolic mode operator",
		"umask: `\x00': invalid symbolic mode operator",
		"umask: 089: octal number out of range",
		"",
	}, "\n")
	if got := result.Stderr; got != wantStderr {
		t.Fatalf("Stderr = %q, want %q", got, wantStderr)
	}
}
