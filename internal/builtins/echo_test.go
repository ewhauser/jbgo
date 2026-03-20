package builtins_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestEchoSupportsGNUEscapeDecoding(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo -n -e '\\x1b\\n\\e\\n\\33\\n\\033\\n\\0033\\n'\n" +
			"echo -n -e '\\x\\n'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "\x1b\n\x1b\n\\33\n\x1b\n\x1b\n\\x\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEchoSupportsUnicodeEscapesInCLocale(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "LC_ALL=C LANG=C echo -n -e '\\u0065|\\U00000065|\\u6|abcd\\u006|\\u03bc|\\U000003bc|\\U0010ffff|\\U00110000|\\udc00|\\U0000dc00'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := string([]byte{'e', '|', 'e', '|', 0x06, '|', 'a', 'b', 'c', 'd', 0x06, '|'}) +
		`\u03BC|\u03BC|\U0010FFFF|\U00110000|\uDC00|\uDC00`
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEchoSupportsUnicodeEscapesInUTF8Locale(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "LC_ALL=en_US.UTF-8 LANG=en_US.UTF-8 echo -n -e '\\u0065|\\u03bc|\\U0001F642|\\udc00|\\U0000dc00|\\U00110000'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := append([]byte{'e', '|'}, []byte("μ")...)
	want = append(want,
		'|',
		0xf0, 0x9f, 0x99, 0x82,
		'|',
		0xed, 0xb0, 0x80,
		'|',
		0xed, 0xb0, 0x80,
		'|',
		0xf4, 0x90, 0x80, 0x80,
	)
	if got := []byte(result.Stdout); !bytes.Equal(got, want) {
		t.Fatalf("Stdout bytes = %v, want %v", got, want)
	}
}

func TestEchoSupportsLegacyHighUnicodeEscapesInUTF8Locale(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "LC_ALL=en_US.UTF-8 LANG=en_US.UTF-8 echo -n -e '\\U00200000|\\U04000000|\\U7fffffff|\\U80000000|\\UFFFFFFFF|Z'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := []byte{
		0xf8, 0x88, 0x80, 0x80, 0x80,
		'|',
		0xfc, 0x84, 0x80, 0x80, 0x80, 0x80,
		'|',
		0xfd, 0xbf, 0xbf, 0xbf, 0xbf, 0xbf,
		'|',
		'|',
		'|',
		'Z',
	}
	if got := []byte(result.Stdout); !bytes.Equal(got, want) {
		t.Fatalf("Stdout bytes = %v, want %v", got, want)
	}
}

func TestEchoTreatsDoubleHyphenAsLiteralAndHonorsBackslashC(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo -- 'foo'\n" +
			"echo -n -e -- 'foo\\n'\n" +
			"echo -e 'foo\\n\\cbar'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "-- foo\n-- foo\nfoo\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEchoSupportsPOSIXLYCorrectMode(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "POSIXLY_CORRECT=1 echo -n -E 'foo\\n'\n" +
			"POSIXLY_CORRECT=1 echo -nE 'foo'\n" +
			"POSIXLY_CORRECT=1 echo -E -n 'foo'\n" +
			"POSIXLY_CORRECT=1 echo --version\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "foo\n-nE foo\n-E -n foo\n--version\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEchoRecognizesExactHelpVersionAndOptionPrecedence(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo --version\n" +
			"echo --ver\n" +
			"echo -e -E '\\na'\n" +
			"echo -E -e '\\na'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if !strings.HasPrefix(result.Stdout, "echo (gbash) dev\n--ver\n\\na\n") {
		t.Fatalf("Stdout = %q, want version banner, literal partial long option, and disabled escapes output", result.Stdout)
	}
	if !strings.HasSuffix(result.Stdout, "\na\n") {
		t.Fatalf("Stdout = %q, want final escape-enabled newline chunk", result.Stdout)
	}
}

func TestEchoSupportsGNUOctalWrapping(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo -ne '\\0501\\0777\\08\\1'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := []byte{'A', 0xff, 0x00, '8', '\\', '1'}
	if got := []byte(result.Stdout); !bytes.Equal(got, want) {
		t.Fatalf("Stdout bytes = %v, want %v", got, want)
	}
}

func TestEchoHelpIsAvailableAsSoleLongOption(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo --help\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, needle := range []string{"Usage: echo", `\e`, `\uHHHH`, `\UHHHHHHHH`} {
		if !strings.Contains(result.Stdout, needle) {
			t.Fatalf("Stdout = %q, want help text containing %q", result.Stdout, needle)
		}
	}
}
