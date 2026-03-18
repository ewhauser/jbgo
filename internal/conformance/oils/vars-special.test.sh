## oils_failures_allowed: 3
## compare_shells: bash

# NOTE:
# - $! is tested in background.test.sh
# - $- is tested in sh-options
#
# TODO: It would be nice to make a table, like:
#
# $$  $BASHPID  $PPID   $SHLVL   $BASH_SUBSHELL
#  X 
# (Subshell,  Command Sub,  Pipeline,  Spawn $0)
#
# And see whether the variable changed.

#### $PWD is set
# Just test that it has a slash for now.
echo $PWD | grep -q /
echo status=$?
## STDOUT:
status=0
## END

#### $PWD is not only set, but exported
env | grep -q PWD
echo status=$?
## stdout: status=0

#### $PATH is set if unset at startup

# because it a shebang #!/usr/bin/env python2
# This test is still useful for the C++ oils-for-unix.

# Get absolute path before changing PATH
sh=$(which $SH)

old_path=$PATH
unset PATH

$sh -c 'echo $PATH' > path.txt

PATH=$old_path

# cat path.txt

# should contain /usr/bin
if egrep -q '(^|:)/usr/bin($|:)' path.txt; then
  echo yes
fi

# should contain /bin
if egrep -q '(^|:)/bin($|:)' path.txt ; then
  echo yes
fi

## STDOUT:
yes
yes
## END

#### $HOME is NOT set

home=$(echo $HOME)
test "$home" = ""
echo status=$?

env | grep HOME
echo status=$?

# not in interactive shell either
$SH -i -c 'echo $HOME' | grep /
echo status=$?

## STDOUT:
status=0
status=1
status=1
## END

#### Vars set interactively only: $HISTFILE

$SH --norc --rcfile /dev/null -c 'echo histfile=${HISTFILE:+yes}'
$SH --norc --rcfile /dev/null -i -c 'echo histfile=${HISTFILE:+yes}'

## STDOUT:
histfile=
histfile=yes
## END

#### Some vars are set, even without startup file, or env: PATH, PWD

flags='--noprofile --norc --rcfile /devnull'

sh_path=$(which $SH)
sh_prefix=$sh_path

#echo PATH=$PATH

# bash exports PWD, but not PATH PS4

/usr/bin/env -i PYTHONPATH=$PYTHONPATH $sh_prefix $flags -c 'typeset -p PATH PWD PS4' >&2
echo path pwd ps4 $?

/usr/bin/env -i PYTHONPATH=$PYTHONPATH $sh_prefix $flags -c 'typeset -p SHELLOPTS' >&2
echo shellopts $?

/usr/bin/env -i PYTHONPATH=$PYTHONPATH $sh_prefix $flags -c 'typeset -p HOME PS1' >&2
echo home ps1 $?

# IFS is set, but not exported
/usr/bin/env -i PYTHONPATH=$PYTHONPATH $sh_prefix $flags -c 'typeset -p IFS' >&2
echo ifs $?

## STDOUT:
path pwd ps4 0
shellopts 0
home ps1 1
ifs 0
## END

#### UID EUID PPID can't be changed

# bash makes these 3 read-only
{
  UID=xx $SH -c 'echo uid=$UID'

  EUID=xx $SH -c 'echo euid=$EUID'

  PPID=xx $SH -c 'echo ppid=$PPID'

} > out.txt

# bash shows that vars are readonly
# cat out.txt
#echo

grep '=xx' out.txt
echo status=$?

## STDOUT:
status=1
## END

#### HOSTNAME OSTYPE can be changed

#$SH -c 'echo hostname=$HOSTNAME'

HOSTNAME=x $SH -c 'echo hostname=$HOSTNAME'
OSTYPE=x $SH -c 'echo ostype=$OSTYPE'
echo

#PS4=x $SH -c 'echo ps4=$PS4'

# OPTIND is special
#OPTIND=xx $SH -c 'echo optind=$OPTIND'

## STDOUT:
hostname=x
ostype=x

