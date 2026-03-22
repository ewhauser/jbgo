package interp

import "testing"

func TestCaseClauseTerminators(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
case a in
  a) echo A ;;&
  *) echo star ;;&
  *) echo star2 ;;
esac

for x in aa bb cc dd zz; do
  case $x in
    aa) echo aa ;&
    bb) echo bb ;&
    cc) echo cc ;;
    dd) echo dd ;;
  esac
  echo --
done
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "A\nstar\nstar2\naa\nbb\ncc\n--\nbb\ncc\n--\ncc\n--\ndd\n--\n--\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestCaseClauseNoCaseMatchOption(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
shopt -s nocasematch

case FOO in
  foo) echo match ;;
  *) echo miss ;;
esac
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "match\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestCaseClauseMatchesInvalidUTF8Bytes(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
c=$(printf \\377)

case $c in
  '')   echo a ;;
  "$c") echo b ;;
esac

case "$c" in
  '')   echo a ;;
  "$c") echo b ;;
esac

sum=0
i=1
while [ "$i" -le 255 ]; do
  hex=$(printf '%x' "$i")
  c="$(printf "\\x$hex")"
  case "$c" in
    "$c") sum=$(( sum + 1 )) ;;
    *) echo "[bug i=$i hex=$hex c=$c]" ;;
  esac
  i=$(( i + 1 ))
done
printf 'sum=%d\n' "$sum"
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	const wantStdout = "b\nb\nsum=255\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
