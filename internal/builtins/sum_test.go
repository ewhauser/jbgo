package builtins_test

import (
	"strings"
	"testing"
)

func TestSumBSDModesAndStdin(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/input.txt", []byte("abc\n"))

	tests := []struct {
		name     string
		script   string
		stdout   string
		stderr   string
		exitCode int
	}{
		{
			name:     "default-file",
			script:   "sum /tmp/input.txt\n",
			stdout:   "08288     1 /tmp/input.txt\n",
			exitCode: 0,
		},
		{
			name:     "explicit-bsd",
			script:   "sum -r /tmp/input.txt\n",
			stdout:   "08288     1 /tmp/input.txt\n",
			exitCode: 0,
		},
		{
			name:     "stdin-default",
			script:   "printf 'abc\\n' | sum\n",
			stdout:   "08288     1\n",
			exitCode: 0,
		},
		{
			name:     "empty-stdin",
			script:   "printf '' | sum\n",
			stdout:   "00000     0\n",
			exitCode: 0,
		},
		{
			name:     "explicit-dash-among-files",
			script:   "printf 'abc\\n' | sum - /tmp/input.txt\n",
			stdout:   "08288     1 -\n08288     1 /tmp/input.txt\n",
			exitCode: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != tc.exitCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.exitCode, result.Stderr)
			}
			if result.Stdout != tc.stdout {
				t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.stdout)
			}
			if result.Stderr != tc.stderr {
				t.Fatalf("Stderr = %q, want %q", result.Stderr, tc.stderr)
			}
		})
	}
}

func TestSumSysVModes(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/input.txt", []byte("abc\n"))

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "short-flag",
			script: "sum -s /tmp/input.txt\n",
			want:   "304 1 /tmp/input.txt\n",
		},
		{
			name:   "long-flag",
			script: "sum --sysv /tmp/input.txt\n",
			want:   "304 1 /tmp/input.txt\n",
		},
		{
			name:   "grouped-short-prefers-sysv",
			script: "sum -rs /tmp/input.txt\n",
			want:   "304 1 /tmp/input.txt\n",
		},
		{
			name:   "stdin",
			script: "printf 'abc\\n' | sum --sysv\n",
			want:   "304 1\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if result.Stdout != tc.want {
				t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestSumErrorsAndPartialSuccess(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/present.txt", []byte("abc\n"))
	if err := session.FileSystem().MkdirAll(t.Context(), "/tmp/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll(/tmp/dir) error = %v", err)
	}

	tests := []struct {
		name     string
		script   string
		stdout   string
		stderr   string
		exitCode int
	}{
		{
			name:     "missing-file",
			script:   "sum /tmp/missing.txt\n",
			stderr:   "sum: /tmp/missing.txt: No such file or directory\n",
			exitCode: 1,
		},
		{
			name:     "directory",
			script:   "sum /tmp/dir\n",
			stderr:   "sum: /tmp/dir: Is a directory\n",
			exitCode: 1,
		},
		{
			name:     "continue-after-open-error",
			script:   "sum /tmp/missing.txt /tmp/present.txt\n",
			stdout:   "08288     1 /tmp/present.txt\n",
			stderr:   "sum: /tmp/missing.txt: No such file or directory\n",
			exitCode: 1,
		},
		{
			name:     "invalid-option",
			script:   "sum --definitely-invalid\n",
			stderr:   "sum: unrecognized option '--definitely-invalid'\nTry 'sum --help' for more information.\n",
			exitCode: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != tc.exitCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.exitCode, result.Stderr)
			}
			if result.Stdout != tc.stdout {
				t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.stdout)
			}
			if result.Stderr != tc.stderr {
				t.Fatalf("Stderr = %q, want %q", result.Stderr, tc.stderr)
			}
		})
	}
}

func TestSumHelpAndVersion(t *testing.T) {
	session := newSession(t, &Config{})

	help := mustExecSession(t, session, "sum --help\n")
	if help.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", help.ExitCode, help.Stderr)
	}
	if got := help.Stdout; got == "" || !strings.Contains(got, "Usage: sum [OPTION]... [FILE]...") {
		t.Fatalf("help output missing usage: %q", help.Stdout)
	}
	if got := help.Stdout; !strings.Contains(got, "-s, --sysv") || !strings.Contains(got, "use System V sum algorithm, use 512 bytes blocks") {
		t.Fatalf("help output missing sysv option: %q", help.Stdout)
	}

	version := mustExecSession(t, session, "sum --version\n")
	if version.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", version.ExitCode, version.Stderr)
	}
	if got, want := version.Stdout, "sum (gbash)\n"; got != want {
		t.Fatalf("version Stdout = %q, want %q", got, want)
	}
	if version.Stderr != "" {
		t.Fatalf("version Stderr = %q, want empty", version.Stderr)
	}
}