## END

#### $1 .. $9 are scoped, while $0 is not
fun() {
  case $0 in
    *sh)
      echo 'sh'
      ;;
    *sh-*)  # bash-4.4 is OK
      echo 'sh'
      ;;
  esac

  echo $1 $2
}
fun a b

## STDOUT:
sh
a b
## END

#### $?
echo $?  # starts out as 0
sh -c 'exit 33'
echo $?
## STDOUT:
0
33
## END
## status: 0

#### $#
set -- 1 2 3 4
echo $#
## stdout: 4
## status: 0

#### $$ looks like a PID
echo $$ | egrep -q '[0-9]+'  # Test that it has decimal digits
echo status=$?
## STDOUT:
status=0
## END

#### $$ doesn't change with subshell or command sub
# Just test that it has decimal digits
set -o errexit
die() {
  echo 1>&2 "$@"; exit 1
}
parent=$$
test -n "$parent" || die "empty PID in parent"
( child=$$
  test -n "$child" || die "empty PID in subshell"
  test "$parent" = "$child" || die "should be equal: $parent != $child"
  echo 'subshell OK'
)
echo $( child=$$
        test -n "$child" || die "empty PID in command sub"
        test "$parent" = "$child" || die "should be equal: $parent != $child"
        echo 'command sub OK'
      )
exit 3  # make sure we got here
## status: 3
## STDOUT:
subshell OK
command sub OK
## END

#### $BASHPID DOES change with subshell and command sub
set -o errexit
die() {
  echo 1>&2 "$@"; exit 1
}
parent=$BASHPID
test -n "$parent" || die "empty BASHPID in parent"
( child=$BASHPID
  test -n "$child" || die "empty BASHPID in subshell"
  test "$parent" != "$child" || die "should not be equal: $parent = $child"
  echo 'subshell OK'
)
echo $( child=$BASHPID
        test -n "$child" || die "empty BASHPID in command sub"
        test "$parent" != "$child" ||
          die "should not be equal: $parent = $child"
        echo 'command sub OK'
      )
exit 3  # make sure we got here

## status: 3
## STDOUT:
subshell OK
command sub OK
## END

#### Background PID $! looks like a PID
sleep 0.01 &
pid=$!
wait
echo $pid | egrep '[0-9]+' >/dev/null
echo status=$?
## stdout: status=0

#### $PPID
echo $PPID | egrep '[0-9]+'
## status: 0

# NOTE: There is also $BASHPID

#### $PIPESTATUS
echo hi | sh -c 'cat; exit 33' | wc -l >/dev/null
argv.sh "${PIPESTATUS[@]}"
## status: 0
## STDOUT:
['0', '33', '0']
## END

#### $RANDOM
echo $RANDOM | egrep '[0-9]+'
## status: 0

#### $UID and $EUID
# These are both bash-specific.
set -o errexit
echo $UID | egrep -o '[0-9]+' >/dev/null
echo $EUID | egrep -o '[0-9]+' >/dev/null
echo status=$?
## stdout: status=0

#### $OSTYPE is non-empty
test -n "$OSTYPE"
echo status=$?
## STDOUT:
status=0
## END

#### $HOSTNAME
test "$HOSTNAME" = "$(hostname)"
echo status=$?
## STDOUT:
status=0
## END

#### $LINENO is the current line, not line of function call
echo $LINENO  # first line
g() {
  argv.sh $LINENO  # line 3
}
f() {
  argv.sh $LINENO  # line 6
  g
  argv.sh $LINENO  # line 8
}
f
## STDOUT: 
1
['6']
['3']
['8']
## END

#### $LINENO in "bare" redirect arg (bug regression)
filename=$TMP/bare3
rm -f $filename
> $TMP/bare$LINENO
test -f $filename && echo written
echo $LINENO
## STDOUT: 
written
5
## END

#### $LINENO in redirect arg (bug regression)
filename=$TMP/lineno_regression3
rm -f $filename
echo x > $TMP/lineno_regression$LINENO
test -f $filename && echo written
echo $LINENO
## STDOUT: 
written
5
## END

