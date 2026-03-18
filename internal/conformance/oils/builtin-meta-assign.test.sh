## oils_failures_allowed: 0
## compare_shells: bash

#### builtin declare a=(x y) is allowed

$SH -c 'declare a=(x y); declare -p a'
if test $? -ne 0; then
  echo 'fail'
fi

$SH -c 'builtin declare a=(x y); declare -p a'
if test $? -ne 0; then
  echo 'fail'
fi

$SH -c 'builtin declare -a a=(x y); declare -p a'
if test $? -ne 0; then
  echo 'fail'
fi

## BUG bash STDOUT:
declare -a a=([0]="x" [1]="y")
fail
fail
## END

## STDOUT:
declare -a a=(x y)
declare -a a=(x y)
declare -a a=(x y)
## END

#### command export,readonly

command export c=export
echo c=$c

command readonly c=readonly
echo c=$c

echo --

command command export cc=export
echo cc=$cc

command command readonly cc=readonly
echo cc=$cc

## STDOUT:
c=export
c=readonly
--
cc=export
cc=readonly
## END

#### command local

f() {
  command local s=local
  echo s=$s
}

f

## STDOUT:
s=local
## END

#### export, builtin export

x='a b'

export y=$x
echo $y

builtin export z=$x
echo $z

## STDOUT:
a b
a b
## END

#### \builtin declare - ble.sh relies on it

x='a b'

builtin declare c=$x
echo $c

\builtin declare d=$x
echo $d

'builtin' declare e=$x
echo $e

b=builtin
$b declare f=$x
echo $f

b=b
${b}uiltin declare g=$x
echo $g

## STDOUT:
a b
a b
a b
a b
a b
## END

## BUG bash STDOUT:
a
a
a
a
a
## END

#### \command readonly - similar issue

# \command readonly is equivalent to \builtin declare

x='a b'

readonly b=$x
echo $b

command readonly c=$x
echo $c

\command readonly d=$x
echo $d

'command' readonly e=$x
echo $e

# The issue here is that we have a heuristic in EvalWordSequence2:
# fs len(part_vals) == 1

## STDOUT:
a b
a b
a b
a b
## END

## BUG bash STDOUT:
a b
a
a
a
## END

x='a b'

z=command
$z readonly c=$x
echo $c

z=c
${z}ommand readonly d=$x
echo $d

## STDOUT:
a b
a b
## END

#### static builtin command ASSIGN, command builtin ASSIGN

builtin command export bc=export
echo bc=$bc

builtin command readonly bc=readonly
echo bc=$bc

echo --

command builtin export cb=export
echo cb=$cb

command builtin readonly cb=readonly
echo cb=$cb

## STDOUT:
bc=export
bc=readonly
--
cb=export
cb=readonly
## END

#### dynamic builtin command ASSIGN, command builtin ASSIGN

b=builtin
c=command
e=export
r=readonly

$b $c export bc=export
echo bc=$bc

$b $c readonly bc=readonly
echo bc=$bc

echo --

$c $b export cb=export
echo cb=$cb

$c $b readonly cb=readonly
echo cb=$cb

echo --

$b $c $e bce=export
echo bce=$bce

$b $c $r bcr=readonly
echo bcr=$bcr

echo --

$c $b $e cbe=export
echo cbe=$cbe

$c $b $r cbr=readonly
echo cbr=$cbr

## STDOUT:
bc=export
bc=readonly
--
cb=export
cb=readonly
--
bce=export
bcr=readonly
--
cbe=export
cbr=readonly
## END

#### builtin typeset, export,readonly

builtin typeset s=typeset
echo s=$s

builtin export s=export
echo s=$s

builtin readonly s=readonly
echo s=$s

echo --

builtin builtin typeset s2=typeset
echo s2=$s2

builtin builtin export s2=export
echo s2=$s2

builtin builtin readonly s2=readonly
echo s2=$s2

## STDOUT:
s=typeset
s=export
s=readonly
--
s2=typeset
s2=export
s2=readonly
## END

#### builtin declare,local

builtin declare s=declare
echo s=$s

f() {
  builtin local s=local
  echo s=$s
}

f

## STDOUT:
s=declare
s=local
## END

