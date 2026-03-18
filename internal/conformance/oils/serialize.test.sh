## compare_shells: bash

# TODO: fix J8 bug causing failure

# Cross-cutting test of serialization formats.  That is, what J8 Notation
# should fix.
#
# TODO: Also see spec/xtrace for another use case.

#### printf %q newline

newline=$'one\ntwo'
printf '%q\n' "$newline"

quoted="$(printf '%q\n' "$newline")"
restored=$(eval "echo $quoted")
test "$newline" = "$restored" && echo roundtrip-ok

## STDOUT:
$'one\ntwo'
roundtrip-ok
## END

#### printf %q spaces

# bash does a weird thing and uses \

spaces='one two'
printf '%q\n' "$spaces"

## STDOUT:
'one two'
## END

#### printf %q quotes

quotes=\'\"
printf '%q\n' "$quotes"

quoted="$(printf '%q\n' "$quotes")"
restored=$(eval "echo $quoted")
test "$quotes" = "$restored" && echo roundtrip-ok

## STDOUT:
\'\"
roundtrip-ok
## END

#### printf %q unprintable

unprintable=$'\xff'
printf '%q\n' "$unprintable"

## STDOUT:
$'\377'
## END

#### printf %q unicode

unicode=$'\u03bc'
unicode=$'\xce\xbc'  # does the same thing

printf '%q\n' "$unicode"

## STDOUT:
μ
## END

#### printf %q invalid unicode

# recovery!  inspecting the bash source seems to confirm this.
unicode=$'\xce'
printf '%q\n' "$unicode"

unicode=$'\xce\xce\xbc'
printf '%q\n' "$unicode"

unicode=$'\xce\xbc\xce'
printf '%q\n' "$unicode"

unicode=$'\xcea'
printf '%q\n' "$unicode"
unicode=$'a\xce'
printf '%q\n' "$unicode"
## STDOUT:
$'\xce'
$'\xceμ'
$'μ\xce'
$'\xcea'
$'a\xce'
## END
## OK bash STDOUT:
$'\316'
$'\316μ'
$'μ\316'
$'\316a'
$'a\316'
## END

#### set

zz=$'one\ntwo'

set | grep zz
## STDOUT:
zz=$'one\ntwo'
## END

#### declare

zz=$'one\ntwo'

typeset | grep zz
typeset -p zz

## STDOUT:
zz=$'one\ntwo'
declare -- zz=$'one\ntwo'
## END

#### ${var@Q}

zz=$'one\ntwo \u03bc'

# weirdly, quoted and unquoted aren't different
echo ${zz@Q}
echo "${zz@Q}"
## STDOUT:
$'one\ntwo μ'
$'one\ntwo μ'
## END

#### xtrace
zz=$'one\ntwo'
set -x
echo "$zz"
## STDOUT:
one
two
## END
## STDERR:
+ echo $'one\ntwo'
## END

