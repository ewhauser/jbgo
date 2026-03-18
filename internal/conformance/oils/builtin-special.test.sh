## oils_failures_allowed: 0
## compare_shells: bash

#### true is not special; prefix assignments don't persist, it can be redefined
foo=bar true
echo foo=$foo

true() {
  echo true func
}
foo=bar true
echo foo=$foo

## STDOUT:
foo=
true func
foo=
## END

# POSIX rule about special builtins pointed at:
#
# https://www.reddit.com/r/oilshell/comments/5ykpi3/oildev_is_alive/

#### Prefix assignments persist after special builtins, like : (set -o posix)
set -o posix

foo=bar :
echo foo=$foo

# Not true when you use 'builtin'
z=Z builtin :
echo z=$Z

## STDOUT:
foo=bar
z=
## END

#### Prefix assignments persist after readonly, but NOT exported (set -o posix)

# Bash only implements it behind the posix option
set -o posix
foo=bar readonly spam=eggs
echo foo=$foo
echo spam=$spam

# should NOT be exported
printenv.sh foo
printenv.sh spam

## STDOUT:
foo=bar
spam=eggs
None
None
## END

#### Prefix binding for exec is a special case (versus e.g. readonly)

pre1=pre1 readonly x=x
pre2=pre2 exec sh -c 'echo pre1=$pre1 x=$x pre2=$pre2'

## STDOUT:
pre1= x= pre2=pre2
## END

#### exec without args is a special case of the special case in some shells

FOO=bar exec >& 2
echo FOO=$FOO
#declare -p | grep FOO

## STDERR:
FOO=
## END

#### Which shells allow special builtins to be redefined?
eval() {
  echo 'eval func' "$@"
}
eval 'echo hi'

# we allow redefinition, but the definition is NOT used!
## status: 0
## STDOUT:
hi
## END

# we PREVENT redefinition

# should not allow redefinition

#### Special builtins can't be redefined as shell functions (set -o posix)
set -o posix

eval 'echo hi'

eval() {
  echo 'sh func' "$@"
}

eval 'echo hi'

## status: 0
## STDOUT:
hi
hi
## END

#### Non-special builtins CAN be redefined as functions
test -n "$BASH_VERSION" && set -o posix
true() {
  echo 'true func'
}
true hi
echo status=$?
## STDOUT:
true func
status=0
## END

#### Shift is special and fails whole script

# https://pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html#tag_18_14
#
# 2.8.1 - Consequences of shell errors
#
# Special built-ins should exit a non-interactive shell
# bash and busybox dont't implement this even with set -o posix, so it seems risky

$SH -c '
if test -n "$BASH_VERSION"; then
  set -o posix
fi
set -- a b
shift 3
echo status=$?
'
if test "$?" != 0; then
  echo 'non-zero status'
fi

## STDOUT:
non-zero status
## END

#### set is special and fails whole script, even if using || true
$SH -c '
if test -n "$BASH_VERSION"; then
  set -o posix
fi

echo ok
set -o invalid_ || true
echo should not get here
'
if test "$?" != 0; then
  echo 'non-zero status'
fi

## STDOUT:
ok
non-zero status
## END

#### bash 'type' gets confused - says 'function', but runs builtin

echo TRUE
type -t true  # builtin
true() { echo true func; }
type -t true  # now a function
echo ---

echo EVAL

type -t eval  # builtin
# define function before set -o posix
eval() { echo "shell function: $1"; }
eval 'echo before posix'

if test -n "$BASH_VERSION"; then
  # this makes the eval definition invisible!
  set -o posix
fi

eval 'echo after posix'  # this is the builtin eval
# bash claims it's a function, but it's a builtin
type -t eval

# it finds the function and the special builtin
#type -a eval

## BUG bash STDOUT:
TRUE
builtin
function
---
EVAL
builtin
shell function: echo before posix
after posix
function
## END

## STDOUT:
TRUE
builtin
function
---
EVAL
builtin
before posix
after posix
builtin
## END

#### command, builtin - both can be redefined, not special (regression)

builtin echo b
command echo c

builtin() {
  echo builtin-redef "$@"
}

command() {
  echo command-redef "$@"
}

builtin echo b
command echo c

## STDOUT:
b
c
builtin-redef echo b
command-redef echo c
## END
