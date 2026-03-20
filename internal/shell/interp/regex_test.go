package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runLegacyBashScript(t *testing.T, src string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:              "/tmp",
		Stdout:           &stdout,
		Stderr:           &stderr,
		LegacyBashCompat: true,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	err = runner.runShellReader(context.Background(), strings.NewReader(src), "legacy-bash-test.sh", nil)
	return stdout.String(), stderr.String(), err
}

func TestConditionalRegexRuntimeErrors(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
[[ aa =~ a+ ]]
printf 'seed=%d\n' "${#BASH_REMATCH[@]}"
bad='|'
[[ a =~ $bad ]]
printf 'pipe=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
bad='^)a\ b($'
[[ 'a b' =~ $bad ]]
printf 'paren=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
brace='{'
[[ { =~ $brace ]]
printf 'brace=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "seed=1\npipe=0 rematch=1\nparen=2 rematch=0\nbrace=2 rematch=0\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}

	if !strings.Contains(stderr, "invalid regular expression `^)a\\ b($': parentheses not balanced") {
		t.Fatalf("stderr = %q, want parentheses diagnostic", stderr)
	}
	if !strings.Contains(stderr, "invalid regular expression `{': Invalid preceding regular expression") {
		t.Fatalf("stderr = %q, want bare brace diagnostic", stderr)
	}
	if got := strings.Count(stderr, "invalid regular expression"); got != 2 {
		t.Fatalf("stderr = %q, want 2 invalid regex diagnostics", stderr)
	}
}

func TestConditionalRegexGroupedWhitespace(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
[[ 'a && b' =~ (a && b) ]]
printf 'status=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "status=0 rematch=2\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestConditionalRegexLegacyBashRuntimeErrors(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runLegacyBashScript(t, `
[[ aa =~ a+ ]]
printf 'seed=%d\n' "${#BASH_REMATCH[@]}"
bad='|'
[[ a =~ $bad ]]
printf 'pipe=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
bad='^)a\ b($'
[[ 'a b' =~ $bad ]]
printf 'paren=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
[[ '|' =~ | ]]
printf 'literal=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
[[ '|' =~ a| ]]
printf 'trailing=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "seed=1\npipe=0 rematch=1\nparen=2 rematch=0\nliteral=0 rematch=1\ntrailing=0 rematch=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestConditionalRegexLiteralAlternationMatches(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
[[ '|' =~ | ]]
printf 'pipe1=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
[[ 'a' =~ a| ]]
printf 'pipe2=%d rematch=%d\n' "$?" "${#BASH_REMATCH[@]}"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "pipe1=0 rematch=1\npipe2=0 rematch=1\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestConditionalRegexLiteralBracesAndClasses(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
[[ { =~ "{" ]] && echo quoted
[[ '{}' =~ \{\} ]] && echo escaped
lisp='^^([][{}\(\)^@])|^(~@)'
[[ '(' =~ $lisp ]] && echo class
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "quoted\nescaped\nclass\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestConditionalRegexLegacyBashCompatInCommandSubstitution(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runLegacyBashScript(t, `
bad='^)a\ b($'
out=$(
	[[ 'a b' =~ $bad ]]
	printf 'status=%d rematch=%d' "$?" "${#BASH_REMATCH[@]}"
)
printf '%s\n' "$out"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "status=2 rematch=0\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
