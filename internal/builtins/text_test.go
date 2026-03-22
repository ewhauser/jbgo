package builtins_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/policy"
)

func TestGrepWorksInPipelineFromStdin(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo alpha > /tmp/in.txt\n echo beta >> /tmp/in.txt\n echo alpha-two >> /tmp/in.txt\n cat /tmp/in.txt | grep alpha\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "alpha\nalpha-two\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestGrepRecursiveSearchPrefixesFilenames(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p dir/sub\n echo needle > dir/root.txt\n echo another needle > dir/sub/file.txt\n grep -r needle dir\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/home/agent/dir/root.txt:needle", "/home/agent/dir/sub/file.txt:another needle"} {
		if !containsLine(strings.Split(strings.TrimSpace(result.Stdout), "\n"), want) {
			t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
		}
	}
}

func TestGrepReturnsExitCodeOneWhenNoMatch(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo hello > /tmp/in.txt\n grep missing /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("want empty output on no-match, got stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestGrepReturnsExitCodeTwoOnMissingFile(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "grep pattern /missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "/missing.txt") {
		t.Fatalf("Stderr = %q, want missing-file error", result.Stderr)
	}
}

func TestHeadReadsFirstNLines(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/in.txt\n echo two >> /tmp/in.txt\n echo three >> /tmp/in.txt\n head -n 2 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "one\ntwo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHeadStopsReadingInfinitePipelineAfterRequestedLines(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result, err := rt.Run(ctx, &ExecutionRequest{
		Script: "seq inf inf | head -n2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "inf\ninf\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHeadShowsHeadersForMultipleFiles(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo aaa > /tmp/a.txt\n echo bbb > /tmp/b.txt\n head /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "==> /tmp/a.txt <==\naaa\n\n==> /tmp/b.txt <==\nbbb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHeadAcceptsSuffixedLineCounts(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "seq 1100 > /tmp/in.txt\nhead -n1K /tmp/in.txt | wc -l\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "1024"; got != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestHeadSupportsLegacyNumericCountAndSilentAlias(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\nthree\\n' > /tmp/a.txt\nprintf 'four\\nfive\\n' > /tmp/b.txt\nhead -2 --silent /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one\ntwo\nfour\nfive\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTailSupportsFromLineSyntax(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/in.txt\n echo two >> /tmp/in.txt\n echo three >> /tmp/in.txt\n echo four >> /tmp/in.txt\n tail -n +3 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "three\nfour\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTailSupportsFromByteSyntax(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'abcdef\\n' > /tmp/in.txt\n tail -c +3 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "cdef\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTailWorksInPipeline(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/in.txt\n echo two >> /tmp/in.txt\n echo three >> /tmp/in.txt\n cat /tmp/in.txt | tail -n 2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "two\nthree\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHeadAndTailSupportLongByteAndHeaderFlags(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'abcdef\\n' > /tmp/a.txt\nprintf 'uvwxyz\\n' > /tmp/b.txt\nhead --bytes=3 --verbose /tmp/a.txt /tmp/b.txt\ntail --bytes=2 --quiet /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "==> /tmp/a.txt <==\nabc\n==> /tmp/b.txt <==\nuvwf\nz\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTailLongLinesFlagDoesNotEnableFromLineMode(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\nthree\\nfour\\n' > /tmp/in.txt\ntail --lines=+3 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "two\nthree\nfour\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCReportsTotalsForMultipleFiles(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/a.txt\n echo two words > /tmp/b.txt\n wc /tmp/a.txt /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/tmp/a.txt", "/tmp/b.txt", "total"} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
		}
	}
}

