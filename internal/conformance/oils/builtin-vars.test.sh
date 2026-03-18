## oils_failures_allowed: 1
## compare_shells: bash

# Tests for builtins having to do with variables: export, readonly, unset, etc.
#
# Also see assign.test.sh.

#### Export sets a global variable
# Even after you do export -n, it still exists.
f() { export GLOBAL=X; }
f
echo $GLOBAL
printenv.sh GLOBAL
## STDOUT:
X
X
## END

#### Export sets a global variable that persists after export -n
f() { export GLOBAL=X; }
f
echo $GLOBAL
printenv.sh GLOBAL
export -n GLOBAL
echo $GLOBAL
printenv.sh GLOBAL
## STDOUT: 
X
X
X
None
## END

#### export -n undefined is ignored
set -o errexit
export -n undef
echo status=$?
## stdout: status=0

#### export -n foo=bar not allowed
foo=old
export -n foo=new
echo status=$?
echo $foo
## STDOUT:
status=2
old
## END
## OK bash STDOUT:
status=0
new
## END

#### Export a global variable and unset it
f() { export GLOBAL=X; }
f
echo $GLOBAL
printenv.sh GLOBAL
unset GLOBAL
echo g=$GLOBAL
printenv.sh GLOBAL
## STDOUT: 
X
X
g=
None
## END

#### Export existing global variables
G1=g1
G2=g2
export G1 G2
printenv.sh G1 G2
## STDOUT: 
g1
g2
## END

#### Export existing local variable
f() {
  local L1=local1
  export L1
  printenv.sh L1
}
f
printenv.sh L1
## STDOUT: 
local1
None
## END

#### Export a local that shadows a global
V=global
f() {
  local V=local1
  export V
  printenv.sh V
}
f
printenv.sh V  # exported local out of scope; global isn't exported yet
export V
printenv.sh V  # now it's exported
## STDOUT: 
local1
None
global
## END

#### Export a variable before defining it
export U
U=u
printenv.sh U
## stdout: u

#### Unset exported variable, then define it again.  It's NOT still exported.
export U
U=u
printenv.sh U
unset U
printenv.sh U
U=newvalue
echo $U
printenv.sh U
## STDOUT:
u
None
newvalue
None
## END

#### Exporting a parent func variable (dynamic scope)
# The algorithm is to walk up the stack and export that one.
inner() {
  export outer_var
  echo "inner: $outer_var"
  printenv.sh outer_var
}
outer() {
  local outer_var=X
  echo "before inner"
  printenv.sh outer_var
  inner
  echo "after inner"
  printenv.sh outer_var
}
outer
## STDOUT:
before inner
None
inner: X
X
after inner
X
## END

#### Dependent export setting
# FOO is not respected here either.
export FOO=foo v=$(printenv.sh FOO)
echo "v=$v"
## stdout: v=None

#### Exporting a variable doesn't change it
old=$PATH
export PATH
new=$PATH
test "$old" = "$new" && echo "not changed"
## stdout: not changed

#### can't export array (strict_array)

typeset -a a
a=(1 2 3)

export a
printenv.sh a
## STDOUT:
None
## END

#### can't export associative array (strict_array)

typeset -A a
a["foo"]=bar

export a
printenv.sh a
## STDOUT:
None
## END

#### assign to readonly variable
# bash doesn't abort unless errexit!
readonly foo=bar
foo=eggs
echo "status=$?"  # nothing happens
## status: 1
## BUG bash stdout: status=1
## BUG bash status: 0

#### Make an existing local variable readonly
f() {
	local x=local
	readonly x
	echo $x
	eval 'x=bar'  # Wrap in eval so it's not fatal
	echo status=$?
}
x=global
f
echo $x
## STDOUT:
local
status=1
global
## END

#### assign to readonly variable - errexit
set -o errexit
readonly foo=bar
foo=eggs
echo "status=$?"  # nothing happens
## status: 1

#### Unset a variable
foo=bar
echo foo=$foo
unset foo
echo foo=$foo
## STDOUT:
foo=bar
foo=
## END

#### Unset exit status
V=123
unset V
echo status=$?
## stdout: status=0

#### Unset nonexistent variable
unset ZZZ
echo status=$?
## stdout: status=0

#### Unset readonly variable
readonly R=foo
unset R
echo status=$?
## status: 0
## stdout: status=1

#### Unset a function without -f
f() {
  echo foo
}
f
unset f
f
## stdout: foo
## status: 127

#### Unset has dynamic scope
f() {
  unset foo
}
foo=bar
echo foo=$foo
f
echo foo=$foo
## STDOUT:
foo=bar
foo=
## END

