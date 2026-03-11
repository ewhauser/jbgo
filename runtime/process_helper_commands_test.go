package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEnvAndPrintEnvScopeNestedEnvironment(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -i ONLY=value printenv ONLY\nprintenv ONLY || echo missing\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "value\nmissing\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsLongIgnoreEnvironment(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env --ignore-environment ONLY=present printenv ONLY\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "present\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTeeAppendsAndWritesMultipleFiles(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'one\\n' | tee /tmp/a >/tmp/out1\nprintf 'two\\n' | tee -a /tmp/a /tmp/b >/tmp/out2\ncat /tmp/a\ncat /tmp/b\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one\ntwo\ntwo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTrueAndFalseCommandsByPath(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "if /bin/true; then echo yes; fi\nif /bin/false; then echo bad; else echo no; fi\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "yes\nno\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWhichFindsRegisteredCommandsOnPath(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "which echo true missing || echo miss\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/usr/bin/echo\n/usr/bin/true\nmiss\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHelpShowsBuiltinSynopsis(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "help -s pwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "pwd: pwd\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDateFormatsFixedUTCInstant(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "date -u -d 2024-05-06T07:08:09 +%F'T'%T%Z\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "2024-05-06T07:08:09UTC\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDateSupportsLongFlagAliases(t *testing.T) {
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "utc",
			script: "date --utc --date 2024-05-06T07:08:09 +%Z\n",
			want:   "UTC\n",
		},
		{
			name:   "date",
			script: "date --date 2024-05-06T07:08:09 +%F\n",
			want:   "2024-05-06\n",
		},
		{
			name:   "iso-8601",
			script: "date --date 2024-05-06T07:08:09 --iso-8601\n",
			want:   "2024-05-06T07:08:09+0000\n",
		},
		{
			name:   "rfc-email",
			script: "date --date 2024-05-06T07:08:09 --rfc-email\n",
			want:   "Mon, 06 May 2024 07:08:09 +0000\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: tc.script,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSleepHonorsShortDuration(t *testing.T) {
	rt := newRuntime(t, &Config{})

	start := time.Now()
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "sleep 0.02\n",
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if elapsed < 15*time.Millisecond {
		t.Fatalf("elapsed = %s, want at least 15ms", elapsed)
	}
	if elapsed > time.Second {
		t.Fatalf("elapsed = %s, want well under 1s", elapsed)
	}
}

func TestTimeoutStopsNestedCommand(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeout 0.02 sleep 1 || echo timed\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "timed\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want timeout message", result.Stderr)
	}
}

func TestTimeoutSupportsLongKillAfterAndSignalOptions(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "timeout --signal TERM --kill-after 0.01 0.02 sleep 1 || echo timed\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "timed\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "execution timed out") {
		t.Fatalf("Stderr = %q, want timeout message", result.Stderr)
	}
}

func TestBashRunsNestedCommandString(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -c 'echo \"$1\"' ignored value\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "value\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestShRunsScriptFromStdin(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'echo from-stdin\\n' | sh\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "from-stdin\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestXArgsSupportsBatchingAndReplacement(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\nb\\n' | xargs -n 1 echo\nprintf 'left\\nright\\n' | xargs -I{} echo item:{}\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\nitem:left\nitem:right\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestXArgsSupportsLongFlags(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\0b\\0' | xargs --null --verbose --max-args 1 echo\nprintf '' | xargs --no-run-if-empty echo skip\n",
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
