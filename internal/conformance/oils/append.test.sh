# spec/append.test.sh: Test +=

## compare_shells: bash

#### Append string to string
s='abc'
s+=d
echo $s
## stdout: abcd

#### Append array to array
a=(x y )
a+=(t 'u v')
argv.sh "${a[@]}"
## stdout: ['x', 'y', 't', 'u v']

#### Append string to undefined variable

s+=foo
echo s=$s

# I think that's a mistake, but += is a legacy construct, so let's copy it.

set -u

t+=foo
echo t=$t
t+=foo
echo t=$t
## STDOUT:
s=foo
t=foo
t=foofoo
## END

#### Append to array to undefined variable

y+=(c d)
argv.sh "${y[@]}"
## STDOUT:
['c', 'd']
## END

#### error: s+=(my array)
s='abc'
s+=(d e f)
argv.sh "${s[@]}"
## status: 0
## STDOUT:
['abc', 'd', 'e', 'f']
## END

#### error: myarray+=s

# They treat this as implicit index 0.  We disallow this on the LHS, so we will
# also disallow it on the RHS.
a=(x y )
a+=z
argv.sh "${a[@]}"
## status: 1
## stdout-json: ""

#### typeset s+=(my array)
typeset s='abc'
echo $s

typeset s+=(d e f)
echo status=$?
argv.sh "${s[@]}"

## status: 0
## STDOUT:
abc
status=0
['abc', 'd', 'e', 'f']
## END

#### error: typeset myarray+=s
typeset a=(x y)
argv.sh "${a[@]}"
typeset a+=s
argv.sh "${a[@]}"

## status: 1
## STDOUT:
['x', 'y']
## END
## BUG bash status: 0
## BUG bash STDOUT:
['x', 'y']
['xs', 'y']
## END

#### error: append used like env prefix
# This should be an error in other shells but it's not.
A=a
A+=a printenv.sh A
## status: 2

#### myarray[1]+=s - Append to element
# They treat this as implicit index 0.  We disallow this on the LHS, so we will
# also disallow it on the RHS.
a=(x y )
a[1]+=z
argv.sh "${a[@]}"
## status: 0
## stdout: ['x', 'yz']

#### myarray[-1]+=s - Append to last element
a=(1 '2 3')
a[-1]+=' 4'
argv.sh "${a[@]}"
## stdout: ['1', '2 3 4']

#### Try to append list to element
# bash - runtime error: cannot assign list to array number
a=(1 '2 3')
a[-1]+=(4 5)
argv.sh "${a[@]}"

## stdout-json: ""
## status: 2

## OK bash status: 0
## OK bash STDOUT:
['1', '2 3']
## END

#### Strings have value semantics, not reference semantics
s1='abc'
s2=$s1
s1+='d'
echo $s1 $s2
## stdout: abcd abc

#### typeset s+= 

typeset s+=foo
echo s=$s

# I think that's a mistake, but += is a legacy construct, so let's copy it.

set -u

typeset t+=foo
echo t=$t
typeset t+=foo
echo t=$t
## STDOUT:
s=foo
t=foo
t=foofoo
## END

#### typeset s${dyn}+= 

dyn=x

typeset s${dyn}+=foo
echo sx=$sx

# I think that's a mistake, but += is a legacy construct, so let's copy it.

set -u

typeset t${dyn}+=foo
echo tx=$tx
typeset t${dyn}+=foo
echo tx=$tx
## STDOUT:
sx=foo
tx=foo
tx=foofoo
## END

#### export readonly +=

export e+=foo
echo e=$e

readonly r+=bar
echo r=$r

set -u

export e+=foo
echo e=$e

#readonly r+=foo
#echo r=$e

## STDOUT:
e=foo
r=bar
e=foofoo
## END

#### local +=

f() {
  local s+=foo
  echo s=$s

  set -u
  local s+=foo
  echo s=$s
}

f
## STDOUT:
s=foo
s=foofoo
## END

#### assign builtin appending array: declare d+=(d e)

declare d+=(d e)
echo "${d[@]}"
declare d+=(c l)
echo "${d[@]}"

readonly r+=(r e)
echo "${r[@]}"
# can't do this again

f() {
  local l+=(l o)
  echo "${l[@]}"

  local l+=(c a)
  echo "${l[@]}"
}

f

## STDOUT:
d e
d e c l
r e
l o
l o c a
## END

#### export+=array disallowed (strict_array)

export e+=(e x)
echo "${e[@]}"

## status: 1
## STDOUT:
## END
## N-I bash status: 0
## N-I bash STDOUT:
e x
## END

#### Type mismatching of lhs+=rhs should not cause a crash
s=
a=()
declare -A d=([lemon]=yellow)

s+=(1)
s+=([melon]=green)

a+=lime
a+=([1]=banana)

d+=orange
d+=(0)

true

## STDOUT:
## END

