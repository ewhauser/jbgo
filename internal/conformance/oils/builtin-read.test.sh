## oils_failures_allowed: 1
## compare_shells: bash

#### read line from here doc

# NOTE: there are TABS below
read x <<EOF
A		B C D E
FG
EOF
echo "[$x]"
## stdout: [A		B C D E]
## status: 0

#### read from empty file
echo -n '' > $TMP/empty.txt
read x < $TMP/empty.txt
argv.sh "status=$?" "$x"

# No variable name, behaves the same
read < $TMP/empty.txt
argv.sh "status=$?" "$REPLY"

## STDOUT:
['status=1', '']
['status=1', '']
## END
## status: 0

#### read /dev/null
read -n 1 </dev/null
echo $?
## STDOUT:
1
## END

#### read with zero args
echo | read
echo status=$?
## STDOUT:
status=0
## END

#### read builtin with no newline returns status 1

# need a separate put reading feature that doesn't use IFS.

echo -n ZZZ | { read x; echo status=$?; echo $x; }

## STDOUT:
status=1
ZZZ
## END
## status: 0

#### read builtin splits value across multiple vars
# NOTE: there are TABS below
read x y z <<EOF
A		B C D E 
FG
EOF
echo "[$x/$y/$z]"
## stdout: [A/B/C D E]
## status: 0

#### read builtin with too few variables
set -o errexit
set -o nounset  # hm this doesn't change it
read x y z <<EOF
A B
EOF
echo /$x/$y/$z/
## stdout: /A/B//
## status: 0

#### read -n (with $REPLY)
echo 12345 > $TMP/readn.txt
read -n 4 x < $TMP/readn.txt
read -n 2 < $TMP/readn.txt  # Do it again with no variable
argv.sh $x $REPLY
## stdout: ['1234', '12']

echo XYZ > "$TMP/readn.txt"
IFS= TMOUT= read -n 1 char < "$TMP/readn.txt"
argv.sh "$char"
## stdout: ['X']

#### read -n doesn't strip whitespace (bug fix)

echo '  a b  ' | (read -n 4; echo "[$REPLY]")
echo '  a b  ' | (read -n 5; echo "[$REPLY]")
echo '  a b  ' | (read -n 6; echo "[$REPLY]")
echo

echo 'one var strips whitespace'
echo '  a b  ' | (read -n 4 myvar; echo "[$myvar]")
echo '  a b  ' | (read -n 5 myvar; echo "[$myvar]")
echo '  a b  ' | (read -n 6 myvar; echo "[$myvar]")
echo

echo 'three vars'
echo '  a b  ' | (read -n 4 x y z; echo "[$x] [$y] [$z]")
echo '  a b  ' | (read -n 5 x y z; echo "[$x] [$y] [$z]")
echo '  a b  ' | (read -n 6 x y z; echo "[$x] [$y] [$z]")

## STDOUT:
[  a ]
[  a b]
[  a b ]

one var strips whitespace
[a]
[a b]
[a b]

three vars
[a] [] []
[a] [b] []
[a] [b] []
## END

#### read -d -n - respects delimiter and splits

echo 'delim c'
echo '  a b c ' | (read -d 'c' -n 3; echo "[$REPLY]")
echo '  a b c ' | (read -d 'c' -n 4; echo "[$REPLY]")
echo '  a b c ' | (read -d 'c' -n 5; echo "[$REPLY]")
echo

echo 'one var'
echo '  a b c ' | (read -d 'c' -n 3 myvar; echo "[$myvar]")
echo '  a b c ' | (read -d 'c' -n 4 myvar; echo "[$myvar]")
echo '  a b c ' | (read -d 'c' -n 5 myvar; echo "[$myvar]")
echo

echo 'three vars'
echo '  a b c ' | (read -d 'c' -n 3 x y z; echo "[$x] [$y] [$z]")
echo '  a b c ' | (read -d 'c' -n 4 x y z; echo "[$x] [$y] [$z]")
echo '  a b c ' | (read -d 'c' -n 5 x y z; echo "[$x] [$y] [$z]")

