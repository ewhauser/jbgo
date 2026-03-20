package interp

import (
	"strings"
	"testing"
)

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
