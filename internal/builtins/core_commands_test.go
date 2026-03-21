package builtins_test

import (
	"context"
	"encoding/binary"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

func TestEnvAndPrintEnvScopeNestedEnvironment(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestTeeSupportsGNUFlagsAndLiteralDashFiles(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name       string
		script     string
		wantCode   int
		wantOut    string
		wantStderr string
	}{
		{
			name:     "ignore interrupts and pipe mode",
			script:   "printf 'one\\n' | tee -ip /tmp/a >/tmp/out\ncat /tmp/a\n",
			wantCode: 0,
			wantOut:  "one\n",
		},
		{
			name:     "bare output-error defaults to warn-nopipe",
			script:   "printf 'two\\n' | tee --output-error /tmp/a >/tmp/out\ncat /tmp/a\n",
			wantCode: 0,
			wantOut:  "two\n",
		},
		{
			name:     "literal dash is a file name",
			script:   "cd /tmp\nprintf 'dash\\n' | tee - >/tmp/out\ncat /tmp/-\ncat /tmp/out\n",
			wantCode: 0,
			wantOut:  "dash\ndash\n",
		},
		{
			name:     "help",
			script:   "tee --help\n",
			wantCode: 0,
			wantOut:  "Usage: tee [OPTION]... [FILE]...\nCopy standard input to each FILE, and also to standard output.\n\n  -a, --append              append to the given FILEs, do not overwrite\n  -i, --ignore-interrupts   ignore interrupt signals\n  -p                        diagnose errors writing to non pipes\n      --output-error[=MODE] set behavior on write error; see MODE below\n  -h, --help                display this help and exit\n      --version             output version information and exit\n\nMODE determines behavior with write errors on outputs:\n  warn         diagnose errors writing to any output\n  warn-nopipe  diagnose errors writing to any output not a pipe\n  exit         exit on error writing to any output\n  exit-nopipe  exit on error writing to any output not a pipe\n",
		},
		{
			name:     "version",
			script:   "tee --version\n",
			wantCode: 0,
			wantOut:  "tee (gbash)\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if tc.wantStderr != "" && result.Stderr != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", result.Stderr, tc.wantStderr)
			}
		})
	}
}

func TestTeeOutputErrorModes(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name      string
		script    string
		wantOut   string
		stderrSub string
	}{
		{
			name: "default continues after open error",
			script: "mkdir /tmp/blocked\nprintf 'hello\\n' | tee /tmp/blocked /tmp/out >/tmp/stdout; echo $?\n" +
				"cat /tmp/out\n",
			wantOut:   "1\nhello\n",
			stderrSub: "tee: /tmp/blocked: open /tmp/blocked:",
		},
		{
			name: "exit mode aborts after open error",
			script: "mkdir /tmp/blocked\nprintf 'hello\\n' | tee --output-error=exit /tmp/blocked /tmp/out >/tmp/stdout; echo $?\n" +
				"test ! -e /tmp/out && echo missing\n",
			wantOut:   "1\nmissing\n",
			stderrSub: "tee: /tmp/blocked: open /tmp/blocked:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; !strings.Contains(got, tc.stderrSub) {
				t.Fatalf("Stderr = %q, want substring %q", got, tc.stderrSub)
			}
		})
	}
}

func TestTrueAndFalseCommandsByPath(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestWhichSupportsAllSilentAndHelp(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=/bin:/usr/bin which -a true\nwhich -s missing || echo silent-miss\nwhich --help\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "/bin/true") || !strings.Contains(result.Stdout, "/usr/bin/true") {
		t.Fatalf("Stdout = %q, want all PATH matches", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "silent-miss\n") {
		t.Fatalf("Stdout = %q, want silent miss marker", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "usage: which [-as] NAME...\n") {
		t.Fatalf("Stdout = %q, want help output", result.Stdout)
	}
}

func TestCommandBuiltinV(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `
shopt -s expand_aliases
alias ll='echo alias'
fn() { :; }
command -v ll fn for pwd /bin/true missing
printf 'first=%d\n' "$?"
command -v echo fn ZZZ for
printf 'second=%d\n' "$?"
mkdir -p /tmp/cmdv
echo 'echo hi' > /tmp/cmdv/tool
chmod +x /tmp/cmdv/tool
command -v /tmp/cmdv/tool
printf 'slash=%d\n' "$?"
echo 'echo hi' > /tmp/cmdv/plain
command -v /tmp/cmdv/plain
printf 'plain=%d\n' "$?"
`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "" +
		"ll\n" +
		"fn\n" +
		"for\n" +
		"pwd\n" +
		"/bin/true\n" +
		"first=1\n" +
		"echo\n" +
		"fn\n" +
		"for\n" +
		"second=0\n" +
		"/tmp/cmdv/tool\n" +
		"slash=0\n" +
		"plain=1\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestCommandBuiltinVUpper(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `
shopt -s expand_aliases
alias ll='echo alias'
fn() { :; }
command -V ll
command -V fn
command -V for
command -V pwd
command -V missing
printf 'status=%d\n' "$?"
`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{
		"ll is aliased to `echo alias'\n",
		"fn is a function\n",
		"fn () \n{ \n    :\n}\n",
		"for is a shell keyword\n",
		"pwd is a shell builtin\n",
		"status=1\n",
	} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Stdout = %q, want substring %q", result.Stdout, want)
		}
	}
	if got := result.Stderr; got != "command: missing: not found\n" {
		t.Fatalf("Stderr = %q, want %q", got, "command: missing: not found\n")
	}
}

