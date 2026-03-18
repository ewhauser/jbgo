## compare_shells: bash
## oils_failures_allowed: 1

# other ops.

#### String length
v=foo
echo ${#v}
## stdout: 3

#### Unicode string length (UTF-8)
v=$'_\u03bc_'
echo ${#v}
## stdout: 3

#### Unicode string length (spec/testdata/utf8-chars.txt)
v=$(cat $REPO_ROOT/spec/testdata/utf8-chars.txt)
echo ${#v}
## stdout: 7

#### String length with incomplete utf-8
for num_bytes in 0 1 2 3 4 5 6 7 8 9 10 11 12 13; do
  s=$(head -c $num_bytes $REPO_ROOT/spec/testdata/utf8-chars.txt)
  echo ${#s}
done 2> $TMP/err.txt

grep 'warning:' $TMP/err.txt
true  # exit 0

## STDOUT:
0
1
2
-1
3
4
-1
-1
5
6
-1
-1
-1
7
[ stdin ]:3: warning: UTF-8 decode: Truncated bytes at offset 2 in string of 3 bytes
[ stdin ]:3: warning: UTF-8 decode: Truncated bytes at offset 5 in string of 6 bytes
[ stdin ]:3: warning: UTF-8 decode: Truncated bytes at offset 5 in string of 7 bytes
[ stdin ]:3: warning: UTF-8 decode: Truncated bytes at offset 9 in string of 10 bytes
[ stdin ]:3: warning: UTF-8 decode: Truncated bytes at offset 9 in string of 11 bytes
[ stdin ]:3: warning: UTF-8 decode: Truncated bytes at offset 9 in string of 12 bytes
## END

#### String length with invalid utf-8 continuation bytes
for num_bytes in 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14; do
  s=$(head -c $num_bytes $REPO_ROOT/spec/testdata/utf8-chars.txt)$(echo -e "\xFF")
  echo ${#s}
done 2> $TMP/err.txt

grep 'warning:' $TMP/err.txt
true

## STDOUT:
-1
-1
-1
-1
-1
-1
-1
-1
-1
-1
-1
-1
-1
-1
-1
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 0 in string of 1 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 1 in string of 2 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 2 in string of 3 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 2 in string of 4 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 4 in string of 5 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 5 in string of 6 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 5 in string of 7 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 5 in string of 8 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 8 in string of 9 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 9 in string of 10 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 9 in string of 11 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 9 in string of 12 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 9 in string of 13 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 13 in string of 14 bytes
[ stdin ]:3: warning: UTF-8 decode: Bad encoding at offset 13 in string of 14 bytes
## END

#### Length of undefined variable
echo ${#undef}
## stdout: 0

#### Length of undefined variable with nounset
set -o nounset
echo ${#undef}
## status: 1

#### Length operator can't be followed by test operator
echo ${#x-default}

x=''
echo ${#x-default}

x='foo'
echo ${#x-default}

## status: 2
## stdout-json: ""

#### ${#s} respects LC_ALL - length in bytes or code points

# This test case is sorta "infected" because spec-common.sh sets LC_ALL=C.UTF-8
#
#
# See demo/04-unicode.sh

#echo $LC_ALL
unset LC_ALL 

# note: this may depend on the CI machine config
LANG=en_US.UTF-8

#LC_ALL=en_US.UTF-8

for s in $'\u03bc' $'\U00010000'; do
  LC_ALL=
  echo "len=${#s}"

  LC_ALL=C
  echo "len=${#s}"

  echo
done

## STDOUT:
len=1
len=2

len=1
len=4

## END

