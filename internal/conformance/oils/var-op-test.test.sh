## compare_shells: bash
## oils_failures_allowed: 0

#### Lazy Evaluation of Alternative
i=0
x=x
echo ${x:-$((i++))}
echo $i
echo ${undefined:-$((i++))}
echo $i  # i is one because the alternative was only evaluated once
## status: 0
## STDOUT:
x
0
0
1
## END

#### Default value when empty
empty=''
echo ${empty:-is empty}
## stdout: is empty

#### Default value when unset
echo ${unset-is unset}
## stdout: is unset

#### Unquoted with array as default value
set -- '1 2' '3 4'
argv.sh X${unset=x"$@"x}X
argv.sh X${unset=x$@x}X
## STDOUT:
['Xx1', '2', '3', '4xX']
['Xx1', '2', '3', '4xX']
## END

#### Quoted with array as default value
set -- '1 2' '3 4'
argv.sh "X${unset=x"$@"x}X"
argv.sh "X${unset=x$@x}X"
## STDOUT:
['Xx1 2 3 4xX']
['Xx1 2 3 4xX']
## END

# Bash 4.2..4.4 had a bug. This was fixed in Bash 5.0.
#
# ## BUG bash STDOUT:
# ['Xx1', '2', '3', '4xX']
# ['Xx1 2 3 4xX']
# ## END

#### Assign default with array
set -- '1 2' '3 4'
argv.sh X${unset=x"$@"x}X
argv.sh "$unset"
## STDOUT:
['Xx1', '2', '3', '4xX']
['x1 2 3 4x']
## END

#### Assign default value when empty
empty=''
${empty:=is empty}
echo $empty
## stdout: is empty

#### Assign default value when unset
${unset=is unset}
echo $unset
## stdout: is unset

#### ${v:+foo} Alternative value when empty
v=foo
empty=''
echo ${v:+v is not empty} ${empty:+is not empty}
## stdout: v is not empty

#### ${v+foo} Alternative value when unset
v=foo
echo ${v+v is not unset} ${unset:+is not unset}
## stdout: v is not unset

#### "${x+foo}" quoted (regression)
# Python's configure caught this
argv.sh "${with_icc+set}" = set
## STDOUT:
['', '=', 'set']
## END

#### ${s+foo} and ${s:+foo} when set -u
set -u
v=v
echo v=${v:+foo}
echo v=${v+foo}
unset v
echo v=${v:+foo}
echo v=${v+foo}
## STDOUT:
v=foo
v=foo
v=
v=
## END

#### "${array[@]} with set -u (bash is outlier)

set -u

typeset -a empty
empty=()

echo empty /"${empty[@]}"/
echo undefined /"${undefined[@]}"/

## status: 1
## STDOUT:
empty //
## END

## BUG bash status: 0
## BUG bash STDOUT:
empty //
undefined //
## END

#### "${undefined[@]+foo}" and "${undefined[@]:+foo}", with set -u

set -u

echo plus /"${array[@]+foo}"/
echo plus colon /"${array[@]:+foo}"/

## STDOUT:
plus //
plus colon //
## END

#### "${a[@]+foo}" and "${a[@]:+foo}" - operators are equivalent on arrays?

echo '+ ' /"${array[@]+foo}"/
echo '+:' /"${array[@]:+foo}"/
echo

typeset -a array
array=()

echo '+ ' /"${array[@]+foo}"/
echo '+:' /"${array[@]:+foo}"/
echo

array=('')

echo '+ ' /"${array[@]+foo}"/
echo '+:' /"${array[@]:+foo}"/
echo

array=(spam eggs)

echo '+ ' /"${array[@]+foo}"/
echo '+:' /"${array[@]:+foo}"/
echo

# Bash 2.0..4.4 has a bug that "${a[@]:-xxx}" produces an empty string.  It
# seemed to consider a[@] and a[*] are non-empty when there is at least one
# element even if the element is empty.  This was fixed in Bash 5.0.
#
# ## BUG bash STDOUT:
# +  //
# +: //
#
# +  //
# +: //
#
# +  /foo/
# +: /foo/
#
# +  /foo/
# +: /foo/
#
# ## END

#### Nix idiom ${!hooksSlice+"${!hooksSlice}"} - was workaround for obsolete bash 4.3 bug

# https://oilshell.zulipchat.com/#narrow/stream/307442-nix/topic/Replacing.20bash.20with.20osh.20in.20Nixpkgs.20stdenv

(argv.sh ${!hooksSlice+"${!hooksSlice}"})

hooksSlice=x

argv.sh ${!hooksSlice+"${!hooksSlice}"}

declare -a hookSlice=()

argv.sh ${!hooksSlice+"${!hooksSlice}"}

foo=42
bar=43

declare -a hooksSlice=(foo bar spam eggs)

argv.sh ${!hooksSlice+"${!hooksSlice}"}

