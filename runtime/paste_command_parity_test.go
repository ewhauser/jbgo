package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestPasteSupportsLongFlagsAndVersion(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\ntwo\\n' > /tmp/in.txt\n" +
			"paste --serial --delimiters=':' /tmp/in.txt\n" +
			"paste --version\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one:two\npaste (gbash) dev\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPasteSupportsZeroTerminatedRecords(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/left.bin", []byte("a\x00b"))
	writeSessionFile(t, session, "/tmp/right.bin", []byte("1\x002\x00"))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "paste -z /tmp/left.bin /tmp/right.bin\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\t1\x00b\t2\x00"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPasteParsesEscapedAndEmptyDelimiters(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n2\\n3\\n' > /tmp/in.txt\n" +
			"paste -s -d '\\0,' /tmp/in.txt\n" +
			"paste -s -d '\\n' /tmp/in.txt\n" +
			"paste -s -d '\\q' /tmp/in.txt\n" +
			"paste -s -d '' /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "12,3\n1\n2\n3\n1q2q3\n123\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestPasteSupportsMultibyteDelimiters(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n2\\n' > /tmp/f1.txt\n" +
			"printf 'a\\nb\\n' > /tmp/f2.txt\n" +
			"paste -d '€' /tmp/f1.txt /tmp/f2.txt\n" +
			"paste -s -d '💣' /tmp/f1.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1€a\n2€b\n1💣2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPasteRejectsTrailingBackslashDelimiter(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n2\\n' > /tmp/in.txt\npaste -d \"\\\\\" /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "delimiter list ends with an unescaped backslash") {
		t.Fatalf("Stderr = %q, want backslash delimiter error", result.Stderr)
	}
}

func TestPastePreservesInvalidUTF8DelimiterWidth(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n2\\n' > /tmp/f1.txt\n" +
			"printf 'a\\nb\\n' > /tmp/f2.txt\n" +
			"delim=$(printf '\\355\\272\\255')\n" +
			"paste -d \"$delim\" /tmp/f1.txt /tmp/f2.txt | od -An -tx1 -v\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 31 ed ba ad 61 0a 32 ed ba ad 62 0a\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPasteSupportsGB18030DelimiterWidth(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "LC_ALL=zh_CN.gb18030\n" +
			"export LC_ALL\n" +
			"printf '1\\n2\\n' > /tmp/f1.txt\n" +
			"printf 'a\\nb\\n' > /tmp/f2.txt\n" +
			"delim=$(printf '\\242\\343')\n" +
			"paste -d \"$delim\" /tmp/f1.txt /tmp/f2.txt | od -An -tx1 -v\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 31 a2 e3 61 0a 32 a2 e3 62 0a\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
