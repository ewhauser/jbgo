## oils_failures_allowed: 0
## compare_shells: bash

#### Env value doesn't persist
FOO=foo printenv.sh FOO
echo -$FOO-
## STDOUT:
foo
--
## END

#### Env value with equals
FOO=foo=foo printenv.sh FOO
## stdout: foo=foo

#### Env binding can use preceding bindings, but not subsequent ones
# This means that for ASSIGNMENT_WORD, on the RHS you invoke the parser again!
# Could be any kind of quoted string.
FOO="foo" BAR="[$FOO][$BAZ]" BAZ=baz printenv.sh FOO BAR BAZ
## STDOUT:
foo
[foo][]
baz

#### Env value with two quotes
FOO='foo'"adjacent" printenv.sh FOO
## stdout: fooadjacent

#### Env value with escaped <
FOO=foo\<foo printenv.sh FOO
## stdout: foo<foo

#### FOO=foo echo [foo]
FOO=foo echo "[$foo]"
## stdout: []

#### FOO=foo fun
fun() {
  echo "[$FOO]"
}
FOO=foo fun
## stdout: [foo]

#### Multiple temporary envs on the stack
g() {
  echo "$F" "$G1" "$G2"
  echo '--- g() ---'
  P=p printenv.sh F G1 G2 A P
}
f() {
  # NOTE: G1 doesn't pick up binding f, but G2 picks up a.
  G1=[$f] G2=[$a] g
  echo '--- f() ---'
  printenv.sh F G1 G2 A P
}
a=A
F=f f
## STDOUT:
f [] [A]
--- g() ---
f
[]
[A]
None
p
--- f() ---
f
None
None
None
None
## END

#### Escaped = in command name
# foo=bar is in the 'spec/bin' dir.
foo\=bar
## stdout: HI

#### Env binding not allowed before compound command
# bash gives exit code 2 for syntax error, because of 'do'.
FOO=bar for i in a b; do printenv.sh $FOO; done
## status: 2

#### Trying to run keyword 'for'
FOO=bar for
## status: 127

#### Empty env binding
EMPTY= printenv.sh EMPTY
## stdout:

#### Assignment doesn't do word splitting
words='one two'
a=$words
argv.sh "$a"
## stdout: ['one two']

