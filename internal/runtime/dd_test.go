package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func execSessionScriptWithInput(t testing.TB, session *Session, script string, stdin []byte) *ExecutionResult {
	t.Helper()

	req := &ExecutionRequest{Script: script}
	if stdin != nil {
		req.Stdin = bytes.NewReader(stdin)
	}

	result, err := session.Exec(context.Background(), req)
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	return result
}

func TestDdSkipAndCountBytes(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/in.txt", []byte("0123456789"))

	result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.txt of=/tmp/out.txt skip=2B count=4B status=none\n", nil)
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "2345"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestDdSeekAndAppendSemantics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "seek truncates without notrunc",
			script: "dd of=/tmp/out.txt seek=2 bs=1 count=1 status=none\n",
			want:   "abZ",
		},
		{
			name:   "seek preserves tail with notrunc",
			script: "dd of=/tmp/out.txt seek=2 bs=1 count=1 conv=notrunc status=none\n",
			want:   "abZdef",
		},
		{
			name:   "append still truncates without notrunc",
			script: "dd of=/tmp/out.txt seek=2 bs=1 count=1 oflag=append status=none\n",
			want:   "abZ",
		},
		{
			name:   "append ignores seek with notrunc",
			script: "dd of=/tmp/out.txt seek=2 bs=1 count=1 oflag=append conv=notrunc status=none\n",
			want:   "abcdefZ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := newSession(t, &Config{})
			writeSessionFile(t, session, "/tmp/out.txt", []byte("abcdef"))

			result := execSessionScriptWithInput(t, session, tc.script, []byte("Z"))
			if got, want := result.ExitCode, 0; got != want {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
			}
			if got := string(readSessionFile(t, session, "/tmp/out.txt")); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDdSameFileSemantics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "truncates before reading",
			script: "dd if=/tmp/file.txt of=/tmp/file.txt bs=1 count=1 status=none\n",
			want:   "",
		},
		{
			name:   "seek reads from materialized truncated prefix",
			script: "dd if=/tmp/file.txt of=/tmp/file.txt seek=2 bs=1 count=1 status=none\n",
			want:   "aba",
		},
		{
			name:   "notrunc keeps tail after same-file seek",
			script: "dd if=/tmp/file.txt of=/tmp/file.txt seek=2 bs=1 count=1 conv=notrunc status=none\n",
			want:   "abadef",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := newSession(t, &Config{})
			writeSessionFile(t, session, "/tmp/file.txt", []byte("abcdef"))

			result := execSessionScriptWithInput(t, session, tc.script, nil)
			if got, want := result.ExitCode, 0; got != want {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
			}
			if got := string(readSessionFile(t, session, "/tmp/file.txt")); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDdSparseSemantics(t *testing.T) {
	t.Parallel()

	t.Run("sparse notrunc preserves existing bytes", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.bin", []byte{0, 0, 0, 0})
		writeSessionFile(t, session, "/tmp/out.txt", []byte("abcdef"))

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.bin of=/tmp/out.txt bs=2 count=2 seek=1 conv=sparse,notrunc status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "abcdef"; got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})

	t.Run("sparse still extends truncated output size", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.bin", []byte{0, 0, 0, 0})

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.bin of=/tmp/out.bin bs=2 count=2 seek=1 conv=sparse status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out.bin"), []byte{0, 0, 0, 0, 0, 0}; !bytes.Equal(got, want) {
			t.Fatalf("output = %v, want %v", got, want)
		}
	})
}