## STDOUT:
[]
[]
['42']
## END

# Bash 4.4 has a bug that ${!undef-} successfully generates an empty word.
#
# ## BUG bash STDOUT:
# []
# []
# []
# ['42']
# ## END

#### ${v-foo} and ${v:-foo} when set -u
set -u
v=v
echo v=${v:-foo}
echo v=${v-foo}
unset v
echo v=${v:-foo}
echo v=${v-foo}
## STDOUT:
v=v
v=v
v=foo
v=foo
## END

#### array and - and +

empty=()
a1=('')
a2=('' x)
a3=(3 4)
echo empty=${empty[@]-minus}
echo a1=${a1[@]-minus}
echo a1[0]=${a1[0]-minus}
echo a2=${a2[@]-minus}
echo a3=${a3[@]-minus}
echo ---

echo empty=${empty[@]+plus}
echo a1=${a1[@]+plus}
echo a1[0]=${a1[0]+plus}
echo a2=${a2[@]+plus}
echo a3=${a3[@]+plus}
echo ---

echo empty=${empty+plus}
echo a1=${a1+plus}
echo a2=${a2+plus}
echo a3=${a3+plus}
echo ---

# Test quoted arrays too
argv.sh "${empty[@]-minus}"
argv.sh "${empty[@]+plus}"
argv.sh "${a1[@]-minus}"
argv.sh "${a1[@]+plus}"
argv.sh "${a1[0]-minus}"
argv.sh "${a1[0]+plus}"
argv.sh "${a2[@]-minus}"
argv.sh "${a2[@]+plus}"
argv.sh "${a3[@]-minus}"
argv.sh "${a3[@]+plus}"

## STDOUT:
empty=minus
a1=
a1[0]=
a2= x
a3=3 4
---
empty=
a1=plus
a1[0]=plus
a2=plus
a3=plus
---
empty=
a1=plus
a2=plus
a3=plus
---
['minus']
[]
['']
['plus']
['']
['plus']
['', 'x']
['plus']
['3', '4']
['plus']
## END

#### $@ (empty) and - and +
echo argv=${@-minus}
echo argv=${@+plus}
echo argv=${@:-minus}
echo argv=${@:+plus}
## STDOUT:
argv=minus
argv=
argv=minus
argv=
## END

#### $@ ("") and - and +
set -- ""
echo argv=${@-minus}
echo argv=${@+plus}
echo argv=${@:-minus}
echo argv=${@:+plus}
## STDOUT:
argv=
argv=plus
argv=minus
argv=
## END

# with a space.

#### $@ ("" "") and - and +
set -- "" ""
echo argv=${@-minus}
echo argv=${@+plus}
echo argv=${@:-minus}
echo argv=${@:+plus}
## STDOUT:
argv=
argv=plus
argv=
argv=plus
## END

#### $* ("" "") and - and + (IFS=)
set -- "" ""
IFS=
echo argv=${*-minus}
echo argv=${*+plus}
echo argv=${*:-minus}
echo argv=${*:+plus}
## STDOUT:
argv=
argv=plus
argv=
argv=plus
## END

#### "$*" ("" "") and - and + (IFS=)
set -- "" ""
IFS=
echo "argv=${*-minus}"
echo "argv=${*+plus}"
echo "argv=${*:-minus}"
echo "argv=${*:+plus}"
## STDOUT:
argv=
argv=plus
argv=minus
argv=
## END

#### assoc array and - and +

declare -A empty=()
declare -A assoc=(['k']=v)

echo empty=${empty[@]-minus}
echo empty=${empty[@]+plus}
echo assoc=${assoc[@]-minus}
echo assoc=${assoc[@]+plus}

echo ---
echo empty=${empty[@]:-minus}
echo empty=${empty[@]:+plus}
echo assoc=${assoc[@]:-minus}
echo assoc=${assoc[@]:+plus}
## STDOUT:
empty=minus
empty=
assoc=v
assoc=plus
---
empty=minus
empty=
assoc=v
assoc=plus
## END

#### Error when empty
empty=''
echo ${empty:?'is em'pty}  # test eval of error
echo should not get here
## stdout-json: ""
## status: 1

#### Error when unset
echo ${unset?is empty}
echo should not get here
## stdout-json: ""
## status: 1

#### Error when unset
v=foo
echo ${v+v is not unset} ${unset:+is not unset}
## stdout: v is not unset

#### ${var=x} dynamic scope
f() { : "${hello:=x}"; echo $hello; }
f
echo hello=$hello

f() { hello=x; }
f
echo hello=$hello
## STDOUT:
x
hello=x
hello=x
## END