func TestCommandBuiltinVUpperPreservesCaseTerminators(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `
case_fn() {
  case $1 in
    a) echo a ;;
    b) echo b ;;
  esac
}
command -V case_fn
`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := strings.Count(result.Stdout, ";;\n"); got < 2 {
		t.Fatalf("Stdout = %q, want case terminators preserved", result.Stdout)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestCommandBuiltinVUpperPreservesHeredocBody(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `
heredoc_fn() {
  cat <<EOF
  keep leading space
EOF
}
command -V heredoc_fn
`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if want := "cat <<EOF\n  keep leading space\nEOF\n"; !strings.Contains(result.Stdout, want) {
		t.Fatalf("Stdout = %q, want substring %q", result.Stdout, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestCommandBuiltinPUsesDefaultPath(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: `
mkdir -p /tmp/bin
echo 'echo wrong' > /tmp/bin/tr
chmod +x /tmp/bin/tr
PATH=/tmp/bin:$PATH
echo aaa | tr a b
echo aaa | command -p tr a b
PATH=
command -p ls >/dev/null
printf 'ls=%d\n' "$?"
`,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "wrong\nbbb\nls=0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestHelpShowsBuiltinSynopsis(t *testing.T) {
	t.Parallel()
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
	if got, want := result.Stdout, "pwd: pwd [-L|-P]\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDateFormatsFixedUTCInstant(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
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

func TestWhoamiReportsDeterministicSandboxIdentity(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "whoami\nid -un\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "agent\nagent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWhoamiFallsBackFromUSERToLOGNAME(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env USER= LOGNAME=logger whoami\nenv USER= LOGNAME= whoami\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "logger\nagent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestWhoamiHelpVersionAndErrors(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name       string
		script     string
		wantCode   int
		wantOut    string
		wantStderr string
	}{
		{
			name:     "help",
			script:   "whoami --help\n",
			wantCode: 0,
			wantOut:  "usage: whoami\n",
		},
		{
			name:     "version",
			script:   "whoami --version\n",
			wantCode: 0,
			wantOut:  "whoami (gbash)\n",
		},
		{
			name:       "invalid long option",
			script:     "whoami --definitely-invalid\n",
			wantCode:   1,
			wantStderr: "whoami: unrecognized option '--definitely-invalid'\n",
		},
		{
			name:       "invalid short option",
			script:     "whoami -x\n",
			wantCode:   1,
			wantStderr: "whoami: invalid option -- 'x'\n",
		},
		{
			name:       "extra operand",
			script:     "whoami someone\n",
			wantCode:   1,
			wantStderr: "whoami: extra operand 'someone'\nTry 'whoami --help' for more information.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: tc.script,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; got != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.wantStderr)
			}
		})
	}
}

func TestWhoHelpVersionAndErrors(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name            string
		script          string
		wantCode        int
		wantOut         string
		wantOutContains []string
		wantStderr      string
	}{
		{
			name:     "help",
			script:   "who --help\n",
			wantCode: 0,
			wantOutContains: []string{
				"Print information about users who are currently logged in.",
				"Usage: who [OPTION]... [ FILE | ARG1 ARG2 ]",
				"-a, --all",
				"-T, --mesg",
				"If FILE is not specified, use",
			},
		},
		{
			name:     "version",
			script:   "who --version\n",
			wantCode: 0,
			wantOut:  "who (gbash)\n",
		},
		{
			name:       "invalid long option",
			script:     "who --definitely-invalid\n",
			wantCode:   1,
			wantStderr: "who: unrecognized option '--definitely-invalid'\nTry 'who --help' for more information.\n",
		},
		{
			name:       "invalid short option",
			script:     "who -x\n",
			wantCode:   1,
			wantStderr: "who: invalid option -- 'x'\nTry 'who --help' for more information.\n",
		},
		{
			name:       "extra operand",
			script:     "who a b c\n",
			wantCode:   1,
			wantStderr: "who: extra operand 'c'\nTry 'who --help' for more information.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: tc.script,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if len(tc.wantOutContains) > 0 {
				for _, want := range tc.wantOutContains {
					if !strings.Contains(result.Stdout, want) {
						t.Fatalf("Stdout = %q, want to contain %q", result.Stdout, want)
					}
				}
			} else if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; got != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.wantStderr)
			}
		})
	}
}

