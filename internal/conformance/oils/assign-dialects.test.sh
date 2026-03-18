## oils_failures_allowed: 3
## compare_shells: bash

# TODO: compare with AT&T ksh - it has this feature

#### K and V are variables in (( array[K] = V ))
K=5
V=42
typeset -a array
(( array[K] = V ))

echo array[5]=${array[5]}
echo keys = ${!array[@]}
echo values = ${array[@]}
## STDOUT:
array[5]=42
keys = 5
values = 42
## END

#### test -v with strings
test -v str
echo str=$?

str=x

test -v str
echo str=$?

## STDOUT:
str=1
str=0
## END

#### test -v with arrays

typeset -a a

test -v a
echo a=$?
test -v 'a[0]'
echo "a[0]=$?"
echo

a[0]=1

test -v a
echo a=$?
test -v 'a[0]'
echo "a[0]=$?"
echo

test -v 'a[1]'
echo "a[1]=$?"

# stupid rule about undefined 'x'
test -v 'a[x]'
echo "a[x]=$?"
echo

## STDOUT:
a=1
a[0]=1

a=0
a[0]=0

a[1]=1
a[x]=0

## END

#### test -v with assoc arrays

typeset -A A

test -v A
echo A=$?
test -v 'A[0]'
echo "A[0]=$?"
echo

A['0']=x

test -v A
echo A=$?
test -v 'A[0]'
echo "A[0]=$?"
echo

test -v 'A[1]'
echo "A[1]=$?"

# stupid rule about undefined 'x'
test -v 'A[x]'
echo "A[x]=$?"
echo

## STDOUT:
A=1
A[0]=1

A=0
A[0]=0

A[1]=1
A[x]=1

## END

## N-I ksh STDOUT:
A=1
A[0]=1

A=1
A[0]=1

A[1]=1
A[x]=1

## END

