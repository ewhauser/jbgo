## compare_shells: bash
## oils_failures_allowed: 1
## oils_cpp_failures_allowed: 2
# case #24 with ulimit -f 1 is different under C++ for some reason - could be due to the python2
# intepreter and SIGXFSZ

#### exec builtin 
exec echo hi
## stdout: hi

#### exec builtin with redirects
exec 1>&2
echo 'to stderr'
## stdout-json: ""
## stderr: to stderr

#### exec builtin with here doc
# This has in a separate file because both code and data can be read from
# stdin.
$SH $REPO_ROOT/spec/bin/builtins-exec-here-doc-helper.sh
## STDOUT:
x=one
y=two
DONE
## END

#### exec builtin accepts --
exec -- echo hi
## STDOUT:
hi
## END

#### exec -- 2>&1
exec -- 3>&1
echo stdout 1>&3
## STDOUT:
stdout
## END

#### exec -a sets argv[0]
exec -a FOOPROC sh -c 'echo $0'
## STDOUT:
FOOPROC
## END

#### Exit out of function
f() { exit 3; }
f
exit 4
## status: 3

#### Exit builtin with invalid arg 
exit invalid
# Rationale: runtime errors are 1
## status: 1

#### Exit builtin with too many args
exit 7 8 9
echo status=$?
## status: 2
## stdout-json: ""

#### time with brace group argument

err=time-$(basename $SH).txt
{
  time {
    sleep 0.01
    sleep 0.02
  }
} 2> $err

grep --only-matching user $err
echo result=$?

# Regression: check fractional seconds
gawk '
BEGIN { ok = 0 }
match( $0, /\.([0-9]+)/, m) {
  if (m[1] > 0) {  # check fractional seconds
    ok = 1
  }
}
END { if (ok) { print "non-zero" } }
' $err

## status: 0
## STDOUT:
user
result=0
non-zero
## END

# time doesn't accept a block?

#### ulimit with no flags is like -f

ulimit > no-flags.txt
echo status=$?

ulimit -f > f.txt
echo status=$?

diff -u no-flags.txt f.txt
echo diff=$?

# Print everything
# ulimit -a

## STDOUT:
status=0
status=0
diff=0
## END

#### ulimit too many args

ulimit 1 2
if test $? -ne 0; then
  echo pass
else
  echo fail
fi

#ulimit -f

## STDOUT:
pass
## END

#### ulimit negative flag

ulimit -f

# interpreted as a flag
ulimit -f -42
if test $? -ne 0; then
  echo pass
else
  echo fail
fi

## STDOUT:
unlimited
pass
## END

#### ulimit negative arg

ulimit -f

# an arg
ulimit -f -- -42
if test $? -ne 0; then
  echo pass
else
  echo fail
fi

## STDOUT:
unlimited
pass
## END

#### ulimit -a doesn't take arg
exit

ulimit -a 42
if test $? -ne 0; then
  echo 'failure that was expected'
fi

## STDOUT:
failure that was expected
## END
## BUG bash STDOUT:
## END

#### ulimit doesn't accept multiple flags - reduce confusion between shells

#   flag parsing, like

#   ulimit -f 999 -n
#   ulimit -f 999 -n 888
#
#
# we want to make it clear we're like the latter

# can't print all and -f
ulimit -f -a >/dev/null
echo status=$?

ulimit -f -n >/dev/null
echo status=$?

ulimit -f -n 999 >/dev/null
echo status=$?

## STDOUT:
status=2
status=2
status=2
## END

exit

ulimit -a > short.txt
ulimit --all > long.txt

wc -l short.txt long.txt

diff -u short.txt long.txt
echo status=$?

## STDOUT:
  8 short.txt
  8 long.txt
 16 total
status=0
## END

#### ulimit accepts 'unlimited'

for arg in zz unlimited; do
  echo "  arg $arg"
  ulimit -f
  echo status=$?
  ulimit -f $arg
  if test $? -ne 0; then
    echo 'FAILED'
  fi
  echo
done
## STDOUT:
  arg zz
unlimited
status=0
FAILED

  arg unlimited
unlimited
status=0

## END

#### ulimit of 2**32, 2**31 (int overflow)