func TestWhoSupportsSelectionFlagsAgainstFixture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		script          string
		wantCode        int
		wantOut         string
		wantOutContains []string
		wantStderr      string
		wantPattern     *regexp.Regexp
	}{
		{
			name:     "default short output",
			script:   "who /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"alice",
				"bob",
			},
		},
		{
			name:     "short flag matches default",
			script:   "who -s /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"alice",
				"bob",
			},
		},
		{
			name:     "heading",
			script:   "who -H /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"NAME",
				"LINE",
				"alice",
			},
		},
		{
			name:     "boot",
			script:   "who -b /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"system boot",
				whoFixtureTimeString(1716371201, false),
			},
		},
		{
			name:     "dead",
			script:   "who -d /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"tty2",
				"term=15 exit=2",
			},
		},
		{
			name:     "login",
			script:   "who -l /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"LOGIN",
				"id=l1",
			},
		},
		{
			name:     "process",
			script:   "who -p /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"ttyS0",
				"id=si",
			},
		},
		{
			name:     "runlevel",
			script:   "who -r /tmp/who.utmp\n",
			wantCode: 0,
			wantOut:  "",
			wantOutContains: func() []string {
				if goruntime.GOOS == "linux" {
					return []string{"run-level 3"}
				}
				return nil
			}(),
		},
		{
			name:     "clock change",
			script:   "who -t /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"clock change",
				whoFixtureTimeString(1716371800, false),
			},
		},
		{
			name:     "users",
			script:   "who -u /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"alice",
				"bob",
				"old",
			},
			wantPattern: regexp.MustCompile(`bob.+\.`),
		},
		{
			name:     "count",
			script:   "who -q /tmp/who.utmp\n",
			wantCode: 0,
			wantOut:  "alice bob\n# users=2\n",
		},
		{
			name:       "lookup",
			script:     "who --lookup -u /tmp/who.utmp\n",
			wantCode:   1,
			wantStderr: "who: --lookup is unsupported in this sandbox\n",
		},
		{
			name:     "inferred long option",
			script:   "who --head /tmp/who.utmp\n",
			wantCode: 0,
			wantOutContains: []string{
				"NAME",
				"alice",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := newWhoFixtureSession(t)
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if len(tc.wantOutContains) > 0 {
				for _, want := range tc.wantOutContains {
					if !strings.Contains(result.Stdout, want) {
						t.Fatalf("Stdout = %q, want to contain %q", result.Stdout, want)
					}
				}
			} else if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; got != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.wantStderr)
			}
			if tc.wantPattern != nil && !tc.wantPattern.MatchString(result.Stdout) {
				t.Fatalf("Stdout = %q, want to match %q", result.Stdout, tc.wantPattern.String())
			}
		})
	}

	defaultResult := mustExecSession(t, newWhoFixtureSession(t), "who /tmp/who.utmp\n")
	shortResult := mustExecSession(t, newWhoFixtureSession(t), "who -s /tmp/who.utmp\n")
	if got, want := shortResult.Stdout, defaultResult.Stdout; got != want {
		t.Fatalf("-s stdout = %q, want %q", got, want)
	}
}

func TestWhoMesgAliasesAndMyLineOnly(t *testing.T) {
	t.Parallel()
	base := mustExecSession(t, newWhoFixtureSession(t), "who -T /tmp/who.utmp\n")
	if base.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", base.ExitCode, base.Stderr)
	}
	if !strings.Contains(base.Stdout, "alice    - tty1") {
		t.Fatalf("Stdout = %q, want alice mesg marker", base.Stdout)
	}
	if !strings.Contains(base.Stdout, "bob      + pts/0") {
		t.Fatalf("Stdout = %q, want bob mesg marker", base.Stdout)
	}

	for _, script := range []string{
		"who -w /tmp/who.utmp\n",
		"who --message /tmp/who.utmp\n",
		"who --writable /tmp/who.utmp\n",
	} {
		result := mustExecSession(t, newWhoFixtureSession(t), script)
		if result.ExitCode != 0 {
			t.Fatalf("%q ExitCode = %d, want 0; stderr=%q", script, result.ExitCode, result.Stderr)
		}
		if got, want := result.Stdout, base.Stdout; got != want {
			t.Fatalf("%q stdout = %q, want %q", script, got, want)
		}
	}

	mResult := mustExecSession(t, newWhoFixtureSession(t), "TTY=/dev/pts/0 who -m /tmp/who.utmp </dev/pts/0\n")
	if mResult.ExitCode != 0 {
		t.Fatalf("-m ExitCode = %d, want 0; stderr=%q", mResult.ExitCode, mResult.Stderr)
	}
	if strings.Contains(mResult.Stdout, "alice") || !strings.Contains(mResult.Stdout, "bob") {
		t.Fatalf("-m stdout = %q, want only bob entry", mResult.Stdout)
	}

	argResult := mustExecSession(t, newWhoFixtureSession(t), "TTY=/dev/pts/0 who am i </dev/pts/0\n")
	if argResult.ExitCode != 0 {
		t.Fatalf("am i ExitCode = %d, want 0; stderr=%q", argResult.ExitCode, argResult.Stderr)
	}
	if got, want := argResult.Stdout, mResult.Stdout; got != want {
		t.Fatalf("am i stdout = %q, want %q", got, want)
	}
}

