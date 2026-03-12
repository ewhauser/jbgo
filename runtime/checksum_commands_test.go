package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestSHA256SumHashesFilesAndStdin(t *testing.T) {
	session := newSession(t, &Config{})
	alpha := []byte("alpha\n")
	binary := []byte{0x80, 0x90, 0xa0, 0xb0, 0xff}
	writeSessionFile(t, session, "/tmp/a.txt", alpha)
	writeSessionFile(t, session, "/tmp/bin.dat", binary)

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "empty-stdin",
			script: "printf '' | sha256sum\n",
			want:   fmt.Sprintf("%s  -\n", sha256Hex(nil)),
		},
		{
			name:   "explicit-stdin-argument",
			script: "cat /tmp/bin.dat | sha256sum -\n",
			want:   fmt.Sprintf("%s  -\n", sha256Hex(binary)),
		},
		{
			name:   "multiple-files",
			script: "sha256sum /tmp/a.txt /tmp/bin.dat\n",
			want: fmt.Sprintf(
				"%s  /tmp/a.txt\n%s  /tmp/bin.dat\n",
				sha256Hex(alpha),
				sha256Hex(binary),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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

func TestSHA256SumAcceptsIgnoredModeFlags(t *testing.T) {
	data := []byte("flag-data")
	want := fmt.Sprintf("%s  /tmp/input.txt\n", sha256Hex(data))

	tests := []string{"-b", "-t", "--binary", "--text"}
	for _, flag := range tests {
		t.Run(flag, func(t *testing.T) {
			session := newSession(t, &Config{})
			writeSessionFile(t, session, "/tmp/input.txt", data)

			result := mustExecSession(t, session, fmt.Sprintf("sha256sum %s /tmp/input.txt\n", flag))
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != want {
				t.Fatalf("Stdout = %q, want %q", got, want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestSHA256SumCheckModeSupportsShortAndLongFlags(t *testing.T) {
	data := []byte("verify-me")
	sum := strings.ToUpper(sha256Hex(data))
	checksums := fmt.Sprintf("%s */tmp/input.txt\n", sum)

	tests := []string{"-c", "--check"}
	for _, flag := range tests {
		t.Run(flag, func(t *testing.T) {
			session := newSession(t, &Config{})
			writeSessionFile(t, session, "/tmp/input.txt", data)
			writeSessionFile(t, session, "/tmp/checksums.txt", []byte(checksums))

			result := mustExecSession(t, session, fmt.Sprintf("sha256sum %s /tmp/checksums.txt\n", flag))
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got, want := result.Stdout, "/tmp/input.txt: OK\n"; got != want {
				t.Fatalf("Stdout = %q, want %q", got, want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestSHA256SumCheckModeResolvesTargetsAgainstWorkingDirectory(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("cwd-target")
	writeSessionFile(t, session, "/tmp/input.txt", data)
	writeSessionFile(t, session, "/tmp/sub/checksums.txt", fmt.Appendf(nil, "%s  input.txt\n", sha256Hex(data)))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script:  "sha256sum -c /tmp/sub/checksums.txt\n",
		WorkDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "input.txt: OK\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestSHA256SumReportsMissingDigestInputsOnStdoutAndContinues(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("present")
	writeSessionFile(t, session, "/tmp/present.txt", data)

	result := mustExecSession(t, session, "sha256sum /tmp/missing.txt /tmp/present.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	want := fmt.Sprintf(
		"sha256sum: /tmp/missing.txt: No such file or directory\n%s  /tmp/present.txt\n",
		sha256Hex(data),
	)
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestSHA256SumCheckModeReportsFailuresAndUnreadableTargets(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("actual")
	writeSessionFile(t, session, "/tmp/present.txt", data)
	writeSessionFile(t, session, "/tmp/checksums.txt", []byte(strings.Join([]string{
		fmt.Sprintf("%s  /tmp/present.txt", sha256Hex([]byte("wrong"))),
		fmt.Sprintf("%s  /tmp/missing.txt", sha256Hex([]byte("missing"))),
		"not a checksum line",
		"",
	}, "\n")))

	result := mustExecSession(t, session, "sha256sum -c /tmp/checksums.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	want := "/tmp/present.txt: FAILED\n" +
		"/tmp/missing.txt: FAILED open or read\n" +
		"sha256sum: WARNING: 2 computed checksums did NOT match\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestSHA256SumCheckModeIgnoresMalformedLines(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/checksums.txt", []byte("not a checksum line\nalso invalid\n"))

	result := mustExecSession(t, session, "sha256sum -c /tmp/checksums.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestSHA256SumMissingChecksumListFailsOnStderr(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "sha256sum -c /tmp/missing.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
	if got, want := result.Stderr, "sha256sum: /tmp/missing.txt: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestSHA256SumRejectsUnknownOptions(t *testing.T) {
	tests := []struct {
		name   string
		arg    string
		stderr string
	}{
		{
			name:   "short",
			arg:    "-z",
			stderr: "sha256sum: invalid option -- 'z'\n",
		},
		{
			name:   "combined-short",
			arg:    "-bc",
			stderr: "sha256sum: invalid option -- 'bc'\n",
		},
		{
			name:   "long",
			arg:    "--bogus",
			stderr: "sha256sum: unrecognized option '--bogus'\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			session := newSession(t, &Config{})
			result := mustExecSession(t, session, fmt.Sprintf("sha256sum %s\n", tc.arg))
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

func TestSHA256SumHelpShowsUsageAndWinsOverOtherArgs(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "sha256sum --help -z\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "sha256sum - compute SHA256 message digest") {
		t.Fatalf("Stdout = %q, want help header", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "Usage: sha256sum [OPTION]... [FILE]...") {
		t.Fatalf("Stdout = %q, want usage text", result.Stdout)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