func TestDdConversions(t *testing.T) {
	t.Parallel()

	t.Run("block", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		result := execSessionScriptWithInput(t, session, "dd of=/tmp/out.bin conv=block cbs=4 status=none\n", []byte("abc\ndefgh\n"))
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out.bin"), []byte("abc defg"); !bytes.Equal(got, want) {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})

	t.Run("block carries across reads", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("abc\n"))

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.txt of=/tmp/out.bin conv=block cbs=4 ibs=2 status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out.bin"), []byte("abc "); !bytes.Equal(got, want) {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})

	t.Run("unblock", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.bin", []byte("abc defg"))

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.bin of=/tmp/out.txt conv=unblock cbs=4 status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "abc\ndefg\n"; got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})

	t.Run("unblock carries across reads", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.bin", []byte("abc defg"))

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.bin of=/tmp/out.txt conv=unblock cbs=4 ibs=3 status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "abc\ndefg\n"; got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})

	t.Run("swab", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("abcde"))

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.txt of=/tmp/out.txt conv=swab status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "badce"; got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})

	t.Run("swab carries across reads", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("abcde"))

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.txt of=/tmp/out.txt conv=swab ibs=3 obs=3 status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "badce"; got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})

	t.Run("encodings", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("[]~\n"))

		for _, script := range []string{
			"dd if=/tmp/in.txt of=/tmp/eb.bin conv=ebcdic status=none\n",
			"dd if=/tmp/in.txt of=/tmp/ibm.bin conv=ibm status=none\n",
			"dd if=/tmp/eb.bin of=/tmp/out.txt conv=ascii status=none\n",
		} {
			result := execSessionScriptWithInput(t, session, script, nil)
			if got, want := result.ExitCode, 0; got != want {
				t.Fatalf("script %q ExitCode = %d, want %d; stderr=%q", script, got, want, result.Stderr)
			}
		}

		if got, want := readSessionFile(t, session, "/tmp/eb.bin"), []byte{0xad, 0xbd, 0x5f, 0x25}; !bytes.Equal(got, want) {
			t.Fatalf("EBCDIC bytes = %v, want %v", got, want)
		}
		if got, want := readSessionFile(t, session, "/tmp/ibm.bin"), []byte{0xad, 0xbd, 0xa1, 0x25}; !bytes.Equal(got, want) {
			t.Fatalf("IBM bytes = %v, want %v", got, want)
		}
		if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "[]~\n"; got != want {
			t.Fatalf("ASCII round-trip = %q, want %q", got, want)
		}
	})
}

func TestDdDiagnosticsAndStatusModes(t *testing.T) {
	t.Parallel()

	t.Run("zero multiplier warning", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("abcdef"))

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.txt of=/tmp/out.txt count=0x1 status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "zero multiplier") {
			t.Fatalf("Stderr = %q, want zero-multiplier warning", result.Stderr)
		}
		if got := len(readSessionFile(t, session, "/tmp/out.txt")); got != 0 {
			t.Fatalf("output length = %d, want 0", got)
		}
	})

	t.Run("stdout seek fails", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		result := execSessionScriptWithInput(t, session, "dd seek=1 bs=1 status=none\n", []byte("x"))
		if got, want := result.ExitCode, 1; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "cannot seek: Illegal seek") {
			t.Fatalf("Stderr = %q, want illegal-seek diagnostic", result.Stderr)
		}
	})

	t.Run("skip overflow is rejected", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("abcdef"))

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.txt of=/tmp/out.txt skip=36028797018963968 status=none\n", nil)
		if got, want := result.ExitCode, 1; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "skip offset is too large") {
			t.Fatalf("Stderr = %q, want skip-overflow diagnostic", result.Stderr)
		}
	})

	t.Run("seek overflow is rejected", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})

		result := execSessionScriptWithInput(t, session, "dd of=/tmp/out.txt seek=36028797018963968 status=none\n", []byte("x"))
		if got, want := result.ExitCode, 1; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "seek offset is too large") {
			t.Fatalf("Stderr = %q, want seek-overflow diagnostic", result.Stderr)
		}
	})

	t.Run("status nxfers", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		result := execSessionScriptWithInput(t, session, "dd of=/tmp/out.txt bs=2 count=2 status=noxfer\n", []byte("abc"))
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "records in") || !strings.Contains(result.Stderr, "records out") {
			t.Fatalf("Stderr = %q, want record counts", result.Stderr)
		}
		if strings.Contains(result.Stderr, "bytes copied") {
			t.Fatalf("Stderr = %q, want no transfer summary", result.Stderr)
		}
	})

	t.Run("invalid conversion combination", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		result := execSessionScriptWithInput(t, session, "dd conv=ascii,ebcdic status=none\n", nil)
		if got, want := result.ExitCode, 1; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "cannot combine any two of {ascii,ebcdic,ibm}") {
			t.Fatalf("Stderr = %q, want conversion conflict", result.Stderr)
		}
	})

	t.Run("zero block size is invalid", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		result := execSessionScriptWithInput(t, session, "dd bs=0 status=none\n", nil)
		if got, want := result.ExitCode, 1; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "invalid number") {
			t.Fatalf("Stderr = %q, want invalid-number diagnostic", result.Stderr)
		}
	})
}