func TestWhoAllMatchesExpandedFlagsAndMissingFilesAreSilent(t *testing.T) {
	t.Parallel()
	allResult := mustExecSession(t, newWhoFixtureSession(t), "who -a /tmp/who.utmp\n")
	if allResult.ExitCode != 0 {
		t.Fatalf("-a ExitCode = %d, want 0; stderr=%q", allResult.ExitCode, allResult.Stderr)
	}

	expandedResult := mustExecSession(t, newWhoFixtureSession(t), "who -bdlprtuT /tmp/who.utmp\n")
	if expandedResult.ExitCode != 0 {
		t.Fatalf("expanded ExitCode = %d, want 0; stderr=%q", expandedResult.ExitCode, expandedResult.Stderr)
	}
	if got, want := allResult.Stdout, expandedResult.Stdout; got != want {
		t.Fatalf("-a stdout = %q, want %q", got, want)
	}

	missingResult := mustExecSession(t, newWhoFixtureSession(t), "who /tmp/missing\n")
	if missingResult.ExitCode != 0 {
		t.Fatalf("missing ExitCode = %d, want 0; stderr=%q", missingResult.ExitCode, missingResult.Stderr)
	}
	if missingResult.Stdout != "" || missingResult.Stderr != "" {
		t.Fatalf("missing result = stdout %q stderr %q, want both empty", missingResult.Stdout, missingResult.Stderr)
	}
}

func TestArchReportsMachineArchitecture(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "arch\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, expectedArchMachine(t)+"\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestArchHelpVersionAndErrors(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name            string
		script          string
		wantCode        int
		wantOut         string
		wantOutContains []string
		wantStderr      string
	}{
		{
			name:     "short help",
			script:   "arch -h\n",
			wantCode: 0,
			wantOutContains: []string{
				"Display machine architecture",
				"Usage: arch",
				"-V, --version",
				"-h, --help",
				"Determine architecture name for current machine.",
			},
		},
		{
			name:     "long help",
			script:   "arch --help\n",
			wantCode: 0,
			wantOutContains: []string{
				"Display machine architecture",
				"Usage: arch",
				"-V, --version",
				"-h, --help",
				"Determine architecture name for current machine.",
			},
		},
		{
			name:     "short version",
			script:   "arch -V\n",
			wantCode: 0,
			wantOut:  "arch (gbash)\n",
		},
		{
			name:     "long version",
			script:   "arch --version\n",
			wantCode: 0,
			wantOut:  "arch (gbash)\n",
		},
		{
			name:     "inferred long version",
			script:   "arch --ver\n",
			wantCode: 0,
			wantOut:  "arch (gbash)\n",
		},
		{
			name:       "invalid long option",
			script:     "arch --definitely-invalid\n",
			wantCode:   1,
			wantStderr: "arch: unrecognized option '--definitely-invalid'\nTry 'arch --help' for more information.\n",
		},
		{
			name:       "invalid short option",
			script:     "arch -x\n",
			wantCode:   1,
			wantStderr: "arch: invalid option -- 'x'\nTry 'arch --help' for more information.\n",
		},
		{
			name:       "extra operand",
			script:     "arch extra\n",
			wantCode:   1,
			wantStderr: "arch: extra operand 'extra'\nTry 'arch --help' for more information.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: tc.script,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if got := result.Stdout; len(tc.wantOutContains) > 0 {
				for _, want := range tc.wantOutContains {
					if !strings.Contains(got, want) {
						t.Fatalf("Stdout = %q, want to contain %q", got, want)
					}
				}
			} else if got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; got != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.wantStderr)
			}
		})
	}
}