#### Unset and scope (bug #653)
unlocal() { unset "$@"; }

level2() {
  local hello=yy

  echo level2=$hello
  unlocal hello
  echo level2=$hello
}

level1() {
  local hello=xx

  level2

  echo level1=$hello
  unlocal hello
  echo level1=$hello

  level2
}

hello=global
level1

## STDOUT:
level2=yy
level2=xx
level1=xx
level1=global
level2=yy
level2=global
## END

#### unset of local reveals variable in higher scope

# consistent.

x=global
f() {
  local x=foo
  echo x=$x
  unset x
  echo x=$x
}
f
## STDOUT:
x=foo
x=global
## END

#### Unset invalid variable name
unset %
echo status=$?
## STDOUT:
status=2
## END

#### Unset nonexistent variable
unset _nonexistent__
echo status=$?
## STDOUT:
status=0
## END

#### Unset -v
foo() {
  echo "function foo"
}
foo=bar
unset -v foo
echo foo=$foo
foo
## STDOUT: 
foo=
function foo
## END

#### Unset -f
foo() {
  echo "function foo"
}
foo=bar
unset -f foo
echo foo=$foo
foo
echo status=$?
## STDOUT: 
foo=bar
status=127
## END

#### Unset array member
a=(x y z)
unset 'a[1]'
echo status=$?
echo "${a[@]}" len="${#a[@]}"
## STDOUT:
status=0
x z len=2
## END

#### Unset errors
unset undef
echo status=$?

a=(x y z)
unset 'a[99]'  # out of range
echo status=$?

unset 'not_array[99]'  # not an array
echo status=$?

## STDOUT:
status=0
status=0
status=0
## END

#### Unset wrong type

declare undef
unset -v 'undef[1]'
echo undef $?
unset -v 'undef["key"]'
echo undef $?

declare a=(one two)
unset -v 'a[1]'
echo array $?

#shopt -s strict_arith || true
# strict_arith is on, when it fails.
unset -v 'a["key"]'
echo array $?

declare -A A=(['key']=val)
unset -v 'A[1]'
echo assoc $?
unset -v 'A["key"]'
echo assoc $?

## STDOUT:
undef 1
undef 1
array 0
array 1
assoc 0
assoc 0
## END

#### unset -v assoc (related to issue #661)

declare -A dict=()
key=1],a[1
dict["$key"]=foo
echo ${#dict[@]}
echo keys=${!dict[@]}
echo vals=${dict[@]}

unset -v 'dict["$key"]'
echo ${#dict[@]}
echo keys=${!dict[@]}
echo vals=${dict[@]}
## STDOUT:
1
keys=1],a[1
vals=foo
0
keys=
vals=
## END

#### unset assoc errors

declare -A assoc=(['key']=value)
unset 'assoc["nonexistent"]'
echo status=$?

## STDOUT:
status=0
## END

#### Unset array member with dynamic parsing

i=1
a=(w x y z)
unset 'a[ i - 1 ]' a[i+1]  # note: can't have space between a and [
echo status=$?
echo "${a[@]}" len="${#a[@]}"
## STDOUT:
status=0
x z len=2
## END

#### Use local twice
f() {
  local foo=bar
  local foo
  echo $foo
}
f
## stdout: bar

#### Local without variable is still unset!
set -o nounset
f() {
  local foo
  echo "[$foo]"
}
f
## stdout-json: ""
## status: 1

#### local after readonly
f() { 
  readonly y
  local x=1 y=$(( x ))
  echo y=$y
}
f
echo y=$y
## status: 1
## stdout-json: ""

## BUG bash status: 0
## BUG bash STDOUT:
y=
y=
## END

#### unset a[-1] (bf.bash regression)

a=(1 2 3)
unset a[-1]
echo len=${#a[@]}

echo last=${a[-1]}
(( last = a[-1] ))
echo last=$last

(( a[-1] = 42 ))
echo "${a[@]}"

## STDOUT:
len=2
last=2
last=2
1 42
## END

#### unset a[-1] in sparse array (bf.bash regression)

a=(0 1 2 3 4)
unset a[1]
unset a[4]
echo len=${#a[@]} a=${a[@]}
echo last=${a[-1]} second=${a[-2]} third=${a[-3]}

echo ---
unset a[3]
echo len=${#a[@]} a=${a[@]}
echo last=${a[-1]} second=${a[-2]} third=${a[-3]}

## STDOUT:
len=3 a=0 2 3
last=3 second=2 third=
---
len=2 a=0 2
last=2 second= third=0
## END

