package builtins_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func TestShufSupportsStdinFileAndEchoModes(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/random.bin", mustHexDecode(t, "d1fdb99af5817142f97a5979d49c8c7d"))
	writeSessionFile(t, session, "/tmp/in.txt", []byte("1\n2\n3\n4\n5\n6\n7\n"))

	result := mustExecSession(t, session,
		"cat /tmp/in.txt | shuf --random-source=/tmp/random.bin > /tmp/stdin.out\n"+
			"cat /tmp/in.txt | shuf --random-source=/tmp/random.bin - > /tmp/dash.out\n"+
			"shuf --random-source=/tmp/random.bin /tmp/in.txt > /tmp/file.out\n"+
			"shuf a b c -e | sort > /tmp/postecho.out\n"+
			"shuf -e -n2 \"$(printf 'a\\nb')\" \"$(printf 'c\\nd')\" > /tmp/echo.out\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	const want = "7\n1\n2\n5\n3\n4\n6\n"
	for _, name := range []string{"/tmp/stdin.out", "/tmp/dash.out", "/tmp/file.out"} {
		if got := string(readSessionFile(t, session, name)); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := string(readSessionFile(t, session, "/tmp/postecho.out")); got != "a\nb\nc\n" {
		t.Fatalf("post-positional echo = %q, want %q", got, "a\nb\nc\n")
	}
	if got := len(readSessionFile(t, session, "/tmp/echo.out")); got != 8 {
		t.Fatalf("echo output length = %d, want 8", got)
	}
}

func TestShufSupportsZeroTerminatedRecords(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/random.bin", mustHexDecode(t, "d1fdb99af5817142f97a5979d49c8c7d"))
	writeSessionFile(t, session, "/tmp/input.bin", []byte("one\x00two\x00\x00"))

	result := mustExecSession(t, session, "shuf -z --random-source=/tmp/random.bin /tmp/input.bin > /tmp/out.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := splitShufZeroRecords(readSessionFile(t, session, "/tmp/out.bin"))
	slices.Sort(parts)
	if got, want := parts, []string{"", "one", "two"}; !slices.Equal(got, want) {
		t.Fatalf("zero-terminated records = %#v, want %#v", got, want)
	}
}

func TestShufSupportsHeadCountRepeatAndRanges(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/random.bin", mustHexDecode(t, "d1fdb99af5817142f97a5979d49c8c7d"))
	writeSessionFile(t, session, "/tmp/in.txt", []byte("1\n2\n3\n4\n5\n6\n7\n"))

	result := mustExecSession(t, session,
		"shuf --random-source=/tmp/random.bin -n5 /tmp/in.txt > /tmp/head.out\n"+
			"shuf --random-source=/tmp/random.bin -i1-10 > /tmp/range.out\n"+
			"shuf -i5-5 > /tmp/single.out\n"+
			"shuf -i5-4 > /tmp/empty.out\n"+
			"shuf -r -n5 /tmp/in.txt > /tmp/repeat.out\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if got, want := string(readSessionFile(t, session, "/tmp/head.out")), "7\n1\n2\n5\n3\n"; got != want {
		t.Fatalf("head-count output = %q, want %q", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/range.out")), "10\n2\n8\n7\n3\n9\n6\n5\n1\n4\n"; got != want {
		t.Fatalf("range output = %q, want %q", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/single.out")), "5\n"; got != want {
		t.Fatalf("single range output = %q, want %q", got, want)
	}
	if got := string(readSessionFile(t, session, "/tmp/empty.out")); got != "" {
		t.Fatalf("empty range output = %q, want empty", got)
	}

	lines := strings.Fields(string(readSessionFile(t, session, "/tmp/repeat.out")))
	if len(lines) != 5 {
		t.Fatalf("repeat output line count = %d, want 5", len(lines))
	}
	for _, line := range lines {
		if !slices.Contains([]string{"1", "2", "3", "4", "5", "6", "7"}, line) {
			t.Fatalf("repeat output contained unexpected line %q", line)
		}
	}
}

func TestShufSupportsOutputFilesAndZeroHeadCount(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/random.bin", mustHexDecode(t, "d1fdb99af5817142f97a5979d49c8c7d"))
	writeSessionFile(t, session, "/tmp/in.txt", []byte("1\n2\n3\n4\n5\n6\n7\n"))
	writeSessionFile(t, session, "/tmp/existing.out", []byte("keep\n"))

	result := mustExecSession(t, session,
		"shuf --random-source=/tmp/random.bin -o /tmp/in.txt /tmp/in.txt\n"+
			"shuf -n0 /tmp/missing\n"+
			"printf '1\\n2\\n' | shuf -n0 > /tmp/stdin-zero.out\n"+
			"shuf -n0 -o /tmp/zero.out /tmp/missing\n"+
			"shuf -n0 -o /tmp/existing.out /tmp/in.txt\n"+
			"printf '' | shuf -r -n0 > /tmp/repeat-zero-stdin.out\n"+
			"shuf -e -r -n0 > /tmp/repeat-zero-echo.out\n"+
			"shuf -r -n0 -i5-4 > /tmp/repeat-zero-range.out\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/in.txt")), "7\n1\n2\n5\n3\n4\n6\n"; got != want {
		t.Fatalf("same-file output = %q, want %q", got, want)
	}
	if got := readSessionFile(t, session, "/tmp/zero.out"); len(got) != 0 {
		t.Fatalf("zero-count output file = %q, want empty", string(got))
	}
	if got := readSessionFile(t, session, "/tmp/existing.out"); len(got) != 0 {
		t.Fatalf("existing output file after -n0 = %q, want empty", string(got))
	}
	for _, name := range []string{
		"/tmp/stdin-zero.out",
		"/tmp/repeat-zero-stdin.out",
		"/tmp/repeat-zero-echo.out",
		"/tmp/repeat-zero-range.out",
	} {
		if got := readSessionFile(t, session, name); len(got) != 0 {
			t.Fatalf("%s = %q, want empty", name, string(got))
		}
	}
}

func TestShufReportsGNUStyleErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		script     string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "conflicting modes",
			script:     "shuf -e 0 -i 0-2\n",
			wantExit:   1,
			wantStderr: "shuf: cannot combine -e and -i options",
		},
		{
			name:       "duplicate output",
			script:     "shuf -o a -o b\n",
			wantExit:   1,
			wantStderr: "shuf: multiple output files specified",
		},
		{
			name:       "duplicate random source",
			script:     "shuf --random-source=a --random-source=b\n",
			wantExit:   1,
			wantStderr: "shuf: multiple random sources specified",
		},
		{
			name:       "duplicate input range",
			script:     "shuf -i0-2 -i0-2\n",
			wantExit:   1,
			wantStderr: "shuf: multiple -i options specified",
		},
		{
			name:       "extra operand",
			script:     "shuf file_a file_b\n",
			wantExit:   1,
			wantStderr: "shuf: extra operand 'file_b'",
		},
		{
			name:       "invalid input range",
			script:     "shuf -i5-3\n",
			wantExit:   1,
			wantStderr: "shuf: invalid input range: '5-3'",
		},
		{
			name:       "invalid line count",
			script:     "shuf -n a\n",
			wantExit:   1,
			wantStderr: "shuf: invalid line count: 'a'",
		},
		{
			name:       "empty repeat stdin",
			script:     "printf '' | shuf -r\n",
			wantExit:   1,
			wantStderr: "shuf: no lines to repeat",
		},
		{
			name:       "random source eof",
			script:     "shuf --random-source=/tmp/random.bin -r -i1-99\n",
			wantExit:   1,
			wantStdout: "38\n30\n10\n26\n23\n61\n46\n99\n75\n43\n10\n89\n10\n44\n24\n59\n22\n51\n",
			wantStderr: "shuf: '/tmp/random.bin': end of file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newSession(t, &Config{})
			writeSessionFile(t, session, "/tmp/random.bin", mustHexDecode(t, "fb838f219b3c2dc573a5586c542f59f8"))
			result, err := session.Exec(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Exec() error = %v", err)
			}
			if result.ExitCode != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantExit, result.Stderr)
			}
			if tc.wantStdout != "" && result.Stdout != tc.wantStdout {
				t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.wantStdout)
			}
			if !strings.Contains(result.Stderr, tc.wantStderr) {
				t.Fatalf("Stderr = %q, want to contain %q", result.Stderr, tc.wantStderr)
			}
		})
	}
}

func TestShufSupportsHelpAndVersion(t *testing.T) {
	t.Parallel()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "shuf --help\nshuf --version\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Usage: shuf [OPTION]... [FILE]\n") {
		t.Fatalf("help output missing usage line: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "shuf (gbash) dev\n") {
		t.Fatalf("version output missing version text: %q", result.Stdout)
	}
}

func splitShufZeroRecords(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, string(part))
	}
	return out
}