func TestWCCountsWordsFromStdin(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one two three | wc -w\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCCountsBinaryBytes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/binary.bin", []byte{0x41, 0x00, 0x42, 0x00, 0x43})

	result := mustExecSession(t, session, "wc -c /tmp/binary.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "5 /tmp/binary.bin\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCCountsLinesFromExplicitStdinWithoutPadding(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\nb\\n' | wc -l -\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2 -\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCSupportsMaxLineLengthAndTotalModes(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\n123\\n' > /tmp/a.txt\nprintf 'xx\\n' > /tmp/b.txt\nwc -L /tmp/a.txt\nwc --total=only -c /tmp/a.txt /tmp/b.txt\nwc --total=always -c /tmp/b.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "3 /tmp/a.txt\n9\n3 /tmp/b.txt\n3 total\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCSupportsFiles0From(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/a.txt", []byte("a\n"))
	writeSessionFile(t, session, "/tmp/b.txt", []byte("bb\n"))
	writeSessionFile(t, session, "/tmp/names", []byte("/tmp/a.txt\x00/tmp/b.txt\x00"))

	result := mustExecSession(t, session, "wc --files0-from=/tmp/names\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/tmp/a.txt", "/tmp/b.txt", "total"} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
		}
	}
}

func TestWCRejectsFiles0FromConflictsAndBadNames(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	conflict, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "wc --files0-from=- file\n",
	})
	if err != nil {
		t.Fatalf("Run(conflict) error = %v", err)
	}
	if conflict.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", conflict.ExitCode, conflict.Stderr)
	}
	if !strings.Contains(conflict.Stderr, "file operands cannot be combined with --files0-from") {
		t.Fatalf("Stderr = %q, want files0-from conflict", conflict.Stderr)
	}

	invalid, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '\\0/tmp/a.txt\\0' | wc --files0-from=-\n",
	})
	if err != nil {
		t.Fatalf("Run(invalid) error = %v", err)
	}
	if invalid.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", invalid.ExitCode, invalid.Stderr)
	}
	if !strings.Contains(invalid.Stderr, "invalid zero-length file name") {
		t.Fatalf("Stderr = %q, want zero-length filename error", invalid.Stderr)
	}
}

