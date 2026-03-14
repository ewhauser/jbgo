package builtins_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSplitFilterStreamsRoundRobinOutput(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n2\\n3\\n4\\n5\\n' | split -nr/2 --filter='cat'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "1\n3\n5\n2\n4\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestCsplitSplitsStdinByLineNumber(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf '1\\n2\\n3\\n4\\n5\\n' | csplit - 3\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "4\n6\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := string(readSessionFile(t, session, "/home/agent/xx00")); got != "1\n2\n" {
		t.Fatalf("xx00 = %q, want %q", got, "1\n2\n")
	}
	if got := string(readSessionFile(t, session, "/home/agent/xx01")); got != "3\n4\n5\n" {
		t.Fatalf("xx01 = %q, want %q", got, "3\n4\n5\n")
	}
}

func TestCsplitHandlesInputWithoutTrailingNewline(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'a\\nb\\nc\\nd' | csplit - 2\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2\n5\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCsplitSupportsSuffixFormattingAndGroupedAliases(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte("1\n2\n3\n4\n5\n"))

	result := mustExecSession(t, session, "csplit -szkn3 -b%03x -f /tmp/out- /tmp/in.txt 3\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty quiet output", got)
	}
	if got := string(readSessionFile(t, session, "/tmp/out-000")); got != "1\n2\n" {
		t.Fatalf("out-000 = %q, want %q", got, "1\n2\n")
	}
	if got := string(readSessionFile(t, session, "/tmp/out-001")); got != "3\n4\n5\n" {
		t.Fatalf("out-001 = %q, want %q", got, "3\n4\n5\n")
	}
}

func TestCsplitSupportsPrecisionSuffixFormat(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte(csplitNumbers(1, 5)))

	result := mustExecSession(t, session, "csplit --prefix=/tmp/hex- --suffix-format=%#6.3x /tmp/in.txt 2\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2\n6\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := string(readSessionFile(t, session, "/tmp/hex-   000")); got != "1\n" {
		t.Fatalf("hex-000 = %q, want %q", got, "1\n")
	}
	if got := string(readSessionFile(t, session, "/tmp/hex- 0x001")); got != "2\n3\n4\n" {
		t.Fatalf("hex-001 = %q, want %q", got, "2\n3\n4\n")
	}
}

func TestCsplitSuppressMatchedElidesFinalEmptyFile(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf '1\\n2\\n3\\n4\\n' | csplit --suppress-matched -z - 2 4\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2\n2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := string(readSessionFile(t, session, "/home/agent/xx00")); got != "1\n" {
		t.Fatalf("xx00 = %q, want %q", got, "1\n")
	}
	if got := string(readSessionFile(t, session, "/home/agent/xx01")); got != "3\n" {
		t.Fatalf("xx01 = %q, want %q", got, "3\n")
	}
	missing := mustExecSession(t, session, "test ! -e /home/agent/xx02\n")
	if missing.ExitCode != 0 {
		t.Fatalf("xx02 unexpectedly exists; stderr=%q", missing.Stderr)
	}
}

func TestCsplitSuppressMatchedRegexNegativeOffset(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte(csplitNumbers(1, 13)))

	result := mustExecSession(t, session, "csplit --suppress-matched /tmp/in.txt /10/-4\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "10\n15\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := string(readSessionFile(t, session, "/home/agent/xx00")); got != csplitNumbers(1, 6) {
		t.Fatalf("xx00 = %q, want %q", got, csplitNumbers(1, 6))
	}
	if got := string(readSessionFile(t, session, "/home/agent/xx01")); got != csplitNumbers(7, 13) {
		t.Fatalf("xx01 = %q, want %q", got, csplitNumbers(7, 13))
	}
}

func TestCsplitKeepsFilesOnErrorWithKeepFiles(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte(csplitNumbers(1, 6)))

	result := mustExecSession(t, session, "csplit -k /tmp/in.txt /3/ /nope/\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stdout, "4\n6\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "csplit: '/nope/': match not found\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	if got := string(readSessionFile(t, session, "/home/agent/xx00")); got != "1\n2\n" {
		t.Fatalf("xx00 = %q, want %q", got, "1\n2\n")
	}
	if got := string(readSessionFile(t, session, "/home/agent/xx01")); got != "3\n4\n5\n" {
		t.Fatalf("xx01 = %q, want %q", got, "3\n4\n5\n")
	}
}

func TestCsplitRemovesFilesOnErrorByDefault(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte(csplitNumbers(1, 6)))

	result := mustExecSession(t, session, "csplit /tmp/in.txt /3/ /nope/\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stderr, "csplit: '/nope/': match not found\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	missing := mustExecSession(t, session, "test ! -e /home/agent/xx00 && test ! -e /home/agent/xx01\n")
	if missing.ExitCode != 0 {
		t.Fatalf("expected cleanup to remove split files; stderr=%q", missing.Stderr)
	}
}

func csplitNumbers(from, to int) string {
	var b strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	return b.String()
}