## STDOUT:
delim c
[  a]
[  a ]
[  a b]

one var
[a]
[a]
[a b]

three vars
[a] [] []
[a] [] []
[a] [b] []
## END

#### read -n with invalid arg
read -n not_a_number
echo status=$?
## stdout: status=2
## OK bash stdout: status=1

#### read -n from pipe

echo abcxyz | { read -n 3; echo reply=$REPLY; }
## status: 0
## stdout: reply=abc

#### read without args uses $REPLY, no splitting occurs (without -n)

echo '  a b  ' | (read; echo "[$REPLY]")
echo '  a b  ' | (read myvar; echo "[$myvar]")

echo '  a b  \
  line2' | (read; echo "[$REPLY]")
echo '  a b  \
  line2' | (read myvar; echo "[$myvar]")

# Now test with -r
echo '  a b  \
  line2' | (read -r; echo "[$REPLY]")
echo '  a b  \
  line2' | (read -r myvar; echo "[$myvar]")

## STDOUT:
[  a b  ]
[a b]
[  a b    line2]
[a b    line2]
[  a b  \]
[a b  \]
## END

#### read -n vs. -N

# bash docs: https://www.gnu.org/software/bash/manual/html_node/Bash-Builtins.html

echo 'a b c' > $TMP/readn.txt

echo 'read -n'
read -n 5 A B C < $TMP/readn.txt; echo "'$A' '$B' '$C'"
read -n 4 A B C < $TMP/readn.txt; echo "'$A' '$B' '$C'"
echo

echo 'read -N'
read -N 5 A B C < $TMP/readn.txt; echo "'$A' '$B' '$C'"
read -N 4 A B C < $TMP/readn.txt; echo "'$A' '$B' '$C'"
## STDOUT:
read -n
'a' 'b' 'c'
'a' 'b' ''

read -N
'a b c' '' ''
'a b ' '' ''
## END

#### read -N ignores delimiters

echo $'a\nb\nc' > $TMP/read-lines.txt

read -N 3 out < $TMP/read-lines.txt
echo "$out"
## STDOUT:
a
b
## END

#### read will unset extranous vars

echo 'a b' > $TMP/read-few.txt

c='some value'
read a b c < $TMP/read-few.txt
echo "'$a' '$b' '$c'"

c='some value'
read -n 3 a b c < $TMP/read-few.txt
echo "'$a' '$b' '$c'"
## STDOUT:
'a' 'b' ''
'a' 'b' ''
## END

#### read -r ignores backslashes
echo 'one\ two' > $TMP/readr.txt
read escaped < $TMP/readr.txt
read -r raw < $TMP/readr.txt
argv.sh "$escaped" "$raw"
## stdout: ['one two', 'one\\ two']

#### read -r with other backslash escapes
echo 'one\ two\x65three' > $TMP/readr.txt
read escaped < $TMP/readr.txt
read -r raw < $TMP/readr.txt
argv.sh "$escaped" "$raw"
## stdout: ['one twox65three', 'one\\ two\\x65three']

#### read with line continuation reads multiple physical lines
tmp=$TMP/$(basename $SH)-readr.txt
echo -e 'one\\\ntwo\n' > $tmp
read escaped < $tmp
read -r raw < $tmp
argv.sh "$escaped" "$raw"
## stdout: ['onetwo', 'one\\']

#### read multiple vars spanning many lines
read x y << 'EOF'
one-\
two three-\
four five-\
six
EOF
argv.sh "$x" "$y" "$z"
## stdout: ['one-two', 'three-four five-six', '']

#### read -r with \n
echo '\nline' > $TMP/readr.txt
read escaped < $TMP/readr.txt
read -r raw < $TMP/readr.txt
argv.sh "$escaped" "$raw"
# literal \n.
## stdout: ['nline', '\\nline']

