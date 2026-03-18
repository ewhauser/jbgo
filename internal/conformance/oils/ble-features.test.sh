## compare_shells: bash

#### [bash_unset] local-unset / dynamic-unset for localvar
unlocal() { unset -v "$1"; }

f1() {
  local v=local
  unset v
  echo "[$1,local,(unset)] v: ${v-(unset)}"
}
v=global
f1 global

f1() {
  local v=local
  unlocal v
  echo "[$1,local,(unlocal)] v: ${v-(unset)}"
}
v=global
f1 'global'

## STDOUT:
# bash-unset
#   local-unset   = value-unset
#   dynamic-unset = cell-unset
[global,local,(unset)] v: (unset)
[global,local,(unlocal)] v: global
## END

#### [bash_unset] local-unset / dynamic-unset for localvar (mutated from tempenv)
unlocal() { unset -v "$1"; }

f1() {
  local v=local
  unset v
  echo "[$1,local,(unset)] v: ${v-(unset)}"
}
v=global
v=tempenv f1 'global,tempenv'

f1() {
  local v=local
  unlocal v
  echo "[$1,local,(unlocal)] v: ${v-(unset)}"
}
v=global
v=tempenv f1 'global,tempenv'

## STDOUT:
# bash-unset (bash-5.1)
#   local-unset   = local-unset
#   dynamic-unset = cell-unset
[global,tempenv,local,(unset)] v: (unset)
[global,tempenv,local,(unlocal)] v: global
## END

# Note on bug in bash 4.3 to bash 5.0
# [global,tempenv,local,(unset)] v: global
# [global,tempenv,local,(unlocal)] v: global

#### [bash_unset] local-unset / dynamic-unset for tempenv
unlocal() { unset -v "$1"; }

f1() {
  unset v
  echo "[$1,(unset)] v: ${v-(unset)}"
}
v=global
v=tempenv f1 'global,tempenv'

f1() {
  unlocal v
  echo "[$1,(unlocal)] v: ${v-(unset)}"
}
v=global
v=tempenv f1 'global,tempenv'

## STDOUT:
# always-cell-unset, bash-unset
#   local-unset   = cell-unset
#   dynamic-unset = cell-unset
[global,tempenv,(unset)] v: global
[global,tempenv,(unlocal)] v: global
## END

#### [bash_unset] function call with tempenv vs tempenv-eval
unlocal() { unset -v "$1"; }

f5() {
  echo "[$1] v: ${v-(unset)}"
  local v
  echo "[$1,local] v: ${v-(unset)}"
  ( unset v
    echo "[$1,local+unset] v: ${v-(unset)}" )
  ( unlocal v
    echo "[$1,local+unlocal] v: ${v-(unset)}" )
}
v=global
f5 'global'
v=tempenv f5 'global,tempenv'
v=tempenv eval 'f5 "global,tempenv,(eval)"'

## STDOUT:
# bash-unset (bash-5.1)
[global] v: global
[global,local] v: (unset)
[global,local+unset] v: (unset)
[global,local+unlocal] v: global
[global,tempenv] v: tempenv
[global,tempenv,local] v: tempenv
[global,tempenv,local+unset] v: (unset)
[global,tempenv,local+unlocal] v: global
[global,tempenv,(eval)] v: tempenv
[global,tempenv,(eval),local] v: tempenv
[global,tempenv,(eval),local+unset] v: (unset)
[global,tempenv,(eval),local+unlocal] v: tempenv
## END

# Note on bug in bash 4.3 to bash 5.0
# [global] v: global
# [global,local] v: (unset)
# [global,local+unset] v: (unset)
# [global,local+unlocal] v: global
# [global,tempenv] v: tempenv
# [global,tempenv,local] v: tempenv
# [global,tempenv,local+unset] v: global
# [global,tempenv,local+unlocal] v: global
# [global,tempenv,(eval)] v: tempenv
# [global,tempenv,(eval),local] v: tempenv
# [global,tempenv,(eval),local+unset] v: (unset)
# [global,tempenv,(eval),local+unlocal] v: tempenv

#### [bash_unset] localvar-inherit from tempenv
f1() {
  local v
  echo "[$1,(local)] v: ${v-(unset)}"
}
f2() {
  f1 "$1,(func)"
}
f3() {
  local v=local
  f1 "$1,local,(func)"
}
v=global

f1 'global'
v=tempenv f1 'global,tempenv'
(export v=global; f1 'xglobal')

f2 'global'
v=tempenv f2 'global,tempenv'
(export v=global; f2 'xglobal')

f3 'global'

## STDOUT:
# init.bash
#   init.unset   for local
#   init.inherit for tempenv
[global,(local)] v: (unset)
[global,tempenv,(local)] v: tempenv
[xglobal,(local)] v: (unset)
[global,(func),(local)] v: (unset)
[global,tempenv,(func),(local)] v: tempenv
[xglobal,(func),(local)] v: (unset)
[global,local,(func),(local)] v: (unset)
## END

#### [compat_array] ${arr} is ${arr[0]}

arr=(foo bar baz)
argv.sh "$arr" "${arr}"
## stdout: ['foo', 'foo']

#### [compat_array] scalar write to arrays

a=(1 0 0)
: $(( a++ ))
argv.sh "${a[@]}"
## stdout: ['2', '0', '0']

#### [compat_array] scalar write to associative arrays

declare -A d=()
d['0']=1
d['foo']=hello
d['bar']=world
((d++))
argv.sh ${d['0']} ${d['foo']} ${d['bar']}
## stdout: ['2', 'hello', 'world']

#### [compat_array] ${alpha@a}
declare -A alpha=(['1']=2)
echo type=${alpha@a}
echo type=${alpha@a}
## STDOUT:
type=A
type=A
## END
