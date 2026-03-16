package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestXArgsLongFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\0b\\0' | xargs --null --verbose --max-args 1 echo\n" +
			"printf '' | xargs --no-run-if-empty echo skip\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "'echo' 'a'\n'echo' 'b'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestXArgsUsesCommandSpecHelp(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "xargs --help\n"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Build and execute command lines from standard input.") {
		t.Fatalf("Stdout = %q, want help text from command spec", result.Stdout)
	}
	if strings.Contains(result.Stdout, "Run COMMAND with arguments built from standard input.") {
		t.Fatalf("Stdout still contains the old static help text: %q", result.Stdout)
	}
}

func TestXArgsParsesQuotesAndEscapes(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/xargs-quotes.txt", []byte("   this is\n\"quoted \tstuff\"  \nand \\\nan embedded   newline\nwith 'single\tquotes' as well.\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "xargs < /tmp/xargs-quotes.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "this is quoted \tstuff and \nan embedded newline with single\tquotes as well.\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestXArgsRunsCompletedInputBeforeReportingUnmatchedQuotes(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/xargs-unmatched.txt", []byte("one\n\"two\nthree\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "xargs < /tmp/xargs-unmatched.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "unmatched double quote") {
		t.Fatalf("Stderr = %q, want unmatched quote diagnostic", result.Stderr)
	}
}

func TestXArgsStopsAtLogicalEOF(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\nSTOP\\nthree\\n' | xargs -E STOP echo\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one two\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestXArgsRespectsMaxLinesWithTrailingBlankContinuation(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/xargs-lines.txt", []byte("one \ntwo\nthree\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "xargs -L1 echo < /tmp/xargs-lines.txt\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one two\nthree\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestXArgsDelimiterModePreservesEmptyItems(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a,b,,c,' | xargs -d , -n1 echo\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\n\nc\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestXArgsArgFileLeavesChildStdinAvailable(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/xargs-args.txt", []byte("left right\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "printf 'child-stdin\\n' | xargs -a /tmp/xargs-args.txt sh -c 'cat; printf \"%s %s\\\\n\" \"$1\" \"$2\"' _\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "child-stdin\nleft right\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestXArgsWarnsForConflictingBatchOptions(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n2\\n3\\n4\\n' | xargs -L2 -n3\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1 2 3\n4\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "xargs: warning: options --max-lines and --max-args/-n are mutually exclusive, ignoring previous --max-lines value\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestXArgsIgnoresN1AfterReplaceWithoutWarning(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n2\\n' | xargs --replace -n1 echo a-{}-b\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a-1-b\na-2-b\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestXArgsMapsChildFailuresToGNUExitCodes(t *testing.T) {
	rt := newRuntime(t, &Config{})

	nonzero, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'false\\necho ok\\n' | xargs -IARG sh -c ARG\n",
	})
	if err != nil {
		t.Fatalf("Run(nonzero) error = %v", err)
	}
	if nonzero.ExitCode != 123 {
		t.Fatalf("nonzero ExitCode = %d, want 123; stderr=%q", nonzero.ExitCode, nonzero.Stderr)
	}
	if got, want := nonzero.Stdout, "ok\n"; got != want {
		t.Fatalf("nonzero Stdout = %q, want %q", got, want)
	}

	abort255, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'exit 255\\necho nope\\n' | xargs -IARG sh -c ARG\n",
	})
	if err != nil {
		t.Fatalf("Run(abort255) error = %v", err)
	}
	if abort255.ExitCode != 124 {
		t.Fatalf("abort255 ExitCode = %d, want 124; stderr=%q", abort255.ExitCode, abort255.Stderr)
	}
	if abort255.Stdout != "" {
		t.Fatalf("abort255 Stdout = %q, want empty", abort255.Stdout)
	}
	if !strings.Contains(abort255.Stderr, "exited with status 255; aborting") {
		t.Fatalf("abort255 Stderr = %q, want aborting diagnostic", abort255.Stderr)
	}

	notFound, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'item\\n' | xargs definitely-missing-command\n",
	})
	if err != nil {
		t.Fatalf("Run(notFound) error = %v", err)
	}
	if notFound.ExitCode != 127 {
		t.Fatalf("notFound ExitCode = %d, want 127; stderr=%q", notFound.ExitCode, notFound.Stderr)
	}
	if !strings.Contains(notFound.Stderr, "No such file or directory") {
		t.Fatalf("notFound Stderr = %q, want missing command diagnostic", notFound.Stderr)
	}
}

func TestXArgsAcceptsMaxProcsFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '/bin/sleep 2 && /bin/echo one\\n/bin/sleep 1 && /bin/echo two\\n/bin/echo three\\n' | xargs -P3 -n1 -IARG /bin/sh -c ARG\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one\ntwo\nthree\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "xargs: warning: options --max-args and --replace/-I/-i are mutually exclusive, ignoring previous --max-args value\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestXArgsRunsShebangScriptViaDirectExec(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/xargs-script.sh", []byte("#!/bin/sh\nprintf '%s:%s\\n' \"$1\" \"$2\"\n"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "chmod 755 /tmp/xargs-script.sh\nprintf 'left\\nright\\n' | xargs -n1 /tmp/xargs-script.sh fixed\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "fixed:left\nfixed:right\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
