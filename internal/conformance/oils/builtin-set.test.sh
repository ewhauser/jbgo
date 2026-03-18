## compare_shells: bash
## oils_failures_allowed: 0

#### can continue after unknown option 
#
# TODO: this is the posix special builtin logic?

set -o errexit
set -o STRICT || true # unknown option
echo hello
## stdout: hello
## status: 0

#### set with both options and argv
set -o errexit a b c
echo "$@"
false
echo done
## stdout: a b c
## status: 1

#### nounset with "$@"
set a b c
set -u  # shouldn't touch argv
echo "$@"
## stdout: a b c

#### set -u -- clears argv
set a b c
set -u -- # shouldn't touch argv
echo "$@"
## stdout: 

#### set -u -- x y z
set a b c
set -u -- x y z
echo "$@"
## stdout: x y z

#### set -u with undefined variable exits the interpreter

# non-interactive
$SH -c 'set -u; echo before; echo $x; echo after'
if test $? -ne 0; then
  echo OK
fi

# interactive
$SH -i -c 'set -u; echo before; echo $x; echo after'
if test $? -ne 0; then
  echo OK
fi

## STDOUT:
before
OK
before
OK
## END

#### set -u with undefined var in interactive shell does NOT exit the interpreter

# In bash, it aborts the LINE only.  The next line is executed!

# non-interactive
$SH -c 'set -u; echo before; echo $x; echo after
echo line2
'
if test $? -ne 0; then
  echo OK
fi

# interactive
$SH -i -c 'set -u; echo before; echo $x; echo after
echo line2
'
if test $? -ne 0; then
  echo OK
fi

## STDOUT:
before
OK
before
line2
## END

#### set -u error can break out of nested evals
$SH -c '
set -u
test_function_2() {
  x=$blarg
}
test_function() {
  eval "test_function_2"
}

echo before
eval test_function
echo after
'
if test $? -ne 0; then
  echo OK
fi

## STDOUT:
before
OK
## END

#### reset option with long flag
set -o errexit
set +o errexit
echo "[$unset]"
## stdout: []
## status: 0

#### reset option with short flag
set -u 
set +u
echo "[$unset]"
## stdout: []
## status: 0

#### set -eu (flag parsing)
set -eu 
echo "[$unset]"
echo status=$?
## stdout-json: ""
## status: 1

#### set -o lists options
set -o | grep -o noexec
## STDOUT:
noexec
## END

#### 'set' and 'eval' round trip

# NOTE: not testing arrays and associative arrays!
_space='[ ]'
_whitespace=$'[\t\r\n]'
_sq="'single quotes'"
_backslash_dq="\\ \""
_unicode=$'[\u03bc]'

# Save the variables
varfile=$TMP/vars-$(basename $SH).txt

set | grep '^_' > "$varfile"

# Unset variables
unset _space _whitespace _sq _backslash_dq _unicode
echo [ $_space $_whitespace $_sq $_backslash_dq $_unicode ]

# Restore them

. $varfile
echo "Code saved to $varfile" 1>&2  # for debugging

test "$_space" = '[ ]' && echo OK
test "$_whitespace" = $'[\t\r\n]' && echo OK
test "$_sq" = "'single quotes'" && echo OK
test "$_backslash_dq" = "\\ \"" && echo OK
test "$_unicode" = $'[\u03bc]' && echo OK

## STDOUT:
[ ]
OK
OK
OK
OK
OK
## END

#### set - - and so forth
set a b
echo "$@"

set - a b
echo "$@"

set -- a b
echo "$@"

set - -
echo "$@"

set -- --
echo "$@"

## STDOUT:
a b
a b
a b
-
--
## END

show_options() {
  case $- in
    *v*) echo verbose-on ;;
  esac
  case $- in
    *x*) echo xtrace-on ;;
  esac
}

set -x -v
show_options
echo

set - a b c
echo "$@"
show_options
echo

set x - y z
echo "$@"

## STDOUT:
verbose-on
xtrace-on

a b c

x - y z
## END

#### set - stops option processing like set --

show_options() {
  case $- in
    *v*) echo verbose-on ;;
  esac
  case $- in
    *x*) echo xtrace-on ;;
  esac
}

set -x - -v

show_options
echo argv "$@"

## STDOUT:
argv -v
## END

#### A single + is an ignored flag; not an argument

show_options() {
  case $- in
    *v*) echo verbose-on ;;
  esac
  case $- in
    *x*) echo xtrace-on ;;
  esac
}

set +
echo plus "$@"

set -x + -v x y
show_options
echo plus "$@"

## STDOUT:
plus
verbose-on
xtrace-on
plus x y
## END

#### set - + and + -
set - +
echo "$@"

set + -
echo "$@"

## STDOUT:
+
+
## END

#### set -a exports variables
set -a
FOO=bar
BAZ=qux
printenv.sh FOO BAZ
## STDOUT:
bar
qux
## END

#### set +a stops exporting
set -a
FOO=exported
set +a
BAR=not_exported
printenv.sh FOO BAR
## STDOUT:
exported
None
## END

#### set -o allexport (long form)
set -o allexport
VAR1=value1
set +o allexport
VAR2=value2
printenv.sh VAR1 VAR2
## STDOUT:
value1
None
## END

#### variables set before set -a are not exported
BEFORE=before_value
set -a
AFTER=after_value
printenv.sh BEFORE AFTER
## STDOUT:
None
after_value
## END

#### set -a exports local variables
set -a
f() {
  local ZZZ=zzz
  printenv.sh ZZZ
}
f
## STDOUT:
zzz
## END

#### set -a exports declare variables
set -a
declare ZZZ=zzz
printenv.sh ZZZ
## STDOUT:
zzz
## END
