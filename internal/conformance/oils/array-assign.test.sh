## compare_shells: bash
## oils_failures_allowed: 9

# Note: this file elaborates on spec/ble-idioms.test.sh

#### Indexed LHS without spaces, and +=
a[1]=x
echo status=$?
argv.sh "${a[@]}"

a[0+2]=y
argv.sh "${a[@]}"

# += does appending
a[0+2]+=z
argv.sh "${a[@]}"

## STDOUT:
status=0
['x']
['x', 'y']
['x', 'yz']
## END

#### Indexed LHS with spaces

a[1 * 1]=x
a[ 1 + 2 ]=z
echo status=$?

argv.sh "${a[@]}"
## STDOUT:
status=0
['x', 'z']
## END

#### Nested a[i[0]]=0

i=(0 1 2)

a[i[0]]=0
a[ i[1] ]=1
a[ i[2] ]=2
a[ i[1]+i[2] ]=3

argv.sh "${a[@]}"

## STDOUT:
['0', '1', '2', '3']
## END

#### Multiple LHS array words

a=(0 1 2)
b=(3 4 5)

#declare -p a b

HOME=/home/spec-test

# empty string, and tilde sub
a[0 + 1]=  b[2 + 0]=~/src

typeset -p a b

echo ---

# In bash, this bad prefix binding prints an error, but nothing fails
a[0 + 1]='foo' argv.sh b[2 + 0]='bar'
echo status=$?

typeset -p a b

## STDOUT:
declare -a a=([0]="0" [1]="" [2]="2")
declare -a b=([0]="3" [1]="4" [2]="/home/spec-test/src")
---
['b[2', '+', '0]=bar']
status=0
declare -a a=([0]="0" [1]="" [2]="2")
declare -a b=([0]="3" [1]="4" [2]="/home/spec-test/src")
## END

#### LHS array is protected with shopt -s eval_unsafe_arith, e.g. 'a[$(echo 2)]'

a=(0 1 2)
b=(3 4 5)
typeset -p b

expr='a[$(echo 2)]' 

echo 'get' "${b[expr]}"

b[expr]=zzz

echo 'set' "${b[expr]}"
typeset -p b

## STDOUT:
declare -a b=([0]="3" [1]="4" [2]="5")
get 5
set zzz
declare -a b=([0]="3" [1]="4" [2]="zzz")
## END

#### file named a[ is  not executed

PATH=".:$PATH"

for name in 'a[' 'a[5'; do
  echo "echo hi from $name: \$# args: \$@" > "$name"
  chmod +x "$name"
done

# this does not executed a[5
a[5 + 1]=
a[5 / 1]=y
echo len=${#a[@]}

# Not detected as assignment because there's a non-arith character
a[5 # 1]=

## status: 1
## STDOUT:
len=2
## END

#### More fragments like a[  a[5  a[5 +  a[5 + 3]

for name in 'a[' 'a[5'; do
  echo "echo hi from $name: \$# args: \$@" > "$name"
  chmod +x "$name"
done

# syntax error in bash
$SH -c 'a['
echo "a[ status=$?"

$SH -c 'a[5'
echo "a[5 status=$?"

# 1 arg +
$SH -c 'a[5 +'
echo "a[5 + status=$?"

# 2 args
$SH -c 'a[5 + 3]'
echo "a[5 + 3] status=$?"

$SH -c 'a[5 + 3]='
echo "a[5 + 3]= status=$?"

$SH -c 'a[5 + 3]+'
echo "a[5 + 3]+ status=$?"

$SH -c 'a[5 + 3]+='
echo "a[5 + 3]+= status=$?"

# and it doesn't turn a[5 + 3] and a[5 + 3]+ into commands!

## STDOUT:
a[ status=127
a[5 status=127
a[5 + status=127
a[5 + 3] status=127
a[5 + 3]= status=0
a[5 + 3]+ status=127
a[5 + 3]+= status=0
## END

## BUG bash STDOUT:
a[ status=2
a[5 status=2
a[5 + status=2
a[5 + 3] status=127
a[5 + 3]= status=0
a[5 + 3]+ status=127
a[5 + 3]+= status=0
## END

#### Are quotes allowed?

# double quotes allowed in bash
a["1"]=2
echo status=$? len=${#a[@]}

a['2']=3
echo status=$? len=${#a[@]}

# allowed in bash
a[2 + "3"]=5
echo status=$? len=${#a[@]}

a[3 + '4']=5
echo status=$? len=${#a[@]}

## status: 1
## STDOUT:
## END

# syntax errors are not fatal in bash

## BUG bash status: 0
## BUG bash STDOUT:
status=0 len=1
status=1 len=1
status=0 len=2
status=1 len=2
## END

#### Tricky parsing - a[ a[0]=1 ]=X  a[ a[0]+=1 ]+=X

# the nested [] means we can't use regular language lookahead?

echo assign=$(( z[0] = 42 ))

a[a[0]=1]=X
declare -p a

a[ a[2]=3 ]=Y
declare -p a

echo ---

a[ a[0]+=1 ]+=X
declare -p a

## STDOUT:
assign=42
declare -a a=([0]="1" [1]="X")
declare -a a=([0]="1" [1]="X" [2]="3" [3]="Y")
---
declare -a a=([0]="2" [1]="X" [2]="3X" [3]="Y")
## END

#### argv.sh a[1 + 2]=

# This tests that the worse parser doesn't unconditinoally treat a[ as special

a[1 + 2]= argv.sh a[1 + 2]=
echo status=$?

a[1 + 2]+= argv.sh a[1 + 2]+=
echo status=$?

argv.sh a[3 + 4]=

argv.sh a[3 + 4]+=

## STDOUT:
['a[1', '+', '2]=']
status=0
['a[1', '+', '2]+=']
status=0
['a[3', '+', '4]=']
['a[3', '+', '4]+=']
## END

#### declare builtin doesn't allow spaces

declare a[a[0]=1]=X
declare -p a

declare a[ a[2]=3 ]=Y
declare -p a

## STDOUT:
declare -a a=([0]="1" [1]="X")
declare -a a=([0]="1" [1]="X" [2]="3")
## END