func TestWCFiles0FromMatchesGNUEmptyInvalidAndQuotedNames(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf '' > /tmp/empty\nwc --files0-from=/tmp/empty > /tmp/empty.out\nprintf '\\0\\0' | wc --files0-from=- > /tmp/nul.out 2> /tmp/nul.err || true\ncd /tmp\ntouch '1\n2'\nprintf '%s\\0' '1\n2' | wc --files0-from=- > /tmp/nl.out\ncat /tmp/empty.out\nprintf '%s\\n' '---'\ncat /tmp/nul.out\nprintf '%s\\n' '---'\ncat /tmp/nul.err\nprintf '%s\\n' '---'\ncat /tmp/nl.out\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "---\n0 0 0 total\n---\nwc: -:1: invalid zero-length file name\nwc: -:2: invalid zero-length file name\n---\n0 0 0 '1'$'\\n''2'\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCMatchesGNUPaddingForMultipleFiles(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "cd /tmp\nprintf '%s\\n' '2' > 2b\nprintf '%s\\n' '2 words' > 2w\nwc --total=never 2b 2w\nwc --total=only 2b 2w\nwc -c 2b 2w\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = " 1  1  2 2b\n 1  2  8 2w\n2 3 10\n 2 2b\n 8 2w\n10 total\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCFiles0FromKeepsZeroCountOutputUnpadded(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "cd /tmp\ntouch g\nprintf 'g\\0g' | wc --files0-from=-\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "0 0 0 g\n0 0 0 g\n0 0 0 total\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCNonbreakingSpaceWordSeparators(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "export LC_ALL=en_US.iso8859-1\nprintf '=\\xA0=' | wc -w\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCUTF8NonbreakingSeparators(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/nbsp.txt", []byte("=\u00A0="))
	writeSessionFile(t, session, "/tmp/wj.txt", []byte("=\u2060="))

	result := mustExecSession(t, session, "export LC_ALL=en_US.UTF-8\nwc -w /tmp/nbsp.txt\nwc -L /tmp/nbsp.txt\nwc -w /tmp/wj.txt\nwc -L /tmp/wj.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "2 /tmp/nbsp.txt\n" +
		"3 /tmp/nbsp.txt\n" +
		"2 /tmp/wj.txt\n" +
		"3 /tmp/wj.txt\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCBytesOnlyUsesRegularFileSizeWithoutReadingWholeFile(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	const size = 8<<20 + 1
	writeSessionFile(t, session, "/tmp/big.bin", bytes.Repeat([]byte{'x'}, size))

	result := mustExecSession(t, session, "wc -c /tmp/big.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "8388609 /tmp/big.bin\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCUsesDisplayWidthAndGNUCountOrder(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/wide.txt", []byte("A界🙂\n"))

	result := mustExecSession(t, session, "wc -L /tmp/wide.txt\nwc -mL /tmp/wide.txt\nwc -cm /tmp/wide.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "5 /tmp/wide.txt\n4 5 /tmp/wide.txt\n4 9 /tmp/wide.txt\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCAcceptsTotalValueAbbreviations(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'x\\n' > /tmp/x\nwc /tmp/x --tot=au\nwc /tmp/x --total=al\nwc /tmp/x /tmp/x --t=o\nwc /tmp/x /tmp/x --total=n\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "1 1 2 /tmp/x\n1 1 2 /tmp/x\n1 1 2 total\n2 2 4\n1 1 2 /tmp/x\n1 1 2 /tmp/x\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWCPrintsZeroCountsForDirectoriesBeforeErrors(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /tmp/d\nwc /tmp/d || true\n")
	wantStdout := wcExpectedField(0) + " " + wcExpectedField(0) + " " + wcExpectedField(0) + " /tmp/d\n"
	if got := result.Stdout; got != wantStdout {
		t.Fatalf("Stdout = %q, want %q", got, wantStdout)
	}
	if got, want := result.Stderr, "wc: /tmp/d: Is a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestWCFiles0FromReportsSourceReadErrorsAndMixedEntries(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, strings.Join([]string{
		"printf 'a\\n' > /tmp/a.txt",
		"mkdir -p '/tmp/dir with spaces'",
		"wc --files0-from='/tmp/dir with spaces' || true",
		"cd /tmp",
		"wc --files0-from=. || true",
		"printf '/tmp/a.txt\\0\\0/tmp/missing file\\0' > /tmp/names",
		"wc --files0-from=/tmp/names || true",
		"",
	}, "\n"))
	if got, want := result.Stdout, "1 1 2 /tmp/a.txt\n1 1 2 total\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	for _, want := range []string{
		"wc: '/tmp/dir with spaces': read error: Is a directory\n",
		"wc: .: read error: Is a directory\n",
		"wc: /tmp/names:2: invalid zero-length file name\n",
		"wc: '/tmp/missing file': No such file or directory\n",
	} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("Stderr = %q, want substring %q", result.Stderr, want)
		}
	}
}

