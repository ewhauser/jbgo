package runtime

import (
	"context"
	"encoding/binary"
	"regexp"
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

func TestUptimeDefaultSincePrettyAndVersion(t *testing.T) {
	rt := newRuntime(t, &Config{})

	defaultResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "uptime\n",
	})
	if err != nil {
		t.Fatalf("Run(default) error = %v", err)
	}
	if defaultResult.ExitCode != 0 {
		t.Fatalf("default ExitCode = %d, want 0; stderr=%q", defaultResult.ExitCode, defaultResult.Stderr)
	}
	defaultPattern := regexp.MustCompile(`^\s\d{2}:\d{2}:\d{2}\s+up\s+\d+:\d{2},\s+1 user,\s+load average: 0\.00, 0\.00, 0\.00\n$`)
	if !defaultPattern.MatchString(defaultResult.Stdout) {
		t.Fatalf("Stdout = %q, want uptime default format", defaultResult.Stdout)
	}

	sinceResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "uptime --since\n",
	})
	if err != nil {
		t.Fatalf("Run(since) error = %v", err)
	}
	if sinceResult.ExitCode != 0 {
		t.Fatalf("since ExitCode = %d, want 0; stderr=%q", sinceResult.ExitCode, sinceResult.Stderr)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\n$`).MatchString(sinceResult.Stdout) {
		t.Fatalf("Stdout = %q, want since timestamp", sinceResult.Stdout)
	}

	prettyResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env GBASH_SESSION_BOOT_AT=2000-01-01T00:00:00Z uptime -p\n",
	})
	if err != nil {
		t.Fatalf("Run(pretty) error = %v", err)
	}
	if prettyResult.ExitCode != 0 {
		t.Fatalf("pretty ExitCode = %d, want 0; stderr=%q", prettyResult.ExitCode, prettyResult.Stderr)
	}
	if got := prettyResult.Stdout; !strings.HasPrefix(got, "up ") || !strings.Contains(got, "minute") {
		t.Fatalf("Stdout = %q, want pretty uptime output", got)
	}

	versionResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "uptime --version\n",
	})
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if versionResult.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", versionResult.ExitCode, versionResult.Stderr)
	}
	if got, want := versionResult.Stdout, "uptime (gbash)\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestUptimeReadsBootTimeAndUsersFromUtmpFile(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/utmpx", uptimeTestUtmpFixture(1716371201, 2))

	result := mustExecSession(t, session, "uptime /tmp/utmpx\nuptime -s /tmp/utmpx\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout = %q, want 2 lines", result.Stdout)
	}
	defaultPattern := regexp.MustCompile(`^\s\d{2}:\d{2}:\d{2}\s+up\s+.+,\s+2 users,\s+load average: 0\.00, 0\.00, 0\.00$`)
	if !defaultPattern.MatchString(lines[0]) {
		t.Fatalf("first line = %q, want parsed utmp output", lines[0])
	}
	if got, want := lines[1], "2024-05-22 03:46:41"; got != want {
		t.Fatalf("since line = %q, want %q", got, want)
	}
}

func TestUptimeReportsFallbackForBadFileOperands(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/no-boot", []byte("hello"))

	missingResult := mustExecSession(t, session, "uptime /tmp/missing\n")
	if missingResult.ExitCode == 0 {
		t.Fatalf("missing ExitCode = 0, want non-zero")
	}
	if !strings.Contains(missingResult.Stderr, "couldn't get boot time: No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-file message", missingResult.Stderr)
	}
	if !strings.Contains(missingResult.Stdout, "up ???? days ??:??") {
		t.Fatalf("Stdout = %q, want fallback uptime output", missingResult.Stdout)
	}

	dirResult := mustExecSession(t, session, "mkdir -p /tmp/dir\nuptime /tmp/dir\n")
	if dirResult.ExitCode == 0 {
		t.Fatalf("dir ExitCode = 0, want non-zero")
	}
	if !strings.Contains(dirResult.Stderr, "couldn't get boot time: Is a directory") {
		t.Fatalf("Stderr = %q, want directory message", dirResult.Stderr)
	}

	noBootResult := mustExecSession(t, session, "uptime /tmp/no-boot\n")
	if noBootResult.ExitCode == 0 {
		t.Fatalf("no-boot ExitCode = 0, want non-zero")
	}
	if !strings.Contains(noBootResult.Stderr, "couldn't get boot time") {
		t.Fatalf("Stderr = %q, want parse-failure message", noBootResult.Stderr)
	}
}

func TestUptimeRejectsInvalidOptionsAndExtraOperands(t *testing.T) {
	rt := newRuntime(t, &Config{})

	invalidResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "uptime --definitely-invalid\n",
	})
	if err != nil {
		t.Fatalf("Run(invalid) error = %v", err)
	}
	if invalidResult.ExitCode == 0 {
		t.Fatalf("invalid ExitCode = 0, want non-zero")
	}
	if !strings.Contains(invalidResult.Stderr, "unrecognized option") {
		t.Fatalf("Stderr = %q, want invalid-option error", invalidResult.Stderr)
	}

	extraResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "uptime a b\n",
	})
	if err != nil {
		t.Fatalf("Run(extra) error = %v", err)
	}
	if extraResult.ExitCode == 0 {
		t.Fatalf("extra ExitCode = 0, want non-zero")
	}
	if !strings.Contains(extraResult.Stderr, "unexpected value 'b'") {
		t.Fatalf("Stderr = %q, want extra-operand error", extraResult.Stderr)
	}
}

func uptimeTestUtmpFixture(bootSeconds int32, users int) []byte {
	record := func(recordType int32, seconds int32) []byte {
		buf := make([]byte, 384)
		binary.NativeEndian.PutUint32(buf[0:4], uint32(recordType))
		binary.NativeEndian.PutUint32(buf[340:344], uint32(seconds))
		return buf
	}

	out := make([]byte, 0, 384*(users+1))
	out = append(out, record(2, bootSeconds)...)
	for range users {
		out = append(out, record(7, 0)...)
	}
	return out
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
