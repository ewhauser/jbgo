package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func runInterpNode(t *testing.T, src string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("NewRunner error = %v", err)
	}

	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "xtrace-test.sh")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}

	err = runner.Run(context.Background(), file)
	return stdout.String(), stderr.String(), err
}

func TestXTraceUnsetPS4UsesNoFallbackPrefix(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -x
echo 1
unset PS4
echo 2
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "1\n2\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "+ echo 1\n+ unset PS4\necho 2\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTracePS4IsFunctionScoped(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -x
echo one
f() {
  local PS4='- '
  echo func
}
f
echo two
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "one\nfunc\ntwo\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "+ echo one\n+ f\n+ local 'PS4=- '\n- echo func\n+ echo two\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTracePS4ReadsLastStatus(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
PS4='[last=$?] '
set -x
false
echo status=$?
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "status=1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "[last=0] false\n[last=1] echo status=1\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTraceControlBytesUseOctalEscapes(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
s=$'a\x03b\004c\x00d'
set -x
echo "$s"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "a\x03b\x04c\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "+ echo $'a\\003b\\004c'\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTraceControlBytesPreserveUTF8(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
s=$'é\x03'
set -x
echo "$s"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "é\x03\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "+ echo $'é\\003'\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTraceControlBytesPreserveReplacementChar(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
s=$'\xef\xbf\xbd\x03'
set -x
echo "$s"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "\xef\xbf\xbd\x03\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "+ echo $'\xef\xbf\xbd\\003'\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTraceNonPrintableUTF8UsesOctalEscapes(t *testing.T) {
	t.Parallel()

	// U+0085 (NEXT LINE) is a valid 2-byte UTF-8 sequence (C2 85) but
	// non-printable; bash escapes it as octal even in UTF-8 locales.
	stdout, stderr, err := runInterpScript(t, `
s=$'\xc2\x85\x03'
set -x
echo "$s"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "\xc2\x85\x03\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "+ echo $'\\302\\205\\003'\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTracePS4CommandSubstDoesNotTraceRecursively(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
PS4='+$(echo trace) '
set -x
echo one
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "one\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "+trace echo one\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTracePS4ExpandsBeforeEachTraceLine(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
PS4='+$RANDOM '
set -x
readonly x=3
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if got, want := len(lines), 2; got != want {
		t.Fatalf("stderr = %q, want %d trace lines", stderr, want)
	}
	if !strings.HasSuffix(lines[0], " readonly x=3") {
		t.Fatalf("stderr first line = %q, want readonly trace", lines[0])
	}
	if !strings.HasSuffix(lines[1], " x=3") {
		t.Fatalf("stderr second line = %q, want assignment trace", lines[1])
	}
	prefix0 := strings.TrimSuffix(lines[0], " readonly x=3")
	prefix1 := strings.TrimSuffix(lines[1], " x=3")
	if prefix0 == prefix1 {
		t.Fatalf("stderr = %q, want distinct PS4 prefixes", stderr)
	}
}

func TestXTracePS4SideEffectsPersistInCurrentShell(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
unset x
PS4='<${x:=1}> '
set -x
echo one
echo x=$x
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "one\nx=1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "<1> echo one\n<1> echo x=1\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTracePS4RefreshesLoopContextBetweenIterations(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
PS4='<$i> '
set -x
for i in a b; do
  :
done
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "<> for i in a b\n<a> :\n<a> for i in a b\n<b> :\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTracePS4ErrorsDoNotChangeCommandStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		src       string
		wantDiag  string
		wantTrace string
	}{
		{
			name: "parse error",
			src: `
x=1
PS4='+${x'
set -x
echo one
echo status=$?
`,
			wantDiag:  "bad substitution",
			wantTrace: "echo status=0\n",
		},
		{
			name: "runtime error",
			src: `
x=1
PS4='+oops $(( 1 / 0 )) \$'
set -x
echo one
echo status=$?
`,
			wantDiag:  "division by 0",
			wantTrace: "echo status=0\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := runInterpScript(t, tt.src)
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if got, want := stdout, "one\nstatus=0\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if !strings.Contains(stderr, tt.wantDiag) {
				t.Fatalf("stderr = %q, want diagnostic containing %q", stderr, tt.wantDiag)
			}
			if !strings.Contains(stderr, "echo one\n") {
				t.Fatalf("stderr = %q, want traced command", stderr)
			}
			if !strings.Contains(stderr, tt.wantTrace) {
				t.Fatalf("stderr = %q, want traced status command containing %q", stderr, tt.wantTrace)
			}
		})
	}
}

func TestXTraceDeclBuiltinsTraceAssignments(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -x
readonly x=3
declare -a a=(1)
declare a+=(2)
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"+ readonly x=3\n",
		"+ x=3\n",
		"+ a+=('2')\n",
		"+ declare a\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want substring %q", stderr, want)
		}
	}
}

func TestXTraceCoversDoubleBracketAndArithmeticCommand(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -x
lhs=x
if [[ $lhs == x ]]; then
  (( a = 42 ))
fi
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "+ lhs=x\n+ [[ x == x ]]\n+ ((  a = 42  ))\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTraceDoubleBracketTracingDoesNotReExecuteExpansions(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
mark() {
  echo mark >&2
  printf x
}
set -x
[[ $(mark) == x ]]
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "+ [[ x == x ]]\n") {
		t.Fatalf("stderr = %q, want traced [[ command", stderr)
	}
	markLines := 0
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if line == "mark" {
			markLines++
		}
	}
	if got, want := markLines, 1; got != want {
		t.Fatalf("stderr = %q, want %d raw mark line", stderr, want)
	}
}

func TestXTraceDoubleBracketQuotesEmptyOperands(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
empty=
set -x
[[ foo == $empty ]]
[[ $empty == foo ]]
[[ $empty == $empty ]]
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	const wantStderr = "+ [[ foo == '' ]]\n+ [[ '' == foo ]]\n+ [[ '' == '' ]]\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestXTraceArrayAssignmentsDoNotReExecuteExpansions(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -x
declare -a a=($(echo side >&2; echo 1))
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	sideLines := 0
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if line == "side" {
			sideLines++
		}
	}
	if got, want := sideLines, 1; got != want {
		t.Fatalf("stderr = %q, want %d raw side line", stderr, want)
	}
	if !strings.Contains(stderr, "+ a=('1')\n") {
		t.Fatalf("stderr = %q, want traced array assignment", stderr)
	}
}

func TestVerbosePrintsRawInputLines(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -o verbose
x=foo
y=bar
echo $x
echo $(echo $y)

`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "foo\nbar\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "x=foo\ny=bar\necho $x\necho $(echo $y)\n\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestVerbosePrintsOnRunnerRun(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpNode(t, `
set -o verbose
x=foo
echo $x
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "foo\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	const wantStderr = "x=foo\necho $x\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
