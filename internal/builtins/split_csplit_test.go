package builtins_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestSplitFilterStreamsRoundRobinOutput(t *testing.T) {
	t.Parallel()
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

func TestSplitAcceptsLargeCountsWithoutOverflow(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"touch /tmp/in\n"+
		"split --lines=18446744073709551615 /tmp/in /tmp/lines-\n"+
		"printf 'lines=%s\\n' $?\n"+
		"split --bytes=9223372036854775807 /tmp/in /tmp/bytes-\n"+
		"printf 'bytes=%s\\n' $?\n"+
		"split --line-bytes=18446744073709551616 /tmp/in /tmp/linebytes-\n"+
		"printf 'linebytes=%s\\n' $?\n"+
		"split --number=r/9223372036854775807/18446744073709551615 </dev/null >/dev/null\n"+
		"printf 'number=%s\\n' $?\n"+
		"split -99999999999999999991 /tmp/in /tmp/obsolete-\n"+
		"printf 'obsolete=%s\\n' $?\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "lines=0\nbytes=0\nlinebytes=0\nnumber=0\nobsolete=0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestSplitLineBytesSplitsLongRecords(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte("1\n2222\n3\n4"))

	result := mustExecSession(t, session, "split -C 2 /tmp/in.txt /tmp/out-\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := map[string]string{
		"/tmp/out-aa": "1\n",
		"/tmp/out-ab": "22",
		"/tmp/out-ac": "22",
		"/tmp/out-ad": "\n",
		"/tmp/out-ae": "3\n",
		"/tmp/out-af": "4",
	}
	for name, expected := range want {
		if got := string(readSessionFile(t, session, name)); got != expected {
			t.Fatalf("%s = %q, want %q", name, got, expected)
		}
	}
}

func TestSplitLineBytesPreservesSplitNewlineByte(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'x\\n' | split -C 1 - /tmp/ch-\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := string(readSessionFile(t, session, "/tmp/ch-aa")); got != "x" {
		t.Fatalf("ch-aa = %q, want %q", got, "x")
	}
	if got := string(readSessionFile(t, session, "/tmp/ch-ab")); got != "\n" {
		t.Fatalf("ch-ab = %q, want newline byte", got)
	}
	if sessionFileExists(t, session, "/tmp/ch-ac") {
		t.Fatal("ch-ac unexpectedly exists")
	}
}

func TestSplitProtectsInputAliasesIncludingRedirectedStdin(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	input := defaultHomeDir + "/xaa"
	contents := csplitNumbers(1, 11)
	writeSessionFile(t, session, input, []byte(contents))
	if err := session.FileSystem().Symlink(context.Background(), "xaa", defaultHomeDir+"/in2"); err != nil {
		t.Fatalf("Symlink(in2) error = %v", err)
	}
	if err := session.FileSystem().Link(context.Background(), input, defaultHomeDir+"/in3"); err != nil {
		t.Fatalf("Link(in3) error = %v", err)
	}

	scripts := []string{
		"cd " + defaultHomeDir + "\nsplit -C 6 xaa\n",
		"cd " + defaultHomeDir + "\nsplit -C 6 in2\n",
		"cd " + defaultHomeDir + "\nsplit -C 6 in3\n",
		"cd " + defaultHomeDir + "\nsplit -C 6 - < xaa\n",
	}
	for _, script := range scripts {
		result := mustExecSession(t, session, script)
		if result.ExitCode != 1 {
			t.Fatalf("script %q ExitCode = %d, want 1; stderr=%q", script, result.ExitCode, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "would overwrite input") {
			t.Fatalf("script %q stderr = %q, want overwrite diagnostic", script, result.Stderr)
		}
	}
	if got := string(readSessionFile(t, session, input)); got != contents {
		t.Fatalf("xaa = %q, want %q", got, contents)
	}
}

func TestSplitFixedWidthNumericSuffixesKeepCreatedFilesOnExhaustion(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte("abcdefghijkl"))

	result := mustExecSession(t, session, "split -b 1 --numeric-suffixes=89 /tmp/in.txt /tmp/out-\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stderr, "split: output file suffixes exhausted\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	files := sessionFilesWithPrefix(t, session, "/tmp", "out-")
	if got, want := len(files), 11; got != want {
		t.Fatalf("created files = %d, want %d (%v)", got, want, files)
	}
	if !sessionFileExists(t, session, "/tmp/out-89") || !sessionFileExists(t, session, "/tmp/out-99") {
		t.Fatalf("expected /tmp/out-89 through /tmp/out-99 to exist; got %v", files)
	}
	if sessionFileExists(t, session, "/tmp/out-9000") {
		t.Fatal("/tmp/out-9000 unexpectedly exists")
	}
}

func TestSplitAutoGrowsNumericAndHexSuffixes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/numeric.txt", []byte(strings.Repeat("x", 91)))
	writeSessionFile(t, session, "/tmp/hex.txt", []byte(strings.Repeat("y", 241)))

	numeric := mustExecSession(t, session, "split -b 1 -d /tmp/numeric.txt /tmp/num-\n")
	if numeric.ExitCode != 0 {
		t.Fatalf("numeric ExitCode = %d, want 0; stderr=%q", numeric.ExitCode, numeric.Stderr)
	}
	if got, want := len(sessionFilesWithPrefix(t, session, "/tmp", "num-")), 91; got != want {
		t.Fatalf("numeric file count = %d, want %d", got, want)
	}
	if !sessionFileExists(t, session, "/tmp/num-89") || !sessionFileExists(t, session, "/tmp/num-9000") {
		t.Fatalf("numeric auto-grow files missing")
	}

	hex := mustExecSession(t, session, "split -b 1 -x /tmp/hex.txt /tmp/hex-\n")
	if hex.ExitCode != 0 {
		t.Fatalf("hex ExitCode = %d, want 0; stderr=%q", hex.ExitCode, hex.Stderr)
	}
	if got, want := len(sessionFilesWithPrefix(t, session, "/tmp", "hex-")), 241; got != want {
		t.Fatalf("hex file count = %d, want %d", got, want)
	}
	if !sessionFileExists(t, session, "/tmp/hex-ef") || !sessionFileExists(t, session, "/tmp/hex-f000") {
		t.Fatalf("hex auto-grow files missing")
	}
}

func TestSplitLineChunkPartitioningAndKthExtraction(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte("aaaaa\nb\n"))

	result := mustExecSession(t, session, "split -n l/4 /tmp/in.txt /tmp/chunk-\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := string(readSessionFile(t, session, "/tmp/chunk-aa")); got != "aaaaa\n" {
		t.Fatalf("chunk-aa = %q, want %q", got, "aaaaa\n")
	}
	if got := string(readSessionFile(t, session, "/tmp/chunk-ab")); got != "" {
		t.Fatalf("chunk-ab = %q, want empty chunk", got)
	}
	if got := string(readSessionFile(t, session, "/tmp/chunk-ac")); got != "" {
		t.Fatalf("chunk-ac = %q, want empty chunk", got)
	}
	if got := string(readSessionFile(t, session, "/tmp/chunk-ad")); got != "b\n" {
		t.Fatalf("chunk-ad = %q, want %q", got, "b\n")
	}

	kth := mustExecSession(t, session, "split -n l/4/4 /tmp/in.txt\n")
	if got, want := kth.Stdout, "b\n"; got != want {
		t.Fatalf("kth Stdout = %q, want %q", got, want)
	}
	empty := mustExecSession(t, session, "split -n l/2/4 /tmp/in.txt\n")
	if empty.Stdout != "" {
		t.Fatalf("empty kth Stdout = %q, want empty", empty.Stdout)
	}
}

func TestSplitStopsAfterFirstOutputError(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	if err := session.FileSystem().Symlink(context.Background(), "/dev/full", defaultHomeDir+"/xaa"); err != nil {
		t.Fatalf("Symlink(xaa) error = %v", err)
	}

	result := mustExecSession(t, session, "cd "+defaultHomeDir+"\nprintf '1\\n2\\n' | split -b 1 -\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.HasPrefix(result.Stderr, "split: xaa:") {
		t.Fatalf("Stderr = %q, want xaa write diagnostic", result.Stderr)
	}
	if sessionFileExists(t, session, defaultHomeDir+"/xab") {
		t.Fatal("xab unexpectedly exists after first output error")
	}
}

func TestCsplitSplitsStdinByLineNumber(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func sessionFileExists(tb testing.TB, session *Session, name string) bool {
	tb.Helper()

	_, err := session.FileSystem().Stat(context.Background(), name)
	return err == nil
}

func sessionFilesWithPrefix(tb testing.TB, session *Session, dir, prefix string) []string {
	tb.Helper()

	entries, err := session.FileSystem().ReadDir(context.Background(), dir)
	if err != nil {
		tb.Fatalf("ReadDir(%q) error = %v", dir, err)
	}
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out
}
