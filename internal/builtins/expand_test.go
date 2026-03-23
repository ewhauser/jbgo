package builtins_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestExpandHelpAndVersion(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})

	helpResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "expand --help --tabs=x\n"})
	if err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	if helpResult.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", helpResult.ExitCode, helpResult.Stderr)
	}
	for _, want := range []string{
		"Usage: expand [OPTION]... [FILE]...\n",
		"  -i, --initial    do not convert tabs after non blanks\n",
		"  -t, --tabs=N     have tabs N characters apart, not 8\n",
		"      --version    output version information and exit\n",
	} {
		if !strings.Contains(helpResult.Stdout, want) {
			t.Fatalf("help stdout = %q, want substring %q", helpResult.Stdout, want)
		}
	}

	versionResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "expand --version --tabs=x\n"})
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if versionResult.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", versionResult.ExitCode, versionResult.Stderr)
	}
	if got, want := versionResult.Stdout, "expand (gbash)\n"; got != want {
		t.Fatalf("version stdout = %q, want %q", got, want)
	}
}

func TestExpandTransformsFilesAndStdin(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte("a\tb\n\tlead\ttrail\n"))

	fileResult := mustExecSession(t, session, "expand --tabs=4 /tmp/in.txt\n")
	if fileResult.ExitCode != 0 {
		t.Fatalf("file ExitCode = %d, want 0; stderr=%q", fileResult.ExitCode, fileResult.Stderr)
	}
	if got, want := fileResult.Stdout, "a   b\n    lead    trail\n"; got != want {
		t.Fatalf("file stdout = %q, want %q", got, want)
	}

	initialResult := mustExecSession(t, session, "printf '\\ta\\tb' | expand -i --tabs=4\n")
	if initialResult.ExitCode != 0 {
		t.Fatalf("initial ExitCode = %d, want 0; stderr=%q", initialResult.ExitCode, initialResult.Stderr)
	}
	if got, want := initialResult.Stdout, "    a\tb"; got != want {
		t.Fatalf("initial stdout = %q, want %q", got, want)
	}

	stdinResult := mustExecSession(t, session, "printf 'x\\ty' | expand --tabs=4 -\n")
	if stdinResult.ExitCode != 0 {
		t.Fatalf("stdin ExitCode = %d, want 0; stderr=%q", stdinResult.ExitCode, stdinResult.Stderr)
	}
	if got, want := stdinResult.Stdout, "x   y"; got != want {
		t.Fatalf("stdin stdout = %q, want %q", got, want)
	}

	repeatedStdin := mustExecSession(t, session, "printf 'x\\ty' | expand --tabs=4 - -\n")
	if repeatedStdin.ExitCode != 0 {
		t.Fatalf("repeatedStdin ExitCode = %d, want 0; stderr=%q", repeatedStdin.ExitCode, repeatedStdin.Stderr)
	}
	if got, want := repeatedStdin.Stdout, "x   y"; got != want {
		t.Fatalf("repeatedStdin stdout = %q, want %q", got, want)
	}
}

func TestExpandTabListsAndShortcuts(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"printf 'a\\tb\\tc\\td\\te' | expand --tabs '3 6 9'\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\ta\\tb\\tc' | expand --tabs=1,/5\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\ta\\tb\\tc' | expand --tabs=1,+5\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\ta\\tb\\tc' | expand --tabs=8,/4\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\ta\\tb\\tc' | expand --tabs=8,+4\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\ta\\tb\\tc' | expand -2,5 -7\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\ta\\tb\\tc' | expand -8,/4\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\ta\\tb\\tc' | expand -1,+5\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "" +
		"a  b  c  d e---\n" +
		" a   b    c---\n" +
		" a    b    c---\n" +
		"        a   b   c---\n" +
		"        a   b   c---\n" +
		"  a  b c---\n" +
		"        a   b   c---\n" +
		" a    b    c"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestExpandHandlesHugeTabstopWithoutPanicking(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	maxInt := strconv.FormatUint(uint64(^uint(0)>>1), 10)
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '' | expand --tabs=" + maxInt + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
}

func TestExpandErrorsMatchCoreutilsStyle(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})

	invalidFlag, err := rt.Run(context.Background(), &ExecutionRequest{Script: "expand -h\n"})
	if err != nil {
		t.Fatalf("Run(invalidFlag) error = %v", err)
	}
	if invalidFlag.ExitCode != 1 {
		t.Fatalf("invalidFlag ExitCode = %d, want 1", invalidFlag.ExitCode)
	}
	if got, want := invalidFlag.Stderr, "expand: invalid option -- 'h'\nTry 'expand --help' for more information.\n"; got != want {
		t.Fatalf("invalidFlag stderr = %q, want %q", got, want)
	}

	invalidTabs, err := rt.Run(context.Background(), &ExecutionRequest{Script: "expand --tabs=1,+2,3\n"})
	if err != nil {
		t.Fatalf("Run(invalidTabs) error = %v", err)
	}
	if invalidTabs.ExitCode != 1 {
		t.Fatalf("invalidTabs ExitCode = %d, want 1", invalidTabs.ExitCode)
	}
	if got, want := invalidTabs.Stderr, "expand: '+' specifier only allowed with the last value\n"; got != want {
		t.Fatalf("invalidTabs stderr = %q, want %q", got, want)
	}

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/ok.txt", []byte("a\tb\n"))
	multiResult := mustExecSession(t, session, "mkdir /tmp/dir\nexpand --tabs=4 /tmp/ok.txt /tmp/dir /tmp/missing.txt\n")
	if multiResult.ExitCode != 1 {
		t.Fatalf("multiResult ExitCode = %d, want 1; stderr=%q", multiResult.ExitCode, multiResult.Stderr)
	}
	if got, want := multiResult.Stdout, "a   b\n"; got != want {
		t.Fatalf("multiResult stdout = %q, want %q", got, want)
	}
	for _, want := range []string{
		"expand: /tmp/dir: Is a directory\n",
		"expand: /tmp/missing.txt: No such file or directory\n",
	} {
		if !strings.Contains(multiResult.Stderr, want) {
			t.Fatalf("multiResult stderr = %q, want substring %q", multiResult.Stderr, want)
		}
	}
}
