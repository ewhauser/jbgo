## oils_failures_allowed: 3
## compare_shells: bash

#### Long Token - 65535 bytes

{ printf 'echo -n '; head -c 65535 < /dev/zero | tr '\0' x; } > tmp.sh
$SH tmp.sh > out
wc --bytes out

## STDOUT:
65535 out
## END

#### Token that's too long for Oils - 65536 bytes

{ printf 'echo -n '; head -c 65536 < /dev/zero | tr '\0' x; } > tmp.sh
$SH tmp.sh > out
echo status=$?
wc --bytes out

## STDOUT:
status=0
65536 out
## END

#### $% is not a parse error
echo $%
## stdout: $%

#### Bad braced var sub -- not allowed
echo ${%}
## status: 2

#### Bad var sub caught at parse time
if test -f /; then
  echo ${%}
else
  echo ok
fi
## status: 2

#### Incomplete while
echo hi; while
echo status=$?
## status: 2
## stdout-json: ""

#### Incomplete for
echo hi; for
echo status=$?
## status: 2
## stdout-json: ""

#### Incomplete if
echo hi; if
echo status=$?
## status: 2
## stdout-json: ""

#### do unexpected
do echo hi
## status: 2
## stdout-json: ""

#### } is a parse error
}
echo should not get here
## stdout-json: ""
## status: 2

#### { is its own word, needs a space
{ls; }
echo "status=$?"
## stdout-json: ""
## status: 2

#### } on the second line
set -o errexit
{ls;
}
## status: 127

#### Invalid for loop variable name
for i.j in a b c; do
  echo hi
done
echo done
## stdout-json: ""
## status: 2
## BUG bash status: 0
## BUG bash stdout: done

#### bad var name globally isn't parsed like an assignment
FOO-BAR=foo
## status: 127

#### bad var name in export
export FOO-BAR=foo
## status: 1

#### bad var name in local
f() {
  local FOO-BAR=foo
}
f
## status: 1

#### misplaced parentheses are not a subshell
echo a(b)
## status: 2

#### incomplete command sub
$(x
## status: 2

#### incomplete backticks
`x
## status: 2

#### misplaced ;;
echo 1 ;; echo 2
## stdout-json: ""
## status: 2

#### empty clause in [[

# regression test for commit 451ca9e2b437e0326fc8155783d970a6f32729d8
[[ || true ]]

## status: 2

#### interactive parse error (regression)
flags=''
    flags='--rcfile /dev/null'
$SH $flags -i -c 'var=)'

## status: 2

#### array literal inside array is a parse error
a=( inside=() )
echo len=${#a[@]}
## status: 2
## stdout-json: ""
## BUG bash status: 0
## BUG bash stdout: len=0

#### array literal inside loop is a parse error
f() {
  for x in a=(); do
    echo x=$x
  done
  echo done
}
f
## status: 2
## stdout-json: ""

#### array literal in case
f() {
  case a=() in
    foo)
      echo hi
      ;;
  esac
}
f
## status: 2
## stdout-json: ""

#### %foo=() is parse error (regression)

# Lit_VarLike and then (, but NOT at the beginning of a word.

f() {
  %foo=()
}
## status: 2
## stdout-json: ""

#### echo =word is allowed
echo =word
## STDOUT:
=word
## END
