package interp

import (
	"strings"
	"testing"
)

func TestSetSpecialParameterIncludesActiveFlags(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScriptConfig(t, &RunnerConfig{
		Dir:         "/tmp",
		Interactive: true,
	}, `
set -eu
case $- in
  *e*) echo errexit ;;
esac
case $- in
  *u*) echo nounset ;;
esac
case $- in
  *i*) echo interactive ;;
esac
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "errexit\nnounset\ninteractive\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestSetDashClearsVerboseAndXtraceAndSetsParams(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
set -vx
set - one two
case $- in
  *v*|*x*) echo still-on ;;
  *) echo flags-cleared ;;
esac
printf 'params=%s,%s\n' "$1" "$2"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "flags-cleared\nparams=one,two\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	for _, unwanted := range []string{"still-on", "params=one,two", "flags-cleared"} {
		if strings.Contains(stderr, unwanted) {
			t.Fatalf("stderr = %q, want %q to stay untraced after set -", stderr, unwanted)
		}
	}
}

func TestBareSetPrintsParsableAssignments(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
foo=$'one\ntwo'
bar='three four'
saved=$(set)
foo_decl=
bar_decl=
while IFS= read -r line; do
  case $line in
    foo=*) foo_decl=$line ;;
    bar=*) bar_decl=$line ;;
  esac
done <<EOF
$saved
EOF
unset foo bar
eval "$foo_decl"
eval "$bar_decl"
printf '<%s>|<%s>\n' "$foo" "$bar"
`)
	if err != nil {
		t.Fatalf("Run error = %v, stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "<one\ntwo>|<three four>\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestBareSetSkipsUnsetDeclarations(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
declare foo
declare -a arr
export ex
saved=$(set)
foo_seen=0
arr_seen=0
ex_seen=0
while IFS= read -r line; do
  case $line in
    foo|foo=*) foo_seen=1 ;;
    'declare -a arr'*) arr_seen=1 ;;
    'declare -x ex'*) ex_seen=1 ;;
  esac
done <<EOF
$saved
EOF
printf '%s|%s|%s\n' "$foo_seen" "$arr_seen" "$ex_seen"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "0|0|0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestSetRejectsUnknownOptionNameLikeBash(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `set -o STRICT`)
	if err == nil {
		t.Fatal("Run error = nil, want non-zero status")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if got, want := stderr, "set: STRICT: invalid option name\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
