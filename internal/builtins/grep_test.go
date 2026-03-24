package builtins_test

import (
	"context"
	"testing"

	"github.com/ewhauser/gbash/policy"
)

func TestGrepSupportsBREEREAndPatternSources(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'x+y\\nxy\\nalpha\\nbeta\\n' > /tmp/input.txt\n" +
			"printf '^alpha$\\n^beta$\\n' > /tmp/patterns.txt\n" +
			"grep 'x\\+y' /tmp/input.txt\n" +
			"grep 'alpha\\|beta' /tmp/input.txt\n" +
			"grep -E 'x+y' /tmp/input.txt\n" +
			"grep -F 'x+y' /tmp/input.txt\n" +
			"grep -e '^alpha$' -e '^beta$' /tmp/input.txt\n" +
			"grep -f /tmp/patterns.txt /tmp/input.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "xy\nalpha\nbeta\nxy\nx+y\nalpha\nbeta\nalpha\nbeta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestGrepSupportsContextFilenameAndQuietFlags(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'line1\\nline2\\nmatch\\nline4\\nline5\\n' > /tmp/context.txt\n" +
			"printf 'match\\n' > /tmp/a.txt\n" +
			"printf 'match\\n' > /tmp/b.txt\n" +
			"grep -A1 match /tmp/context.txt\n" +
			"grep -B1 match /tmp/context.txt\n" +
			"grep -C1 match /tmp/context.txt\n" +
			"grep -n -B1 -A1 match /tmp/context.txt\n" +
			"grep -H match /tmp/a.txt\n" +
			"grep -h match /tmp/a.txt /tmp/b.txt\n" +
			"grep --quiet match /tmp/a.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "match\nline4\nline2\nmatch\nline2\nmatch\nline4\n2-line2\n3:match\n4-line4\n/tmp/a.txt:match\nmatch\nmatch\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestGrepSupportsFilesWithoutMatchMaxCountAndEmptyPatternFile(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'hit\\nhit\\nmiss\\n' > /tmp/hits.txt\n" +
			"printf 'miss\\n' > /tmp/miss.txt\n" +
			"printf '' > /tmp/empty-patterns.txt\n" +
			"grep --files-without-match hit /tmp/hits.txt /tmp/miss.txt\n" +
			"grep -L hit /tmp/hits.txt /tmp/miss.txt\n" +
			"grep --max-count=1 hit /tmp/hits.txt\n" +
			"grep -m1 hit /tmp/hits.txt\n" +
			"grep -c -f /tmp/empty-patterns.txt /tmp/hits.txt\n" +
			"printf 'count_status=%d\\n' \"$?\"\n" +
			"grep -f /tmp/empty-patterns.txt /tmp/hits.txt\n" +
			"printf 'status=%d\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/miss.txt\n/tmp/miss.txt\nhit\nhit\ncount_status=1\nstatus=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestGrepSuppressesMessagesWithNoMessagesFlag(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "grep -s pattern /tmp/missing.txt\nprintf 'status=%d\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestGrepRejectsUnsupportedPcre(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'item22\\n' > /tmp/input.txt\ngrep -P '[0-9]+' /tmp/input.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if got, want := result.Stderr, "grep: support for the -P option is not compiled into this build\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestGrepUsageErrorsReturnExitCodeTwo(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	for _, tc := range []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "missing-e-value",
			script: "grep -e\n",
			want:   "grep: option requires an argument -- 'e'\nTry 'grep --help' for more information.\n",
		},
		{
			name:   "missing-f-value",
			script: "grep -f\n",
			want:   "grep: option requires an argument -- 'f'\nTry 'grep --help' for more information.\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 2 {
				t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
			}
			if got := result.Stderr; got != tc.want {
				t.Fatalf("Stderr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGrepAliasCommandsUseExtendedAndFixedModes(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'abc\\na.c\\n' > /tmp/input.txt\n" +
			"egrep 'a.c' /tmp/input.txt\n" +
			"fgrep 'a.c' /tmp/input.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "abc\na.c\na.c\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestGrepRecursiveSearchAvoidsSymlinkLoops(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/loop/root/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/loop/root/needle.txt", []byte("needle\n"))
	if err := session.FileSystem().Symlink(context.Background(), "..", "/tmp/loop/root/sub/back"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	result := mustExecSession(t, session, "grep -r needle /tmp/loop/root\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/loop/root/needle.txt:needle\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}
