# Test combination of var ops.
#
# NOTE: There are also slice tests in {array,arith-context}.test.sh.

## compare_shells: bash

#### String slice
foo=abcdefg
echo ${foo:1:3}
## STDOUT:
bcd
## END

#### Cannot take length of substring slice
# These are runtime errors, but we could make them parse time errors.
v=abcde
echo ${#v:1:3}
## status: 1

#### Out of range string slice: begin
# whole thing!
foo=abcdefg
echo _${foo:100:3}
echo $?
## STDOUT:
_
0
## END

#### Out of range string slice: length
foo=abcdefg
echo _${foo:3:100}
echo $?
## STDOUT:
_defg
0
## END

#### Negative start index
foo=abcdefg
echo ${foo: -4:3}
## stdout: def

#### Negative start index respects unicode
foo=abcd-μ-
echo ${foo: -4:3}
## stdout: d-μ

#### Negative second arg is position, not length!
foo=abcdefg
echo ${foo:3:-1} ${foo: 3: -2} ${foo:3 :-3 }
## stdout: def de d

#### Negative start index respects unicode
foo=abcd-μ-
echo ${foo: -5: -3}
## stdout: cd

#### String slice with math
# I think this is the $(()) language inside?
i=1
foo=abcdefg
echo ${foo: i+4-2 : i + 2}
## stdout: def

#### Slice undefined
echo -${undef:1:2}-
set -o nounset
echo -${undef:1:2}-
echo -done-
## STDOUT:
--
## END
## status: 1

#### Slice UTF-8 String
foo='--μ--'
echo ${foo:1:3}
## stdout: -μ-

#### Slice string with invalid UTF-8 results in empty string and warning
s=$(echo -e "\xFF")bcdef
echo -${s:1:3}-
## status: 0
## STDOUT:
--
## END
## STDERR:
[??? no location ???] warning: UTF-8 decode: Bad encoding at offset 0 in string of 6 bytes
## END

#### Slice string with invalid UTF-8 with strict_word_eval
echo slice
s=$(echo -e "\xFF")bcdef
echo -${s:1:3}-
## status: 1
## STDOUT: 
slice
## END

#### Slice with an index that's an array -- silent a[0] decay
i=(3 4 5)
mystr=abcdefg
echo assigned
echo ${mystr:$i:2}

## status: 0
## STDOUT:
assigned
de
## END

#### Slice with an assoc array
declare -A A=(['5']=3 ['6']=4)
mystr=abcdefg
echo assigned
echo ${mystr:$A:2}

## status: 0
## STDOUT:
assigned
ab
## END

#### Simple ${@:offset}

set -- 4 5 6

result=$(argv.sh ${@:0})
echo ${result//"$0"/'SHELL'}

argv.sh ${@:1}
argv.sh ${@:2}

## STDOUT:
['SHELL', '4', '5', '6']
['4', '5', '6']
['5', '6']
## END

#### ${@:offset} and ${*:offset}

argv.shell-name-checked () {
  argv.sh "${@//$0/SHELL}"
}
fun() {
  argv.shell-name-checked -${*:0}- # include $0
  argv.shell-name-checked -${*:1}- # from $1
  argv.shell-name-checked -${*:3}- # last parameter $3
  argv.shell-name-checked -${*:4}- # empty
  argv.shell-name-checked -${*:5}- # out of boundary
  argv.shell-name-checked -${@:0}-
  argv.shell-name-checked -${@:1}-
  argv.shell-name-checked -${@:3}-
  argv.shell-name-checked -${@:4}-
  argv.shell-name-checked -${@:5}-
  argv.shell-name-checked "-${*:0}-"
  argv.shell-name-checked "-${*:1}-"
  argv.shell-name-checked "-${*:3}-"
  argv.shell-name-checked "-${*:4}-"
  argv.shell-name-checked "-${*:5}-"
  argv.shell-name-checked "-${@:0}-"
  argv.shell-name-checked "-${@:1}-"
  argv.shell-name-checked "-${@:3}-"
  argv.shell-name-checked "-${@:4}-"
  argv.shell-name-checked "-${@:5}-"
}
fun "a 1" "b 2" "c 3"
## STDOUT:
['-SHELL', 'a', '1', 'b', '2', 'c', '3-']
['-a', '1', 'b', '2', 'c', '3-']
['-c', '3-']
['--']
['--']
['-SHELL', 'a', '1', 'b', '2', 'c', '3-']
['-a', '1', 'b', '2', 'c', '3-']
['-c', '3-']
['--']
['--']
['-SHELL a 1 b 2 c 3-']
['-a 1 b 2 c 3-']
['-c 3-']
['--']
['--']
['-SHELL', 'a 1', 'b 2', 'c 3-']
['-a 1', 'b 2', 'c 3-']
['-c 3-']
['--']
['--']
## END

#### ${@:offset:length} and ${*:offset:length}

argv.shell-name-checked () {
  argv.sh "${@//$0/SHELL}"
}
fun() {
  argv.shell-name-checked -${*:0:2}- # include $0
  argv.shell-name-checked -${*:1:2}- # from $1
  argv.shell-name-checked -${*:3:2}- # last parameter $3
  argv.shell-name-checked -${*:4:2}- # empty
  argv.shell-name-checked -${*:5:2}- # out of boundary
  argv.shell-name-checked -${@:0:2}-
  argv.shell-name-checked -${@:1:2}-
  argv.shell-name-checked -${@:3:2}-
  argv.shell-name-checked -${@:4:2}-
  argv.shell-name-checked -${@:5:2}-
  argv.shell-name-checked "-${*:0:2}-"
  argv.shell-name-checked "-${*:1:2}-"
  argv.shell-name-checked "-${*:3:2}-"
  argv.shell-name-checked "-${*:4:2}-"
  argv.shell-name-checked "-${*:5:2}-"
  argv.shell-name-checked "-${@:0:2}-"
  argv.shell-name-checked "-${@:1:2}-"
  argv.shell-name-checked "-${@:3:2}-"
  argv.shell-name-checked "-${@:4:2}-"
  argv.shell-name-checked "-${@:5:2}-"
}
fun "a 1" "b 2" "c 3"
## STDOUT:
['-SHELL', 'a', '1-']
['-a', '1', 'b', '2-']
['-c', '3-']
['--']
['--']
['-SHELL', 'a', '1-']
['-a', '1', 'b', '2-']
['-c', '3-']
['--']
['--']
['-SHELL a 1-']
['-a 1 b 2-']
['-c 3-']
['--']
['--']
['-SHELL', 'a 1-']
['-a 1', 'b 2-']
['-c 3-']
['--']
['--']
## END

#### ${@:0:1}
set a b c
result=$(echo ${@:0:1})
echo ${result//"$0"/'SHELL'}
## STDOUT:
SHELL
## END

#### Permutations of implicit begin and length
array=(1 2 3)

argv.sh ${array[@]}

# *** implicit length of N **
argv.sh ${array[@]:0}

# Why is this one not allowed
#argv.sh ${array[@]:}

# ** implicit length of ZERO **
#argv.sh ${array[@]::}
#argv.sh ${array[@]:0:}

argv.sh ${array[@]:0:0}
echo

# Same agreed upon permutations
set -- 1 2 3
argv.sh ${@}
argv.sh ${@:1}
argv.sh ${@:1:0}
echo

s='123'
argv.sh "${s}"
argv.sh "${s:0}"
argv.sh "${s:0:0}"

## STDOUT:
['1', '2', '3']
['1', '2', '3']
[]

['1', '2', '3']
['1', '2', '3']
[]

['123']
['123']
['']
## END

$SH -c 'array=(1 2 3); argv.sh ${array[@]:}'
$SH -c 'array=(1 2 3); argv.sh space ${array[@]: }'

$SH -c 's=123; argv.sh ${s:}'
$SH -c 's=123; argv.sh space ${s: }'

## STDOUT:
['space', '1', '2', '3']
['space', '123']
## END

#### ${array[@]::} has implicit length of zero - for ble.sh

# https://oilshell.zulipchat.com/#narrow/stream/121540-oil-discuss/topic/.24.7Barr.5B.40.5D.3A.3A.7D.20in.20bash.20-.20is.20it.20documented.3F

array=(1 2 3)
argv.sh ${array[@]::}
argv.sh ${array[@]:0:}

echo

set -- 1 2 3
argv.sh ${@::}
argv.sh ${@:0:}

## status: 0
## STDOUT:
[]
[]

[]
[]
## END