echo -n 'one '; ulimit -f

ulimit -f $(( 1 << 32 ))

echo -n 'two '; ulimit -f

ulimit -f $(( 1 << 31 ))

echo -n 'three '; ulimit -f

## STDOUT:
one unlimited
two 4294967296
three 2147483648
## END

#### ulimit that is 64 bits

# no 64-bit integers

echo -n 'before '; ulimit -f

# 1 << 63 overflows signed int

# 512 is 1 << 9, so make it 62-9 = 53 bits

lim=$(( 1 << 53 ))
#echo $lim

# bash says this is out of range
ulimit -f $lim

echo -n 'after '; ulimit -f

## STDOUT:
before unlimited
after 9007199254740992
## END

#### arg that would overflow 64 bits is detected

# no 64-bit integers

echo -n 'before '; ulimit -f

# 1 << 63 overflows signed int

lim=$(( (1 << 62) + 1 ))
#echo lim=$lim

# bash detects that this is out of range

ulimit -f $lim
echo -n 'after '; ulimit -f

## STDOUT:
before unlimited
after unlimited
## END

#### ulimit -f 1 prevents files larger 512 bytes
trap - XFSZ  # don't handle this

rm -f err.txt
touch err.txt

bytes() {
  local n=$1
  local st=0
  for i in $(seq $n); do
    echo -n x
    st=$?
    if test $st -ne 0; then
      echo "ERROR: echo failed with status $st" >> err.txt
    fi
  done
}

ulimit -f 1

bytes 512 > ok.txt
echo 512 status=$?

bytes 513 > too-big.txt
echo 513 status=$?
echo

wc --bytes ok.txt too-big.txt
echo

cat err.txt

## status: -25
## STDOUT:
512 status=0
## END

## OK disabledosh status: 0
## OK disabledosh STDOUT:
512 status=0
513 status=0

 512 ok.txt
 512 too-big.txt
1024 total

ERROR: echo failed with status 1
## END

## BUG bash status: 0
## BUG bash STDOUT:
512 status=0
513 status=0

 512 ok.txt
 513 too-big.txt
1025 total

## END

#### write big file with ulimit

# I think this will test write() errors, rather than the final flush() error
# (which is currently skipped by C++

{ echo 'ulimit -f 1'
  # More than 8 KiB may cause a flush()
  printf 'echo '; head -c 9000 < /dev/zero | tr '\0' X; printf ' >out.txt\n'
  echo 'echo inner=$?'
} > big.sh

$SH big.sh
echo outer=$?

## STDOUT:
outer=153
## END

# not sure why this is different

#### ulimit -S for soft limit (default), -H for hard limit

# Mus be ulimit -S -n 1111

show_state() {
  local msg=$1
  echo "$msg"
  echo -n '  '; ulimit -S -t
  echo -n '  '; ulimit -H -t
  echo
}

show_state 'init'

ulimit -S -t 123456
show_state '-S'

ulimit -H -t 123457
show_state '-H'

ulimit -t 123455
show_state 'no flag'

echo 'GET'

ulimit -S -t 123454
echo -n '  '; ulimit -t
echo -n '  '; ulimit -S -t
echo -n '  '; ulimit -H -t

## STDOUT:
init
  unlimited
  unlimited

-S
  123456
  unlimited

-H
  123456
  123457

no flag
  123455
  123455

GET
  123454
  123454
  123455
## END

#### Changing resource limit is denied

# Not sure why these don't work

flag=-t

ulimit -S -H $flag 100
echo both=$?

ulimit -S $flag 90
echo soft=$?

ulimit -S $flag 95
echo soft=$?

ulimit -S $flag 105
if test $? -ne 0; then
  echo soft OK
else
  echo soft fail
fi

ulimit -H $flag 200
if test $? -ne 0; then
  echo hard OK
else
  echo hard fail
fi

## STDOUT:
both=0
soft=0
soft=0
soft OK
hard OK
## END

#### ulimit -n limits file descriptors

$SH -c 'ulimit -n 64; echo hi >out'
echo status=$?

$SH -c 'ulimit -n 0; echo hi >out'
echo status=$?

## STDOUT:
status=0
status=1
## END