#### $LINENO in [[
echo one
[[ $LINENO -eq 2 ]] && echo OK
## STDOUT:
one
OK
## END

#### $LINENO in ((
echo one
(( x = LINENO ))
echo $x
## STDOUT:
one
2
## END

#### $LINENO in for loop
# hm bash doesn't take into account the word break.  That's OK; we won't either.
echo one
for x in \
  $LINENO zzz; do
  echo $x
done
## STDOUT:
one
2
zzz
## END

#### $LINENO in other for loops
set -- a b c
for x; do
  echo $LINENO $x
done
## STDOUT:
3 a
3 b
3 c
## END

#### $LINENO in for (( loop
# This is a real edge case that I'm not sure we care about.  We would have to
# change the span ID inside the loop to make it really correct.
echo one
for (( i = 0; i < $LINENO; i++ )); do
  echo $i
done
## STDOUT:
one
0
1
## END

#### $LINENO for assignment
a1=$LINENO a2=$LINENO
b1=$LINENO b2=$LINENO
echo $a1 $a2
echo $b1 $b2
## STDOUT:
1 1
2 2
## END

#### $LINENO in case
case $LINENO in
  1) echo 'got line 1' ;;
  *) echo line=$LINENO
esac
## STDOUT:
got line 1
## END

#### $_ with simple command and evaluation

name=world
echo "hi $name"
echo "$_"
## STDOUT:
hi world
hi world
## END

#### $_ and ${_}

_var=value

: 42
echo $_ $_var ${_}var

: 'foo'"bar"
echo $_

## STDOUT:
42 value 42var
foobar
## END

#### $_ with word splitting

setopt shwordsplit

x='with spaces'
: $x
echo $_

## STDOUT:
spaces
## END

#### $_ with pipeline and subshell

shopt -s lastpipe

seq 3 | echo last=$_

echo pipeline=$_

( echo subshell=$_ )
echo done=$_

## STDOUT:
last=
pipeline=last=
subshell=pipeline=last=
done=pipeline=last=
## END

#### $_ with && and ||

echo hi && echo last=$_
echo and=$_

echo hi || echo last=$_
echo or=$_

## STDOUT:
hi
last=hi
and=last=hi
hi
or=hi
## END

#### $_ is not reset with (( and [[

# bash is inconsistent because it does it for pipelines and assignments, but
# not (( and [[

echo simple
(( a = 2 + 3 ))
echo "(( $_"

[[ a == *.py ]]
echo "[[ $_"

## STDOUT:
simple
(( simple
[[ (( simple
## END

#### $_ with assignments, arrays, etc.

: foo
echo "colon [$_]"

s=bar
echo "bare assign [$_]"

declare s=bar
echo "declare [$_]"

a=(1 2)
echo "array [$_]"

declare a=(1 2)
echo "declare array [$_]"

declare -g d=(1 2)
echo "declare flag [$_]"

## STDOUT:
colon [foo]
bare assign []
declare [s=bar]
array []
declare array [a]
declare flag [d]
## END

#### $_ with loop

echo init
echo begin=$_
for x in 1 2 3; do
  echo prev=$_
done

## STDOUT:
init
begin=init
prev=begin=init
prev=prev=begin=init
prev=prev=prev=begin=init
## END

#### $_ is not undefined on first use
set -e

x=$($SH -u -c 'echo prev=$_')
echo status=$?

#echo "$x"

## STDOUT:
status=0
## END

#### BASH_VERSION / OILS_VERSION
    # BASH_VERSION=zz

    echo $BASH_VERSION | egrep -o '4\.4\.0' > /dev/null
    echo matched=$?
## STDOUT:
matched=0
## END

#### $SECONDS

# most likely 0 seconds, but in CI I've seen 1 second
echo $SECONDS | awk '/[0-9]+/ { print "ok" }'

## status: 0
## STDOUT:
ok
## END