func TestUnameReportsSelectedSystemInformation(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})
	expected := expectedUname(t)

	tests := []struct {
		name    string
		script  string
		wantOut string
	}{
		{
			name:    "default kernel name",
			script:  "uname\n",
			wantOut: expected.kernelName + "\n",
		},
		{
			name:    "all",
			script:  "uname -a\n",
			wantOut: expected.all() + "\n",
		},
		{
			name:    "grouped selectors",
			script:  "uname -snrvmo\n",
			wantOut: expected.all() + "\n",
		},
		{
			name:    "kernel name short",
			script:  "uname -s\n",
			wantOut: expected.kernelName + "\n",
		},
		{
			name:    "kernel name alias",
			script:  "uname --sysname\n",
			wantOut: expected.kernelName + "\n",
		},
		{
			name:    "nodename",
			script:  "uname -n\n",
			wantOut: expected.nodename + "\n",
		},
		{
			name:    "kernel release short",
			script:  "uname -r\n",
			wantOut: expected.kernelRelease + "\n",
		},
		{
			name:    "kernel release alias",
			script:  "uname --release\n",
			wantOut: expected.kernelRelease + "\n",
		},
		{
			name:    "kernel version",
			script:  "uname -v\n",
			wantOut: expected.kernelVersion + "\n",
		},
		{
			name:    "machine",
			script:  "uname -m\n",
			wantOut: expected.machine + "\n",
		},
		{
			name:    "operating system short",
			script:  "uname -o\n",
			wantOut: expected.operatingSystem + "\n",
		},
		{
			name:    "operating system long",
			script:  "uname --operating-system\n",
			wantOut: expected.operatingSystem + "\n",
		},
		{
			name:    "operating system inferred long",
			script:  "uname --operating-s\n",
			wantOut: expected.operatingSystem + "\n",
		},
		{
			name:    "processor compatibility",
			script:  "uname -p\n",
			wantOut: "unknown\n",
		},
		{
			name:    "hardware platform compatibility",
			script:  "uname -i\n",
			wantOut: "unknown\n",
		},
		{
			name:    "all with compatibility flags",
			script:  "uname -a -p -i\n",
			wantOut: expected.allWithCompatibility() + "\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestUnameHelpVersionAndErrors(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name               string
		script             string
		wantCode           int
		wantOut            string
		wantOutContains    []string
		wantOutNotContains []string
		wantStderr         string
	}{
		{
			name:     "short help",
			script:   "uname -h\n",
			wantCode: 0,
			wantOutContains: []string{
				"Print certain system information.",
				"Usage: uname [OPTION]...",
				"-a, --all",
				"-s, --kernel-name",
				"-o, --operating-system",
				"-V, --version",
				"-h, --help",
			},
			wantOutNotContains: []string{
				"--sysname",
				"--release",
				"--processor",
				"--hardware-platform",
			},
		},
		{
			name:     "long help",
			script:   "uname --help\n",
			wantCode: 0,
			wantOutContains: []string{
				"Print certain system information.",
				"Usage: uname [OPTION]...",
				"-a, --all",
				"-m, --machine",
				"-o, --operating-system",
				"-V, --version",
				"-h, --help",
			},
			wantOutNotContains: []string{
				"--sysname",
				"--release",
				"--processor",
				"--hardware-platform",
			},
		},
		{
			name:     "short version",
			script:   "uname -V\n",
			wantCode: 0,
			wantOut:  "uname (gbash)\n",
		},
		{
			name:     "long version",
			script:   "uname --version\n",
			wantCode: 0,
			wantOut:  "uname (gbash)\n",
		},
		{
			name:     "inferred long version",
			script:   "uname --ver\n",
			wantCode: 0,
			wantOut:  "uname (gbash)\n",
		},
		{
			name:       "invalid long option",
			script:     "uname --definitely-invalid\n",
			wantCode:   1,
			wantStderr: "uname: unrecognized option '--definitely-invalid'\nTry 'uname --help' for more information.\n",
		},
		{
			name:       "invalid short option",
			script:     "uname -x\n",
			wantCode:   1,
			wantStderr: "uname: invalid option -- 'x'\nTry 'uname --help' for more information.\n",
		},
		{
			name:       "extra operand",
			script:     "uname extra\n",
			wantCode:   1,
			wantStderr: "uname: extra operand 'extra'\nTry 'uname --help' for more information.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if got := result.Stdout; len(tc.wantOutContains) > 0 {
				for _, want := range tc.wantOutContains {
					if !strings.Contains(got, want) {
						t.Fatalf("Stdout = %q, want to contain %q", got, want)
					}
				}
				for _, unwanted := range tc.wantOutNotContains {
					if strings.Contains(got, unwanted) {
						t.Fatalf("Stdout = %q, want not to contain %q", got, unwanted)
					}
				}
			} else if got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; got != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.wantStderr)
			}
		})
	}
}

func TestTtyReportsNotATTYAndSupportsQuietAliases(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name       string
		script     string
		wantCode   int
		wantOut    string
		wantStderr string
	}{
		{
			name:     "default",
			script:   "tty\n",
			wantCode: 1,
			wantOut:  "not a tty\n",
		},
		{
			name:     "short silent",
			script:   "tty -s\n",
			wantCode: 1,
		},
		{
			name:     "long silent",
			script:   "tty --silent\n",
			wantCode: 1,
		},
		{
			name:     "quiet alias",
			script:   "tty --quiet\n",
			wantCode: 1,
		},
		{
			name:     "inferred quiet alias",
			script:   "tty --qui\n",
			wantCode: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: tc.script,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; got != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.wantStderr)
			}
		})
	}
}