#### read -s from pipe, not a terminal

# It's hard to really test this because it requires a terminal.  We hit a
# different code path when reading through a pipe.  There can be bugs there
# too!

echo foo | { read -s; echo $REPLY; }
echo bar | { read -n 2 -s; echo $REPLY; }

# Hm no exit 1 here?  Weird
echo b | { read -n 2 -s; echo $?; echo $REPLY; }
## STDOUT:
foo
ba
0
b
## END

#### read with IFS=$'\n'
# The leading spaces are stripped if they appear in IFS.
IFS=$(echo -e '\n')
read var <<EOF
  a b c
  d e f
EOF
echo "[$var]"
## stdout: [  a b c]

#### read multiple lines with IFS=:
# The leading spaces are stripped if they appear in IFS.
# IFS chars are escaped with :.
tmp=$TMP/$(basename $SH)-read-ifs.txt
IFS=:
cat >$tmp <<'EOF'
  \\a :b\: c:d\
  e
EOF
read a b c d < $tmp
# bash.
printf "%s\n" "[$a|$b|$c|$d]"
## stdout: [  \a |b: c|d  e|]

#### read with IFS=''
IFS=''
read x y <<EOF
  a b c d
EOF
echo "[$x|$y]"
## stdout: [  a b c d|]

#### read does not respect C backslash escapes

# bash doesn't respect these, but other shells do.  Gah!  I think bash
# behavior makes more sense.  It only escapes IFS.
echo '\a \b \c \d \e \f \g \h \x65 \145 \i' > $TMP/read-c.txt
read line < $TMP/read-c.txt
echo $line
## STDOUT:
a b c d e f g h x65 145 i
## END

#### dynamic scope used to set vars
f() {
  read head << EOF
ref: refs/heads/dev/andy
EOF
}
f
echo $head
## STDOUT:
ref: refs/heads/dev/andy
## END

#### read -a reads into array

# read -a is used in bash-completion
# none of these shells implement it

read -a myarray <<'EOF'
a b c\ d
EOF
argv.sh "${myarray[@]}"

# arguments are ignored here
read -r -a array2 extra arguments <<'EOF'
a b c\ d
EOF
argv.sh "${array2[@]}"
argv.sh "${extra[@]}"
argv.sh "${arguments[@]}"
## status: 0
## STDOUT:
['a', 'b', 'c d']
['a', 'b', 'c\\', 'd']
[]
[]
## END

#### read -d : (colon-separated records)
printf a,b,c:d,e,f:g,h,i | {
  IFS=,
  read -d : v1
  echo "v1=$v1"
  read -d : v1 v2
  echo "v1=$v1 v2=$v2"
  read -d : v1 v2 v3
  echo "v1=$v1 v2=$v2 v3=$v3"
}
## STDOUT:
v1=a,b,c
v1=d v2=e,f
v1=g v2=h v3=i
## END

#### read -d '' (null-separated records)
printf 'a,b,c\0d,e,f\0g,h,i' | {
  IFS=,
  read -d '' v1
  echo "v1=$v1"
  read -d '' v1 v2
  echo "v1=$v1 v2=$v2"
  read -d '' v1 v2 v3
  echo "v1=$v1 v2=$v2 v3=$v3"
}
## STDOUT:
v1=a,b,c
v1=d v2=e,f
v1=g v2=h v3=i
## END

#### read -rd
read -rd '' var <<EOF
foo
bar
EOF
echo "$var"
## STDOUT:
foo
bar
## END

#### read -d when there's no delimiter
{ read -d : part
  echo $part $?
  read -d : part
  echo $part $?
} <<EOF
foo:bar
EOF
## STDOUT:
foo 0
bar 1
## END

#### read -t 0 tests if input is available

# is there input available?
read -t 0 < /dev/null
echo $?

# floating point
read -t 0.0 < /dev/null
echo $?