func TestWCFiles0FromStreamingHonorsMaxFileBytes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits: policy.Limits{
				MaxFileBytes: 12,
			},
		}),
	})

	result := mustExecSession(t, session, strings.Join([]string{
		"printf 'a\\n' > /tmp/a.txt",
		"printf 'b\\n' > /tmp/b.txt",
		"printf '/tmp/a.txt\\0/tmp/b.txt\\0' | wc --files0-from=-",
		"",
	}, "\n"))
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1 1 2 /tmp/a.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "wc: -: read error: input exceeds maximum file size of 12 bytes\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestWCFiles0FromStreamsResultsBeforeEOF(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/a.txt", []byte("a\n"))

	stdinReader, stdinWriter := io.Pipe()
	stdoutWrites := make(chan string, 4)
	errCh := make(chan error, 1)
	resultCh := make(chan *ExecutionResult, 1)

	go func() {
		result, err := session.Exec(context.Background(), &ExecutionRequest{
			Script: "wc --files0-from=-\n",
			Stdin:  stdinReader,
			Stdout: wcStreamObserver(stdoutWrites),
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	if _, err := stdinWriter.Write([]byte("/tmp/a.txt\x00")); err != nil {
		t.Fatalf("stdinWriter.Write() error = %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Session.Exec() error = %v", err)
	case got := <-stdoutWrites:
		if got != "1 1 2 /tmp/a.txt\n" {
			t.Fatalf("stdout write = %q, want %q", got, "1 1 2 /tmp/a.txt\n")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed wc output")
	}

	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("stdinWriter.Close() error = %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Session.Exec() error = %v", err)
	case result := <-resultCh:
		if result.ExitCode != 0 {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
		}
		if got, want := result.Stdout, "1 1 2 /tmp/a.txt\n"; got != want {
			t.Fatalf("Stdout = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for wc completion")
	}
}

func TestWCHelpAliasMatchesGNUShortClusterBehavior(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	help := mustExecSession(t, session, "wc -h\n")
	if help.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", help.ExitCode, help.Stderr)
	}
	if !strings.Contains(help.Stdout, "Usage: wc [OPTION]... [FILE]...") {
		t.Fatalf("Stdout = %q, want wc help text", help.Stdout)
	}

	invalid := mustExecSession(t, session, "printf 'a\\n' | wc - -help || true\n")
	if got, want := invalid.Stderr, "wc: invalid option -- 'h'\nTry 'wc --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

type wcStreamObserver chan string

func (w wcStreamObserver) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func TestCatSupportsNumberFlag(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'first\\nsecond\\n' > /tmp/a.txt\nprintf 'third\\n' | cat --number /tmp/a.txt -\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "     1\tfirst\n     2\tsecond\n     3\tthird\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestColumnSupportsTableModeWithShortAndLongFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "short",
			script: "printf 'short long\\nlonger x\\n' | column -t\n",
			want:   "short   long\nlonger  x\n",
		},
		{
			name:   "long",
			script: "printf 'name age\\nalice 30\\nbob 25\\n' | column --table\n",
			want:   "name   age\nalice  30\nbob    25\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})

			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestColumnSupportsSeparatorsOutputDelimitersAndNoMerge(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'a,,c\\nd,e,f\\n' | column -t -s, -n -o ' | '\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a |   | c\nd | e | f\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestColumnFillModeSupportsWidthAndInvalidParseIntBehavior(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "width",
			script: "printf 'a\\nb\\nc\\nd\\ne\\nf\\n' | column -c20\n",
			want:   "a  b  c  d  e  f\n",
		},
		{
			name:   "invalid-width",
			script: "printf 'a\\nb\\n' | column -c nope\n",
			want:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})

			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestColumnSupportsDashMultipleFilesAndWhitespaceOnlyInput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/other.txt", []byte("c d\n"))

	result := mustExecSession(t, session, "printf 'a b\\n' | column -t - /tmp/other.txt\nprintf '   \\n\\t\\n' | column\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a  b\nc  d\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestColumnMissingFileSuppressesPartialOutput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/input.txt", []byte("a b\n"))

	result := mustExecSession(t, session, "column -t /tmp/input.txt /tmp/missing.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
	if got, want := result.Stderr, "column: /tmp/missing.txt: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestColumnRejectsUnknownOptionsAndMissingArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		stderr string
	}{
		{
			name:   "short",
			script: "column -z\n",
			stderr: "column: invalid option -- 'z'\n",
		},
		{
			name:   "long",
			script: "column --bogus\n",
			stderr: "column: unrecognized option '--bogus'\n",
		},
		{
			name:   "missing-arg",
			script: "column -c\n",
			stderr: "column: option requires an argument -- 'c'\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})

			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
			}
			if result.Stdout != "" {
				t.Fatalf("Stdout = %q, want empty", result.Stdout)
			}
			if got := result.Stderr; got != tc.stderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.stderr)
			}
		})
	}
}

func TestColumnHelpWinsOverOtherArgs(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "column --help -z\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "column - columnate lists") {
		t.Fatalf("Stdout = %q, want help header", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "Usage: column [OPTION]... [FILE]...") {
		t.Fatalf("Stdout = %q, want usage text", result.Stdout)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}