func TestTtyUsesSandboxTTYEnvironment(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "TTY=/dev/pts/0 tty\nTTY=tty1 tty -s\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/dev/pts/0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestTtyHelpVersionAndErrors(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	const wantHelp = "Print the file name of the terminal connected to standard input.\n\nUsage: tty [OPTION]...\n\nOptions:\n  -s, --silent   print nothing, only return an exit status [aliases: --quiet]\n  -h, --help     Print help\n  -V, --version  Print version\n"

	tests := []struct {
		name       string
		script     string
		wantCode   int
		wantOut    string
		wantStderr string
	}{
		{
			name:     "short help",
			script:   "tty -h\n",
			wantCode: 0,
			wantOut:  wantHelp,
		},
		{
			name:     "long help",
			script:   "tty --help\n",
			wantCode: 0,
			wantOut:  wantHelp,
		},
		{
			name:     "short version",
			script:   "tty -V\n",
			wantCode: 0,
			wantOut:  "tty (uutils coreutils) 0.7.0\n",
		},
		{
			name:     "long version",
			script:   "tty --version\n",
			wantCode: 0,
			wantOut:  "tty (uutils coreutils) 0.7.0\n",
		},
		{
			name:     "inferred version",
			script:   "tty --ver\n",
			wantCode: 0,
			wantOut:  "tty (uutils coreutils) 0.7.0\n",
		},
		{
			name:       "invalid long option",
			script:     "tty --bogus\n",
			wantCode:   2,
			wantStderr: "error: unexpected argument '--bogus' found\n\nUsage: tty [OPTION]...\n\nFor more information, try '--help'.\n",
		},
		{
			name:       "invalid short option",
			script:     "tty -x\n",
			wantCode:   2,
			wantStderr: "error: unexpected argument '-x' found\n\nUsage: tty [OPTION]...\n\nFor more information, try '--help'.\n",
		},
		{
			name:       "extra operand",
			script:     "tty extra\n",
			wantCode:   2,
			wantStderr: "error: unexpected argument 'extra' found\n\nUsage: tty [OPTION]...\n\nFor more information, try '--help'.\n",
		},
		{
			name:       "value on no-value option",
			script:     "tty --silent=value\n",
			wantCode:   2,
			wantStderr: "error: unexpected argument '--silent=value' found\n\nUsage: tty [OPTION]...\n\nFor more information, try '--help'.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: tc.script,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; got != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.wantStderr)
			}
		})
	}
}

