package builtins_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestUnexpandHelpAndVersion(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})

	helpResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "unexpand --help --tabs=x\n"})
	if err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	if helpResult.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", helpResult.ExitCode, helpResult.Stderr)
	}
	for _, want := range []string{
		"Usage: unexpand [OPTION]... [FILE]...\n",
		"  -a, --all         convert all blanks, instead of just initial blanks\n",
		"      --first-only  convert only leading sequences of blanks (overrides -a)\n",
		"  -t, --tabs=N      have tabs N characters apart instead of 8 (enables -a)\n",
		"      --version     output version information and exit\n",
	} {
		if !strings.Contains(helpResult.Stdout, want) {
			t.Fatalf("help stdout = %q, want substring %q", helpResult.Stdout, want)
		}
	}

	versionResult, err := rt.Run(context.Background(), &ExecutionRequest{Script: "unexpand --version --tabs=x\n"})
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if versionResult.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", versionResult.ExitCode, versionResult.Stderr)
	}
	if got, want := versionResult.Stdout, "unexpand (gbash)\n"; got != want {
		t.Fatalf("version stdout = %q, want %q", got, want)
	}
}

func TestUnexpandTransformsFilesAndStdin(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte("        A     B\n123 \t1\n"))

	fileResult := mustExecSession(t, session, "unexpand --tabs=3 /tmp/in.txt\n")
	if fileResult.ExitCode != 0 {
		t.Fatalf("file ExitCode = %d, want 0; stderr=%q", fileResult.ExitCode, fileResult.Stderr)
	}
	if got, want := fileResult.Stdout, "\t\t  A\t  B\n123\t1\n"; got != want {
		t.Fatalf("file stdout = %q, want %q", got, want)
	}

	firstOnlyResult := mustExecSession(t, session, "printf '        A     B' | unexpand -3\n")
	if firstOnlyResult.ExitCode != 0 {
		t.Fatalf("firstOnly ExitCode = %d, want 0; stderr=%q", firstOnlyResult.ExitCode, firstOnlyResult.Stderr)
	}
	if got, want := firstOnlyResult.Stdout, "\t\t  A     B"; got != want {
		t.Fatalf("firstOnly stdout = %q, want %q", got, want)
	}

	stdinResult := mustExecSession(t, session, "printf 'a  b  c' | unexpand -a -3 -\n")
	if stdinResult.ExitCode != 0 {
		t.Fatalf("stdin ExitCode = %d, want 0; stderr=%q", stdinResult.ExitCode, stdinResult.Stderr)
	}
	if got, want := stdinResult.Stdout, "a\tb\tc"; got != want {
		t.Fatalf("stdin stdout = %q, want %q", got, want)
	}

	repeatedStdin := mustExecSession(t, session, "printf '        A' | unexpand -3 - -\n")
	if repeatedStdin.ExitCode != 0 {
		t.Fatalf("repeatedStdin ExitCode = %d, want 0; stderr=%q", repeatedStdin.ExitCode, repeatedStdin.Stderr)
	}
	if got, want := repeatedStdin.Stdout, "\t\t  A"; got != want {
		t.Fatalf("repeatedStdin stdout = %q, want %q", got, want)
	}
}

func TestUnexpandTabListsAndGNUCases(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"printf '  2\\n' | unexpand --tabs '2 4'\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\t      ' | unexpand -t '3,+6'\n" +
			"printf '%s\\n' ---\n" +
			"printf '\\t      ' | unexpand -t '3,/9'\n" +
			"printf '%s\\n' ---\n" +
			"printf '          ' | unexpand -t '3,+0'\n" +
			"printf '%s\\n' ---\n" +
			"printf '          ' | unexpand -t '3,/0'\n" +
			"printf '%s\\n' ---\n" +
			"printf '1ΔΔΔ5   99999\\n' | unexpand -a\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "" +
		"\t2\n" +
		"---\n" +
		"\t\t" +
		"---\n" +
		"\t\t" +
		"---\n" +
		"\t\t\t " +
		"---\n" +
		"\t\t\t " +
		"---\n" +
		"1ΔΔΔ5   99999\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestUnexpandHandlesHugeTabstopWithoutPanicking(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	maxInt := strconv.FormatUint(uint64(^uint(0)>>1), 10)
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '' | unexpand --tabs=" + maxInt + "\n",
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

func TestUnexpandErrorsMatchCoreutilsStyle(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})

	invalidFlag, err := rt.Run(context.Background(), &ExecutionRequest{Script: "unexpand -f\n"})
	if err != nil {
		t.Fatalf("Run(invalidFlag) error = %v", err)
	}
	if invalidFlag.ExitCode != 1 {
		t.Fatalf("invalidFlag ExitCode = %d, want 1", invalidFlag.ExitCode)
	}
	if got, want := invalidFlag.Stderr, "unexpand: invalid option -- 'f'\nTry 'unexpand --help' for more information.\n"; got != want {
		t.Fatalf("invalidFlag stderr = %q, want %q", got, want)
	}

	invalidTabs, err := rt.Run(context.Background(), &ExecutionRequest{Script: "unexpand --tabs=1,+2,3\n"})
	if err != nil {
		t.Fatalf("Run(invalidTabs) error = %v", err)
	}
	if invalidTabs.ExitCode != 1 {
		t.Fatalf("invalidTabs ExitCode = %d, want 1", invalidTabs.ExitCode)
	}
	if got, want := invalidTabs.Stderr, "unexpand: '+' specifier only allowed with the last value\n"; got != want {
		t.Fatalf("invalidTabs stderr = %q, want %q", got, want)
	}

	invalidChar, err := rt.Run(context.Background(), &ExecutionRequest{Script: "unexpand --tabs=x\n"})
	if err != nil {
		t.Fatalf("Run(invalidChar) error = %v", err)
	}
	if invalidChar.ExitCode != 1 {
		t.Fatalf("invalidChar ExitCode = %d, want 1", invalidChar.ExitCode)
	}
	if got, want := invalidChar.Stderr, "unexpand: tab size contains invalid character(s): 'x'\n"; got != want {
		t.Fatalf("invalidChar stderr = %q, want %q", got, want)
	}

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/ok.txt", []byte("        a\n"))
	multiResult := mustExecSession(t, session, "mkdir /tmp/dir\nunexpand /tmp/ok.txt /tmp/dir /tmp/missing.txt\n")
	if multiResult.ExitCode != 1 {
		t.Fatalf("multiResult ExitCode = %d, want 1; stderr=%q", multiResult.ExitCode, multiResult.Stderr)
	}
	if got, want := multiResult.Stdout, "\ta\n"; got != want {
		t.Fatalf("multiResult stdout = %q, want %q", got, want)
	}
	for _, want := range []string{
		"unexpand: /tmp/dir: Is a directory\n",
		"unexpand: /tmp/missing.txt: No such file or directory\n",
	} {
		if !strings.Contains(multiResult.Stderr, want) {
			t.Fatalf("multiResult stderr = %q, want substring %q", multiResult.Stderr, want)
		}
	}
}