# floating point
echo foo | { read -t 0; echo reply=$REPLY; }
echo $?

## STDOUT:
0
0
reply=
0
## END

#### read -t 0.5

read -t 0.5 < /dev/null
echo $?

## STDOUT:
1
## END

#### read -t -0.5 is invalid
# bash appears to just take the absolute value?

read -t -0.5 < /dev/null
echo $?

## STDOUT:
2
## END
## BUG bash STDOUT:
1
## END

#### read -u

# file descriptor
read -u 3 3<<EOF
hi
EOF
echo reply=$REPLY
## STDOUT:
reply=hi
## END

#### read -u syntax error
read -u -3
echo status=$?
## STDOUT:
status=2
## END

#### read -u -s

# file descriptor
read -s -u 3 3<<EOF
hi
EOF
echo reply=$REPLY
## STDOUT:
reply=hi
## END

#### read -u 3 -d 5

# file descriptor
read -u 3 -d 5 3<<EOF
123456789
EOF
echo reply=$REPLY
## STDOUT:
reply=1234
## END

#### read -u 3 -d b -N 6

# file descriptor
read -u 3 -d b -N 4 3<<EOF
ababababa
EOF
echo reply=$REPLY
# test end on EOF
read -u 3 -d b -N 6 3<<EOF
ab
EOF
echo reply=$REPLY
## STDOUT:
reply=abab
reply=ab
## END

#### read -N doesn't respect delimiter, while read -n does

echo foobar | { read -n 5 -d b; echo $REPLY; }
echo foobar | { read -N 5 -d b; echo $REPLY; }
## STDOUT:
foo
fooba
## END

#### read -p (not fully tested)

# hm DISABLED if we're not going to the terminal
# so we're only testing that it accepts the flag here

echo hi | { read -p 'P'; echo $REPLY; }
echo hi | { read -p 'P' -n 1; echo $REPLY; }
## STDOUT:
hi
h
## END
## stderr-json: ""

#### read usage
read -n -1
echo status=$?
## STDOUT:
status=2
## END
## OK bash stdout: status=1

#### read with smooshed args
echo hi | { read -rn1 var; echo var=$var; }
## STDOUT:
var=h
## END

#### read -r -d '' for NUL strings, e.g. find -print0

mkdir -p read0
cd read0
rm -f *

touch a\\b\\c\\d  # -r is necessary!

find . -type f -a -print0 | { read -r -d ''; echo "[$REPLY]"; }

## STDOUT:
[./a\b\c\d]
## END
## N-I dash/zsh/mksh STDOUT:
## END

#### while IFS= read -r line || [[ -n $line ]]
printf '%s' $'one\nlast line without newline' > "$TMP/read-loop.txt"
while IFS= read -r line || [[ -n $line ]]; do
  printf '<%s>\n' "$line"
done < "$TMP/read-loop.txt"

#### while IFS= read -r -d '' item || [[ -n $item ]]
printf '%s\0%s' alpha beta > "$TMP/read-loop-nul.txt"
while IFS= read -r -d '' item || [[ -n $item ]]; do
  printf '<%s>\n' "$item"
done < "$TMP/read-loop-nul.txt"

#### read from redirected directory is non-fatal error

# version and enable this

cd $TMP
mkdir -p dir
read x < ./dir
echo status=$?

## STDOUT:
status=1
## END

#### read -n from directory

# same hanging bug

mkdir -p dir
read -n 3 x < ./dir
echo status=$?
## STDOUT:
status=1
## END

#### mapfile from directory (bash doesn't handle errors)

mkdir -p dir
mapfile $x < ./dir
echo status=$?

## STDOUT:
status=1
## END
## BUG bash STDOUT:
status=0
## END

#### read -n 0

echo 'a\b\c\d\e\f' | (read -n 0; argv.sh "$REPLY")

## STDOUT:
['']
## END

#### read -n and backslash escape