func TestDdGNUCompatRegressions(t *testing.T) {
	t.Parallel()

	t.Run("redirected stdout seek bytes", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})

		result := execSessionScriptWithInput(t, session, "dd oseek=8B bs=5 status=none > /tmp/out\n", []byte("abcdefghijklm"))
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out"), append(make([]byte, 8), []byte("abcdefghijklm")...); !bytes.Equal(got, want) {
			t.Fatalf("output = %v, want %v", got, want)
		}

		result = execSessionScriptWithInput(t, session, "dd oseek=8B bs=5 count=0 status=none > /tmp/out2\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("count=0 ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out2"), make([]byte, 8); !bytes.Equal(got, want) {
			t.Fatalf("count=0 output = %v, want %v", got, want)
		}
	})

	t.Run("redirected stdout seek is relative to current offset", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})

		result := execSessionScriptWithInput(t, session, "{ printf AB; dd seek=1 bs=1 count=1 status=none; } > /tmp/out\n", []byte("Z"))
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out"), []byte{'A', 'B', 0, 'Z'}; !bytes.Equal(got, want) {
			t.Fatalf("output = %v, want %v", got, want)
		}
	})

	t.Run("redirected stdout honors oflag directory", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})

		result := execSessionScriptWithInput(t, session, "dd if=/dev/null oflag=directory status=none > /tmp/out\n", nil)
		if got, want := result.ExitCode, 1; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "setting flags for 'standard output': Not a directory") {
			t.Fatalf("Stderr = %q, want standard-output directory diagnostic", result.Stderr)
		}
	})

	t.Run("append redirected stdout seek does not materialize holes", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/out", []byte("AB"))

		result := execSessionScriptWithInput(t, session, "dd seek=1 bs=1 count=0 status=none >> /tmp/out\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("count=0 ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out"), []byte("AB"); !bytes.Equal(got, want) {
			t.Fatalf("count=0 output = %v, want %v", got, want)
		}

		result = execSessionScriptWithInput(t, session, "dd seek=1 bs=1 count=1 status=none >> /tmp/out\n", []byte("Z"))
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("count=1 ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out"), []byte("ABZ"); !bytes.Equal(got, want) {
			t.Fatalf("count=1 output = %v, want %v", got, want)
		}
	})

	t.Run("skip past eof on file is silent", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("data\n"))

		result := execSessionScriptWithInput(t, session, "dd status=none if=/tmp/in.txt skip=2 of=/dev/null\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if result.Stderr != "" {
			t.Fatalf("Stderr = %q, want empty", result.Stderr)
		}
	})

	t.Run("skip past eof on fifo warns", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		result, err := session.Exec(ctx, &ExecutionRequest{
			Script: "mkfifo /tmp/in.pipe\n" +
				"printf abc > /tmp/in.pipe &\n" +
				"dd if=/tmp/in.pipe skip=10 bs=1 count=0 status=none of=/dev/null\n" +
				"wait\n",
		})
		if err != nil {
			t.Fatalf("Exec() error = %v", err)
		}
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "cannot skip to specified offset") {
			t.Fatalf("Stderr = %q, want skip warning", result.Stderr)
		}
	})

	t.Run("seek on redirected fifo stdout reports illegal seek", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		result, err := session.Exec(ctx, &ExecutionRequest{
			Script: "mkfifo /tmp/out.pipe\n" +
				"cat /tmp/out.pipe > /tmp/drain.out &\n" +
				"dd seek=1 bs=1 count=0 status=none > /tmp/out.pipe\n" +
				"printf 'status=%s\\n' \"$?\"\n" +
				"wait\n",
		})
		if err != nil {
			t.Fatalf("Exec() error = %v", err)
		}
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := result.Stdout, "status=1\n"; got != want {
			t.Fatalf("Stdout = %q, want %q", got, want)
		}
		if !strings.Contains(result.Stderr, "cannot seek: Illegal seek") {
			t.Fatalf("Stderr = %q, want illegal seek diagnostic", result.Stderr)
		}
		if got := readSessionFile(t, session, "/tmp/drain.out"); len(got) != 0 {
			t.Fatalf("drain output = %q, want empty", got)
		}
	})

	t.Run("latin1 case conversion", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/upper.txt", []byte{0xC9, '\n'})
		writeSessionFile(t, session, "/tmp/lower.txt", []byte{0xE9, '\n'})

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/upper.txt of=/tmp/out-lower.txt conv=lcase status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("lower ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out-lower.txt"), []byte{0xE9, '\n'}; !bytes.Equal(got, want) {
			t.Fatalf("lower output = %v, want %v", got, want)
		}

		result = execSessionScriptWithInput(t, session, "dd if=/tmp/lower.txt of=/tmp/out-upper.txt conv=ucase status=none\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("upper ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := readSessionFile(t, session, "/tmp/out-upper.txt"), []byte{0xC9, '\n'}; !bytes.Equal(got, want) {
			t.Fatalf("upper output = %v, want %v", got, want)
		}
	})

	t.Run("zero multiplier with huge factor becomes zero", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("abcdef"))
		big := strings.Repeat("9", 61)

		result := execSessionScriptWithInput(t, session, "dd if=/tmp/in.txt of=/tmp/out.txt count=00x"+big+" status=noxfer\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got := len(readSessionFile(t, session, "/tmp/out.txt")); got != 0 {
			t.Fatalf("output length = %d, want 0", got)
		}
		if got, want := result.Stderr, "0+0 records in\n0+0 records out\n"; got != want {
			t.Fatalf("Stderr = %q, want %q", got, want)
		}
	})

	t.Run("iflag nocache rejects pipe", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})

		result := execSessionScriptWithInput(t, session, "dd count=0 status=none | dd iflag=nocache count=0 status=none\n", nil)
		if got, want := result.ExitCode, 1; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
	})

	t.Run("iflag nocache rejects direct", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})

		result := execSessionScriptWithInput(t, session, "dd iflag=nocache,direct if=/dev/null status=none\n", nil)
		if got, want := result.ExitCode, 1; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
	})

	t.Run("redirected stdin preserves offset across dd invocations", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("abcde\n"))

		result := execSessionScriptWithInput(t, session, "(dd skip=1 count=1 bs=1 status=none; dd skip=1 bs=1 status=none) < /tmp/in.txt > /tmp/out.txt\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "bde\n"; got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})

	t.Run("count zero still advances redirected stdin offset", func(t *testing.T) {
		t.Parallel()

		session := newSession(t, &Config{})
		writeSessionFile(t, session, "/tmp/in.txt", []byte("LA:3456789abcdef\n"))

		result := execSessionScriptWithInput(t, session, "(dd bs=1 skip=3 count=0 status=none && dd bs=5 status=none) < /tmp/in.txt > /tmp/out.txt\n", nil)
		if got, want := result.ExitCode, 0; got != want {
			t.Fatalf("ExitCode = %d, want %d; stderr=%q", got, want, result.Stderr)
		}
		if got, want := string(readSessionFile(t, session, "/tmp/out.txt")), "3456789abcdef\n"; got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})
}