#### Assignment doesn't do glob expansion
touch _tmp/z.Z _tmp/zz.Z
a=_tmp/*.Z
argv.sh "$a"
## stdout: ['_tmp/*.Z']

#### Env binding in readonly/declare is NOT exported!  (pitfall)

# All shells agree on this, but it's very confusing behavior.
FOO=foo readonly v=$(printenv.sh FOO)
echo "v=$v"

# bash has probems here:
FOO=foo readonly v2=$FOO
echo "v2=$v2"

## STDOUT:
v=None
v2=foo
## END
## BUG bash STDOUT:
v=None
v2=
## END

#### assignments / array assignments not interpreted after 'echo'
a=1 echo b[0]=2 c=3
## stdout: b[0]=2 c=3

#### dynamic local variables (and splitting)
f() {
  local "$1"  # Only x is assigned here
  echo x=\'$x\'
  echo a=\'$a\'

  local $1  # x and a are assigned here
  echo x=\'$x\'
  echo a=\'$a\'
}
f 'x=y a=b'
## STDOUT:
x='y a=b'
a=''
x='y a=b'
a=''
## END

#### readonly x= gives empty string (regression)
readonly x=
argv.sh "$x"
## STDOUT:
['']
## END

#### 'local x' does not set variable
set -o nounset
f() {
  local x
  echo $x
}
f
## status: 1

#### 'local -a x' does not set variable
set -o nounset
f() {
  local -a x
  echo $x
}
f
## status: 1

#### 'local x' and then array assignment
f() {
  local x
  x[3]=foo
  echo ${x[3]}
}
f
## status: 0
## stdout: foo

#### 'declare -A' and then dict assignment
declare -A foo
key=bar
foo["$key"]=value
echo ${foo["bar"]}
## status: 0
## stdout: value

#### declare in an if statement
# bug caught by my feature detection snippet in bash-completion
if ! foo=bar; then
  echo BAD
fi
echo $foo
if ! eval 'spam=eggs'; then
  echo BAD
fi
echo $spam
## STDOUT:
bar
eggs
## END

#### Modify a temporary binding
# (regression for bug found by Michael Greenberg)
f() {
  echo "x before = $x"
  x=$((x+1))
  echo "x after  = $x"
}
x=5 f
## STDOUT:
x before = 5
x after  = 6
## END

#### Reveal existence of "temp frame" (All shells disagree here!!!)
f() {
  echo "x=$x"

  x=mutated-temp  # mutate temp frame
  echo "x=$x"

  # Declare a new local
  local x='local'
  echo "x=$x"

  # Unset it
  unset x
  echo "x=$x"
}

x=global
x=temp-binding f
echo "x=$x"

## STDOUT:
x=temp-binding
x=mutated-temp
x=local
x=mutated-temp
x=global
## END
## BUG bash STDOUT:
x=temp-binding
x=mutated-temp
x=local
x=global
x=global
## END

#### Test above without 'local' (which is not POSIX)
f() {
  echo "x=$x"

  x=mutated-temp  # mutate temp frame
  echo "x=$x"

  # Unset it
  unset x
  echo "x=$x"
}

x=global
x=temp-binding f
echo "x=$x"

## STDOUT:
x=temp-binding
x=mutated-temp
x=global
x=global
## END

#### Using ${x-default} after unsetting local shadowing a global
f() {
  echo "x=$x"
  local x='local'
  echo "x=$x"
  unset x
  echo "- operator = ${x-default}"
  echo ":- operator = ${x:-default}"
}
x=global
f
## STDOUT:
x=global
x=local
- operator = global
:- operator = global
## END

#### Using ${x-default} after unsetting a temp binding shadowing a global
f() {
  echo "x=$x"
  local x='local'
  echo "x=$x"
  unset x
  echo "- operator = ${x-default}"
  echo ":- operator = ${x:-default}"
}
x=global
x=temp-binding f
## STDOUT:
x=temp-binding
x=local
- operator = temp-binding
:- operator = temp-binding
## END
## BUG bash STDOUT:
x=temp-binding
x=local
- operator = global
:- operator = global
## END

#### static assignment doesn't split
words='a b c'
export ex=$words
glo=$words
readonly ro=$words
argv.sh "$ex" "$glo" "$ro"

## STDOUT:
['a b c', 'a b c', 'a b c']
## END

#### aliased assignment doesn't split
shopt -s expand_aliases || true
words='a b c'
alias e=export
alias r=readonly
e ex=$words
r ro=$words
argv.sh "$ex" "$ro"
## STDOUT:
['a b c', 'a b c']
## END

words='a b c'
e=export
r=readonly
$e ex=$words
$r ro=$words
argv.sh "$ex" "$ro"

## STDOUT:
['a b c', 'a b c']
## END

#### assignment using dynamic var names doesn't split
words='a b c'
arg_ex=ex=$words
arg_ro=ro=$words

# no quotes, this is split of course
export $arg_ex
readonly $arg_ro

argv.sh "$ex" "$ro"

arg_ex2=ex2=$words
arg_ro2=ro2=$words

# quotes, no splitting
export "$arg_ex2"
readonly "$arg_ro2"

argv.sh "$ex2" "$ro2"

## STDOUT:
['a b c', 'a b c']
['a b c', 'a b c']
## END

#### assign and glob
cd $TMP
touch foo=a foo=b
foo=*
argv.sh "$foo"
unset foo

export foo=*
argv.sh "$foo"
unset foo

## STDOUT:
['*']
['*']
## END

#### declare and glob
cd $TMP
touch foo=a foo=b
typeset foo=*
argv.sh "$foo"
unset foo
## STDOUT:
['*']
## END

#### readonly $x where x='b c'
one=a
two='b c'
readonly $two $one
a=new
echo status=$?
b=new
echo status=$?
c=new
echo status=$?

## status: 1
## stdout-json: ""

# most shells make two variable read-only

## OK bash status: 0
## OK bash STDOUT:
status=1
status=1
status=1
## END

#### readonly a=(1 2) no_value c=(3 4) makes 'no_value' readonly
readonly a=(1 2) no_value c=(3 4)
no_value=x
## status: 1
## stdout-json: ""

#### export a=1 no_value c=2
no_value=foo
export a=1 no_value c=2
printenv.sh no_value
## STDOUT:
foo
## END

#### local a=loc $var c=loc
var='b'
b=global
echo $b
f() {
  local a=loc $var c=loc
  argv.sh "$a" "$b" "$c"
}
f
## STDOUT:
global
['loc', '', 'loc']
## END

#### redirect after assignment builtin (eval redirects after evaluating arguments)

# See also: spec/redir-order.test.sh (#2307)
# The $(stdout_stderr.sh) is evaluated *before* the 2>/dev/null redirection

readonly x=$(stdout_stderr.sh) 2>/dev/null
echo done
## STDOUT:
done
## END
## STDERR:
STDERR
## END

#### redirect after command sub (like case above but without assignment builtin)
echo stdout=$(stdout_stderr.sh) 2>/dev/null
## STDOUT:
stdout=STDOUT
## END
## STDERR:
STDERR
## END

#### redirect after bare assignment
x=$(stdout_stderr.sh) 2>/dev/null
echo done
## STDOUT:
done
## END
## stderr-json: ""
## BUG bash STDERR:
STDERR
## END

#### redirect after declare -p

foo=bar
typeset -p foo 1>&2

## STDERR:
typeset foo=bar
## END
## OK bash STDERR:
declare -- foo="bar"
## END
## stdout-json: ""

declare -a arr
arr=(foo bar baz)
declare -a arr
echo arr:${#arr[@]}
## STDOUT:
arr:3
## END

declare -A dict
dict['foo']=hello
dict['bar']=oil
dict['baz']=world
declare -A dict
echo dict:${#dict[@]}
## STDOUT:
dict:3
## END

#### "readonly -a arr" and "readonly -A dict" should not not remove existing arrays

declare -a arr
arr=(foo bar baz)
declare -A dict
dict['foo']=hello
dict['bar']=oil
dict['baz']=world

readonly -a arr
echo arr:${#arr[@]}
readonly -A dict
echo dict:${#dict[@]}
## STDOUT:
arr:3
dict:3
## END

declare -a arr1
readonly -a arr2
declare -A dict1
readonly -A dict2

declare -p arr1 arr2 dict1 dict2
## STDOUT:
declare -a arr1=()
declare -ra arr2=()
declare -A dict1=()
declare -rA dict2=()
## END
## N-I bash STDOUT:
declare -a arr1
declare -r arr2
declare -A dict1
declare -r dict2
## END

#### readonly array should not be modified by a+=(1)

a=(1 2 3)
readonly -a a
eval 'a+=(4)'
argv.sh "${a[@]}"
eval 'declare -n r=a; r+=(4)'
argv.sh "${a[@]}"

## STDOUT:
['1', '2', '3']
['1', '2', '3']
## END