echo 'a\b\c\d\e\f' | (read -n 5; argv.sh "$REPLY")
echo 'a\ \ \ \ \ ' | (read -n 5; argv.sh "$REPLY")

## STDOUT:
['abcde']
['a    ']
## END

#### read -n 4 with incomplete backslash

echo 'abc\def\ghijklmn' | (read -n 4; argv.sh "$REPLY")
echo '   \xxx\xxxxxxxx' | (read -n 4; argv.sh "$REPLY")

# bash implements "-n NUM" as number of characters
## STDOUT:
['abcd']
['   x']
## END

#### read -n 4 with backslash + delim

echo $'abc\\\ndefg' | (read -n 4; argv.sh "$REPLY")

## STDOUT:
['abcd']
## END

#### "backslash + newline" should be swallowed regardless of "-d <delim>"

printf '%s\n' 'a b\' 'c d' | (read; argv.sh "$REPLY")
printf '%s\n' 'a b\,c d'   | (read; argv.sh "$REPLY")
printf '%s\n' 'a b\' 'c d' | (read -d ,; argv.sh "$REPLY")
printf '%s\n' 'a b\,c d'   | (read -d ,; argv.sh "$REPLY")

## STDOUT:
['a bc d']
['a b,c d']
['a bc d\n']
['a b,c d\n']
## END

#### empty input and splitting

echo '' | (read -a a; argv.sh "${a[@]}")
IFS=x
echo '' | (read -a a; argv.sh "${a[@]}")
IFS=
echo '' | (read -a a; argv.sh "${a[@]}")
## STDOUT:
[]
[]
[]
## END

#### IFS='x ' read -a: trailing spaces (unlimited split)

IFS='x '
echo 'a b'     | (read -a a; argv.sh "${a[@]}")
echo 'a b '    | (read -a a; argv.sh "${a[@]}")
echo 'a bx'    | (read -a a; argv.sh "${a[@]}")
echo 'a bx '   | (read -a a; argv.sh "${a[@]}")
echo 'a b x'   | (read -a a; argv.sh "${a[@]}")
echo 'a b x '  | (read -a a; argv.sh "${a[@]}")
echo 'a b x x' | (read -a a; argv.sh "${a[@]}")

## STDOUT:
['a', 'b']
['a', 'b']
['a', 'b']
['a', 'b']
['a', 'b']
['a', 'b']
['a', 'b', '']
## END

#### IFS='x ' read a b: trailing spaces (with max_split)
echo 'hello world  test   ' | (read a b; argv.sh "$a" "$b")
echo '-- IFS=x --'
IFS='x '
echo 'a ax  x  '     | (read a b; argv.sh "$a" "$b")
echo 'a ax  x  x'    | (read a b; argv.sh "$a" "$b")
echo 'a ax  x  x  '  | (read a b; argv.sh "$a" "$b")
echo 'a ax  x  x  a' | (read a b; argv.sh "$a" "$b")
## STDOUT:
['hello', 'world  test']
-- IFS=x --
['a', 'ax  x']
['a', 'ax  x  x']
['a', 'ax  x  x']
['a', 'ax  x  x  a']
## END

#### IFS='x ' read -a: intermediate spaces (unlimited split)

IFS='x '
echo 'a x b'   | (read -a a; argv.sh "${a[@]}")
echo 'a xx b'  | (read -a a; argv.sh "${a[@]}")
echo 'a xxx b' | (read -a a; argv.sh "${a[@]}")
echo 'a x xb'  | (read -a a; argv.sh "${a[@]}")
echo 'a x x b' | (read -a a; argv.sh "${a[@]}")
echo 'ax b'    | (read -a a; argv.sh "${a[@]}")
echo 'ax xb'   | (read -a a; argv.sh "${a[@]}")
echo 'ax  xb'  | (read -a a; argv.sh "${a[@]}")
echo 'ax x xb' | (read -a a; argv.sh "${a[@]}")
## STDOUT:
['a', 'b']
['a', '', 'b']
['a', '', '', 'b']
['a', '', 'b']
['a', '', 'b']
['a', 'b']
['a', '', 'b']
['a', '', 'b']
['a', '', '', 'b']
## END

