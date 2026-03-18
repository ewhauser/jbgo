# xtrace test.  Test PS4 and line numbers, etc.

## oils_failures_allowed: 1
## compare_shells: bash

#### unset PS4

set -x
echo 1
unset PS4
echo 2
## STDOUT:
1
2
## STDERR:
+ echo 1
+ unset PS4
echo 2
## END

#### set -o verbose prints unevaluated code
set -o verbose
x=foo
y=bar
echo $x
echo $(echo $y)
## STDOUT:
foo
bar
## STDERR:
x=foo
y=bar
echo $x
echo $(echo $y)
## OK bash STDERR:
x=foo
y=bar
echo $x
echo $(echo $y)
## END

#### xtrace with unprintable chars

$SH >stdout 2>stderr <<'EOF'

s=$'a\x03b\004c\x00d'
set -o xtrace
echo "$s"
EOF

show_hex() { od -A n -t c -t x1; }

echo STDOUT
cat stdout | show_hex
echo

echo STDERR
grep 'echo' stderr 

## STDOUT:
STDOUT
   a 003   b 004   c  \0   d  \n
  61  03  62  04  63  00  64  0a

STDERR
+ echo $'a\u0003b\u0004c\u0000d'
## END

## OK bash STDOUT:
STDOUT
   a 003   b 004   c  \n
  61  03  62  04  63  0a

STDERR
+ echo $'a\003b\004c'
## END

#### xtrace with unicode chars

mu1='[μ]'
mu2=$'[\u03bc]'

set -o xtrace
echo "$mu1" "$mu2"

## STDOUT:
[μ] [μ]
## END
## STDERR:
+ echo '[μ]' '[μ]'
## END

#### xtrace with paths
set -o xtrace
echo my-dir/my_file.cc
## STDOUT:
my-dir/my_file.cc
## END
## STDERR:
+ echo my-dir/my_file.cc
## END

#### xtrace with tabs

set -o xtrace
echo $'[\t]'
## stdout-json: "[\t]\n"
## STDERR:
+ echo $'[\t]'
## END
# this is a bug because it's hard to see
## BUG bash stderr-json: "+ echo '[\t]'\n"

#### xtrace with whitespace, quotes, and backslash
set -o xtrace
echo '1 2' \' \" \\
## STDOUT:
1 2 ' " \
## END

## STDERR:
+ echo '1 2' $'\'' '"' $'\\'
## END

#### xtrace with newlines
# want.
set -x
echo $'[\n]'
## STDOUT:
[
]
## STDERR: 
+ echo $'[\n]'
## END
# bash has ugly output that spans lines
## OK bash STDERR:
+ echo '[
]'
## END

#### xtrace written before command executes
set -x
echo one >&2
echo two >&2
## stdout-json: ""
## STDERR:
+ echo one
one
+ echo two
two

#### Assignments and assign builtins
set -x
x=1 x=2; echo $x; readonly x=3
## STDOUT:
2
## END
## STDERR:
+ x=1
+ x=2
+ echo 2
+ readonly x=3
## END
## OK bash STDERR:
+ x=1
+ x=2
+ echo 2
+ readonly x=3
+ x=3
## END

#### [[ ]]

set -x

dir=/
if [[ -d $dir ]]; then
  (( a = 42 ))
fi
## stdout-json: ""
## STDERR:
+ dir=/
+ [[ -d $dir ]]
+ (( a = 42 ))
## END
## OK bash STDERR:
+ dir=/
+ [[ -d / ]]
+ ((  a = 42  ))
## END

#### PS4 is scoped
set -x
echo one
f() { 
  local PS4='- '
  echo func;
}
f
echo two
## STDERR:
+ echo one
+ f
+ local 'PS4=- '
- echo func
+ echo two
## END

#### xtrace with variables in PS4
PS4='+$x:'
set -o xtrace
x=1
echo one
x=2
echo two
## STDOUT:
one
two
## END

## STDERR:
+:x=1
+1:echo one
+1:x=2
+2:echo two
## END

#### PS4 with unterminated ${
x=1
PS4='+${x'
set -o xtrace
echo one
echo status=$?
## STDOUT:
one
status=0
## END

#### PS4 with unterminated $(
x=1
PS4='+$(x'
set -o xtrace
echo one
echo status=$?
## STDOUT:
one
status=0
## END

#### PS4 with runtime error
x=1
PS4='+oops $(( 1 / 0 )) \$'
set -o xtrace
echo one
echo status=$?
## STDOUT:
one
status=0
## END

#### Reading $? in PS4
PS4='[last=$?] '
set -x
false
echo ok
## STDOUT:
ok
## END
## STDERR:
[last=0] false
[last=1] echo ok
## END

#### Regression: xtrace for "declare -a a+=(v)"

a=(1)
set -x
declare a+=(2)
## STDERR:
+ declare a+=(2)
## END
## OK bash STDERR:
+ a+=('2')
+ declare a
## END

#### Regression: xtrace for "a+=(v)"

a=(1)
set -x
a+=(2)
## STDERR:
+ a+=(2)
## END