func TestUptimeDefaultSincePrettyAndVersion(t *testing.T) {
	t.Parallel()
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

	helpResult, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "uptime --help\nuptime --ver\nuptime --sin\n",
	})
	if err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	if helpResult.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", helpResult.ExitCode, helpResult.Stderr)
	}
	if !strings.Contains(helpResult.Stdout, "Usage: uptime [OPTION]... [FILE]") {
		t.Fatalf("Stdout = %q, want help usage", helpResult.Stdout)
	}
	if !strings.Contains(helpResult.Stdout, "uptime (gbash)\n") {
		t.Fatalf("Stdout = %q, want inferred version output", helpResult.Stdout)
	}
	if !regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\n$`).MatchString(helpResult.Stdout) {
		t.Fatalf("Stdout = %q, want inferred --since output", helpResult.Stdout)
	}
}

func TestUptimeReadsBootTimeAndUsersFromUtmpFile(t *testing.T) {
	t.Parallel()
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
	wantSince := time.Unix(1716371201, 0).UTC().Local().Format("2006-01-02 15:04:05")
	if got, want := lines[1], wantSince; got != want {
		t.Fatalf("since line = %q, want %q", got, want)
	}
}

func TestUptimeReportsFallbackForBadFileOperands(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func newWhoFixtureSession(t *testing.T) *Session {
	t.Helper()

	session := newSession(t, &Config{})
	fixture := whoTestUtmpFixture()
	writeSessionFile(t, session, "/tmp/who.utmp", fixture)
	writeSessionFile(t, session, "/var/run/utmp", fixture)
	writeSessionFile(t, session, "/var/run/utmpx", fixture)
	writeSessionFile(t, session, "/dev/tty1", []byte("tty1\n"))
	writeSessionFile(t, session, "/dev/pts/0", []byte("pts0\n"))

	ctx := context.Background()
	if err := session.FileSystem().Chmod(ctx, "/dev/tty1", 0o600); err != nil {
		t.Fatalf("Chmod(/dev/tty1) error = %v", err)
	}
	if err := session.FileSystem().Chmod(ctx, "/dev/pts/0", 0o620); err != nil {
		t.Fatalf("Chmod(/dev/pts/0) error = %v", err)
	}

	old := time.Unix(1716360000, 0)
	recent := time.Now().Add(-30 * time.Second)
	if err := session.FileSystem().Chtimes(ctx, "/dev/tty1", old, old); err != nil {
		t.Fatalf("Chtimes(/dev/tty1) error = %v", err)
	}
	if err := session.FileSystem().Chtimes(ctx, "/dev/pts/0", recent, recent); err != nil {
		t.Fatalf("Chtimes(/dev/pts/0) error = %v", err)
	}

	return session
}

func whoTestUtmpFixture() []byte {
	type whoFixtureRecord struct {
		recordType int16
		pid        int32
		line       string
		id         string
		user       string
		host       string
		timestamp  int32
		exitTerm   int16
		exitStatus int16
	}

	records := []whoFixtureRecord{
		{recordType: 2, timestamp: 1716371201},
		{recordType: 3, timestamp: 1716371800},
		{recordType: 5, pid: 111, line: "ttyS0", id: "si", timestamp: 1716372000},
		{recordType: 6, pid: 222, line: "tty1", id: "l1", timestamp: 1716372200},
		{recordType: 8, pid: 333, line: "tty2", id: "d2", timestamp: 1716372400, exitTerm: 15, exitStatus: 2},
		{recordType: 1, pid: int32('N')*256 + int32('3'), timestamp: 1716372600},
		{recordType: 7, pid: 444, line: "tty1", id: "u1", user: "alice", host: "remote.example", timestamp: 1716372800},
		{recordType: 7, pid: 555, line: "pts/0", id: "p0", user: "bob", host: "example.invalid:0", timestamp: 1716373000},
	}

	out := make([]byte, 0, 384*len(records))
	for _, record := range records {
		buf := make([]byte, 384)
		binary.NativeEndian.PutUint16(buf[0:2], uint16(record.recordType))
		binary.NativeEndian.PutUint32(buf[4:8], uint32(record.pid))
		copy(buf[8:40], record.line)
		copy(buf[40:44], record.id)
		copy(buf[44:76], record.user)
		copy(buf[76:332], record.host)
		binary.NativeEndian.PutUint16(buf[332:334], uint16(record.exitTerm))
		binary.NativeEndian.PutUint16(buf[334:336], uint16(record.exitStatus))
		binary.NativeEndian.PutUint32(buf[340:344], uint32(record.timestamp))
		out = append(out, buf...)
	}
	return out
}

func whoFixtureTimeString(seconds int64, cLocale bool) string {
	when := time.Unix(seconds, 0).Local()
	if cLocale {
		return when.Format("Jan _2 15:04")
	}
	return when.Format("2006-01-02 15:04")
}

func TestSleepHonorsShortDuration(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -c 'printf \"%s|%s\\n\" \"$0\" \"$1\"' ignored value\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "ignored|value\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBashCommandStringDefaultsArg0ToInvocationName(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -c 'printf \"%s|%s\\n\" \"$0\" \"$1\"'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "bash|\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBashCommandNotFoundFromCommandStringUsesInvocationPrefix(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -c 'missing-cmd'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "bash: missing-cmd: command not found\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBashScriptCommandNotFoundLeavesComplexTargetsUnprefixed(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'echo\\rTEST\\n' > myscript\nbash myscript\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "echo\rTEST: command not found\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestShRunsScriptFromStdin(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat <<'EOF' | sh\nprintf \"%s\\n\" \"$0\"\nEOF\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "sh\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBashHelpUsesSpecParser(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash --help\nsh --help\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "usage: bash [-i] [-aeflnux] [-o option] [--login] [--noprofile] [--norc] [--rcfile file] [-c command_string [name [arg ...]]] [-s] [script [arg ...]]\nusage: sh [-i] [-aeflnux] [-o option] [--login] [--noprofile] [--norc] [--rcfile file] [-c command_string [name [arg ...]]] [-s] [script [arg ...]]\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if strings.Contains(result.Stdout, "--interactive") {
		t.Fatalf("Stdout = %q, did not expect long interactive flag", result.Stdout)
	}
}

func TestBashRcfileRunsForInteractiveCommandString(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat > /tmp/rc.sh <<'EOF'\nexport FROM_RCFILE=ready\nEOF\nbash --rcfile /tmp/rc.sh -i -c 'echo ${FROM_RCFILE:-missing}'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "ready\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	assertInteractiveShellStderr(t, result.Stderr, "bash")
}

func TestBashNorcSuppressesRcfileForInteractiveCommandString(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat > /tmp/rc.sh <<'EOF'\nexport FROM_RCFILE=ready\nEOF\nbash --norc --rcfile /tmp/rc.sh -i -c 'echo ${FROM_RCFILE:-missing}'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "missing\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	assertInteractiveShellStderr(t, result.Stderr, "bash")
}

func TestBashAcceptsLongStartupCompatibilityFlags(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash --noprofile --norc --login -c 'echo ok'\nsh --noprofile --norc --login -c 'echo ok'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "ok\nok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestHostnameBuiltinUsesRuntimeNodename(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Env: map[string]string{
			"GBASH_UNAME_NODENAME": "sandbox-host",
		},
		Script: "hostname\necho $HOSTNAME\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "sandbox-host\nsandbox-host\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestBashRunsScriptFileAndPassesArgs(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat > /tmp/script.sh <<'EOF'\nprintf \"%s|%s|%s\\n\" \"$0\" \"$1\" \"$2\"\nEOF\nbash /tmp/script.sh left right\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/script.sh|left|right\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBashMissingScriptFileReturns127(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash /tmp/missing-script.sh\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "bash: /tmp/missing-script.sh: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBashMissingScriptFileWithErrexitReturns1(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -o errexit /tmp/missing-script.sh\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "bash: /tmp/missing-script.sh: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestShDashSReadsScriptFromStdinAndUsesArgs(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat <<'EOF' | sh -s value\nprintf \"%s|%s\\n\" \"$0\" \"$1\"\nEOF\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "sh|value\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBashGroupedShortFlagsSetShellOptionsForCommandString(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -ceu 'echo \"$MISSING\"'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
	if !strings.Contains(result.Stderr, "unbound variable") {
		t.Fatalf("Stderr = %q, want nounset diagnostic", result.Stderr)
	}
}

func TestBashCommandStringPrefixesFatalArithmeticDiagnostic(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -c 'echo $((a + 42x))'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	const want = "bash: line 1: a + 42x: value too great for base (error token is \"42x\")\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBashCommandStringStopsAfterArithmeticExpansionError(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -c 'x=42x; echo before; echo $((x)); echo after'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "before\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "value too great for base") {
		t.Fatalf("Stderr = %q, want arithmetic diagnostic", result.Stderr)
	}
}

func TestBashCommandStringStopsAfterBadSubstitution(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -c 'echo before; echo ${foo[]}; echo after'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "before\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "bad substitution") {
		t.Fatalf("Stderr = %q, want bad substitution diagnostic", result.Stderr)
	}
}

func TestBashStdinKeepsRunningAfterArithmeticExpansionError(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat <<'EOF' | bash\nx=42x\necho before\necho $((x))\necho after\nEOF\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "before\nafter\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "value too great for base") {
		t.Fatalf("Stderr = %q, want arithmetic diagnostic", result.Stderr)
	}
}

func TestBashDashOpipefailAffectsCommandString(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -e -o pipefail -c 'false | true; echo after'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
}

func TestBashStartupOptionsAffectExecution(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name      string
		script    string
		wantOut   string
		stderrSub string
	}{
		{
			name:    "noexec",
			script:  "bash -n -c 'echo should-not-run'\n",
			wantOut: "",
		},
		{
			name:    "allexport",
			script:  "bash -a -c 'FOO=bar env | grep \"^FOO=bar$\"'\n",
			wantOut: "FOO=bar\n",
		},
		{
			name:    "noglob",
			script:  "bash -f -c 'printf \"%s\\n\" /tmp/*'\n",
			wantOut: "/tmp/*\n",
		},
		{
			name:      "xtrace",
			script:    "bash -x -c 'echo traced'\n",
			wantOut:   "traced\n",
			stderrSub: "+ echo traced",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
			}
			if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if tc.stderrSub != "" && !strings.Contains(result.Stderr, tc.stderrSub) {
				t.Fatalf("Stderr = %q, want substring %q", result.Stderr, tc.stderrSub)
			}
		})
	}
}

func TestBashInteractiveStdinPersistsStateAndStartupOptions(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat <<'EOF' | bash -iu\npwd\ncd /tmp\npwd\nalias hi=\"echo alias-ok\"\nhi\nset +o nounset\necho X${MISSING}Y\nexit 7\nEOF\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	for _, want := range []string{
		"~$ /home/agent\n",
		"/tmp$ /tmp\n",
		"alias-ok\n",
		"XY\n",
	} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Stdout = %q, want substring %q", result.Stdout, want)
		}
	}
	assertInteractiveShellStderr(t, result.Stderr, "bash")
}

func TestBashInteractiveCommandStringUsesInteractiveSemantics(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -ic 'alias hi=\"echo alias-ok\"\nhi'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "alias-ok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	assertInteractiveShellStderr(t, result.Stderr, "bash")
}

func TestBashInteractiveCommandStringPrefixesBuiltinWarnings(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "bash -ic '_f(){ COMPREPLY=(foo); }\ncompgen -F _f'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "foo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	assertInteractiveShellStderr(t, result.Stderr, "bash", "bash: compgen: warning: -F option may not work as you expect\n")
}

func TestBashInteractiveScriptUsesInteractiveSemantics(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'alias hi=\"echo alias-ok\"\\nhi\\n' > /tmp/script.sh\nbash -i /tmp/script.sh\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "alias-ok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	assertInteractiveShellStderr(t, result.Stderr, "bash")
}

func TestShInteractiveCommandStringUsesInteractiveSemantics(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "sh -ic 'alias hi=\"echo alias-ok\"\nhi'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "alias-ok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	assertInteractiveShellStderr(t, result.Stderr, "sh")
}

func assertInteractiveShellStderr(t *testing.T, got, name string, extra ...string) {
	t.Helper()
	wantTail := name + ": no job control in this shell\n" + strings.Join(extra, "")
	if goruntime.GOOS != "linux" {
		if got != wantTail {
			t.Fatalf("Stderr = %q, want %q", got, wantTail)
		}
		return
	}
	pattern := "^" + regexp.QuoteMeta(name) + `: cannot set terminal process group \([0-9]+\): Inappropriate ioctl for device\n` + regexp.QuoteMeta(wantTail) + "$"
	if !regexp.MustCompile(pattern).MatchString(got) {
		t.Fatalf("Stderr = %q, want pattern %q", got, pattern)
	}
}

func TestXArgsSupportsBatchingAndReplacement(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