#### IFS='x ' incomplete backslash
echo ' a b \' | (read a; argv.sh "$a")
echo ' a b \' | (read a b; argv.sh "$a" "$b")
IFS='x '
echo $'a ax  x    \\\nhello' | (read a b; argv.sh "$a" "$b")
## STDOUT:
['a b']
['a', 'b']
['a', 'ax  x    hello']
## END

#### IFS='\ ' and backslash escaping
IFS='\ '
echo "hello\ world  test" | (read a b; argv.sh "$a" "$b")
IFS='\'
echo "hello\ world  test" | (read a b; argv.sh "$a" "$b")
## STDOUT:
['hello world', 'test']
['hello world  test', '']
## END

#### max_split and backslash escaping
echo 'Aa b \ a\ b' | (read a b; argv.sh "$a" "$b")
echo 'Aa b \ a\ b' | (read a b c; argv.sh "$a" "$b" "$c")
echo 'Aa b \ a\ b' | (read a b c d; argv.sh "$a" "$b" "$c" "$d")
## STDOUT:
['Aa', 'b  a b']
['Aa', 'b', ' a b']
['Aa', 'b', ' a b', '']
## END

#### IFS=x read a b <<< xxxxxx
IFS='x '
echo x     | (read a b; argv.sh "$a" "$b")
echo xx    | (read a b; argv.sh "$a" "$b")
echo xxx   | (read a b; argv.sh "$a" "$b")
echo xxxx  | (read a b; argv.sh "$a" "$b")
echo xxxxx | (read a b; argv.sh "$a" "$b")
echo '-- spaces --'
echo 'x    ' | (read a b; argv.sh "$a" "$b")
echo 'xx   ' | (read a b; argv.sh "$a" "$b")
echo 'xxx  ' | (read a b; argv.sh "$a" "$b")
echo 'xxxx ' | (read a b; argv.sh "$a" "$b")
echo 'xxxxx' | (read a b; argv.sh "$a" "$b")
echo '-- with char --'
echo 'xa    ' | (read a b; argv.sh "$a" "$b")
echo 'xax   ' | (read a b; argv.sh "$a" "$b")
echo 'xaxx  ' | (read a b; argv.sh "$a" "$b")
echo 'xaxxx ' | (read a b; argv.sh "$a" "$b")
echo 'xaxxxx' | (read a b; argv.sh "$a" "$b")
## STDOUT:
['', '']
['', '']
['', 'xx']
['', 'xxx']
['', 'xxxx']
-- spaces --
['', '']
['', '']
['', 'xx']
['', 'xxx']
['', 'xxxx']
-- with char --
['', 'a']
['', 'a']
['', 'axx']
['', 'axxx']
['', 'axxxx']
## END

#### read and "\ "

IFS='x '
check() { echo "$1" | (read a b; argv.sh "$a" "$b"); }

echo '-- xs... --'
check 'x '
check 'x \ '
check 'x \ \ '
check 'x \ \ \ '
echo '-- xe... --'
check 'x\ '
check 'x\ \ '
check 'x\ \ \ '
check 'x\  '
check 'x\  '
check 'x\    '

# check 'xx\ '
# check 'xx\ '

## STDOUT:
-- xs... --
['', '']
['', ' ']
['', '  ']
['', '   ']
-- xe... --
['', ' ']
['', '  ']
['', '   ']
['', ' ']
['', ' ']
['', ' ']
## END

#### read bash bug
IFS='x '
echo 'x\  \ ' | (read a b; argv.sh "$a" "$b")
## STDOUT:
['', '   ']
## END
## BUG bash STDOUT:
['', '\x01']
## END