#### array ${arr[0]=x}
arr=()
echo ${#arr[@]}
: ${arr[0]=x}
echo ${#arr[@]}
## STDOUT:
0
1
## END

#### assoc array ${arr["k"]=x}

declare -A arr=()
echo ${#arr[@]}
: ${arr['k']=x}
echo ${#arr[@]}
## STDOUT:
0
1
## END

#### "\z" as arg
echo "${undef-\$}"
echo "${undef-\(}"
echo "${undef-\z}"
echo "${undef-\"}"
echo "${undef-\`}"
echo "${undef-\\}"
## STDOUT:
$
\(
\z
"
`
\
## END
# Note: this line terminates the quoting by ` not to confuse the text editor.

#### "\e" as arg
echo "${undef-\e}"
## STDOUT:
\e
## END

#### op-test for ${a} and ${a[0]}

test-hyphen() {
  echo "a   : '${a-no-colon}' '${a:-with-colon}'"
  echo "a[0]: '${a[0]-no-colon}' '${a[0]:-with-colon}'"
}

a=()
test-hyphen
a=("")
test-hyphen
a=("" "")
test-hyphen
IFS=
test-hyphen

## STDOUT:
a   : 'no-colon' 'with-colon'
a[0]: 'no-colon' 'with-colon'
a   : '' 'with-colon'
a[0]: '' 'with-colon'
a   : '' 'with-colon'
a[0]: '' 'with-colon'
a   : '' 'with-colon'
a[0]: '' 'with-colon'
## END

## END:

#### op-test for ${a[@]} and ${a[*]}

test-hyphen() {
  echo "a[@]: '${a[@]-no-colon}' '${a[@]:-with-colon}'"
  echo "a[*]: '${a[*]-no-colon}' '${a[*]:-with-colon}'"
}

a=()
test-hyphen
a=("")
test-hyphen
a=("" "")
test-hyphen
IFS=
test-hyphen

## STDOUT:
a[@]: 'no-colon' 'with-colon'
a[*]: 'no-colon' 'with-colon'
a[@]: '' 'with-colon'
a[*]: '' 'with-colon'
a[@]: ' ' ' '
a[*]: ' ' ' '
a[@]: ' ' ' '
a[*]: '' 'with-colon'
## END

# Bash 2.0..4.4 has a bug that "${a[@]:-xxx}" produces an empty string.  It
# seemed to consider a[@] and a[*] are non-empty when there is at least one
# element even if the element is empty.  This was fixed in Bash 5.0.
#
# ## BUG bash STDOUT:
# a[@]: 'no-colon' 'with-colon'
# a[*]: 'no-colon' 'with-colon'
# a[@]: '' ''
# a[*]: '' ''
# a[@]: ' ' ' '
# a[*]: ' ' ' '
# a[@]: ' ' ' '
# a[*]: '' ''
# ## END

## END:

#### op-test for ${!array} with array="a" and array="a[0]"

test-hyphen() {
  ref='a'
  echo "ref=a   : '${!ref-no-colon}' '${!ref:-with-colon}'"
  ref='a[0]'
  echo "ref=a[0]: '${!ref-no-colon}' '${!ref:-with-colon}'"
}

a=()
test-hyphen
a=("")
test-hyphen
a=("" "")
test-hyphen
IFS=
test-hyphen

## STDOUT:
ref=a   : 'no-colon' 'with-colon'
ref=a[0]: 'no-colon' 'with-colon'
ref=a   : '' 'with-colon'
ref=a[0]: '' 'with-colon'
ref=a   : '' 'with-colon'
ref=a[0]: '' 'with-colon'
ref=a   : '' 'with-colon'
ref=a[0]: '' 'with-colon'
## END

## END:

#### op-test for ${!array} with array="a[@]" or array="a[*]"

test-hyphen() {
  ref='a[@]'
  echo "ref=a[@]: '${!ref-no-colon}' '${!ref:-with-colon}'"
  ref='a[*]'
  echo "ref=a[*]: '${!ref-no-colon}' '${!ref:-with-colon}'"
}

a=()
test-hyphen
a=("")
test-hyphen
a=("" "")
test-hyphen
IFS=
test-hyphen

## STDOUT:
ref=a[@]: 'no-colon' 'with-colon'
ref=a[*]: 'no-colon' 'with-colon'
ref=a[@]: '' 'with-colon'
ref=a[*]: '' 'with-colon'
ref=a[@]: ' ' ' '
ref=a[*]: ' ' ' '
ref=a[@]: ' ' ' '
ref=a[*]: '' 'with-colon'
## END

## BUG bash STDOUT:
ref=a[@]: 'no-colon' 'with-colon'
ref=a[*]: 'no-colon' 'with-colon'
ref=a[@]: '' ''
ref=a[*]: '' ''
ref=a[@]: ' ' ' '
ref=a[*]: ' ' ' '
ref=a[@]: ' ' ' '
ref=a[*]: '' ''
## END

## END:

#### op-test for unquoted ${a[*]:-empty} with IFS=

IFS=
a=("" "")
argv.sh ${a[*]:-empty}

## STDOUT:
[]
## END

## END:
