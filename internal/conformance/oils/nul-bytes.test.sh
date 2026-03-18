## compare_shells: bash
## oils_failures_allowed: 2
## oils_cpp_failures_allowed: 1

#### NUL bytes with echo -e

show_hex() { od -A n -t c -t x1; }

echo -e '\0-' | show_hex
#echo -e '\x00-'
#echo -e '\000-'

## STDOUT:
  \0   -  \n
  00  2d  0a
## END

#### printf - literal NUL in format string

# Show both printable and hex
show_hex() { od -A n -t c -t x1; }

printf $'x\U0z' | show_hex
echo ---

printf $'x\U00z' | show_hex
echo ---

printf $'\U0z' | show_hex

## STDOUT:
   x
  78
---
   x
  78
---
## END

#### printf - \0 escape shows NUL byte
show_hex() { od -A n -t c -t x1; }

printf '\0\n' | show_hex
## STDOUT:
  \0  \n
  00  0a
## END

show_hex() { od -A n -t c -t x1; }

nul=$'\0'
echo "$nul" | show_hex
printf '%s\n' "$nul" | show_hex

## STDOUT:
  \n
  0a
  \n
  0a
## END

show_hex() { od -A n -t c -t x1; }

# legacy echo -e

echo $'\0' | show_hex

## STDOUT:
  \n
  0a
## END

#### NUL bytes and IFS splitting

argv.sh $(echo -e '\0')
argv.sh "$(echo -e '\0')"
argv.sh $(echo -e 'a\0b')
argv.sh "$(echo -e 'a\0b')"

## STDOUT:
[]
['']
['ab']
['ab']
## END

#### NUL bytes with test -n

test -n $''
echo status=$?

test -n $'\0'
echo status=$?

## STDOUT:
status=1
status=1
## END

#### NUL bytes with test -f

test -f $'\0'
echo status=$?

touch foo
test -f $'foo\0'
echo status=$?

test -f $'foo\0bar'
echo status=$?

test -f $'foobar'
echo status=$?

## STDOUT:
status=1
status=0
status=0
status=1
## END

empty=$''
nul=$'\0'

echo empty=${#empty}
echo nul=${#nul}

## STDOUT:
empty=0
nul=0
## END

#### Compare \x00 byte versus \x01 byte - command sub

# https://stackoverflow.com/questions/32722007/is-skipping-ignoring-nul-bytes-on-process-substitution-standardized
# bash contains a warning!

show_bytes() {
  echo -n "$1" | od -A n -t x1
}

s=$(printf '.\001.')
echo len=${#s}
show_bytes "$s"

s=$(printf '.\000.')
echo len=${#s}
show_bytes "$s"

s=$(printf '\000')
echo len=${#s} 
show_bytes "$s"

## STDOUT:
len=3
 2e 01 2e
len=2
 2e 2e
len=0
## END

#### Compare \x00 byte versus \x01 byte - read builtin

# Hm same odd behavior

show_string() {
  read s
  echo len=${#s}
  echo -n "$s" | od -A n -t x1
}

printf '.\001.' | show_string

printf '.\000.' | show_string

printf '\000' | show_string

## STDOUT:
len=3
 2e 01 2e
len=2
 2e 2e
len=0
## END

#### Compare \x00 byte versus \x01 byte - read -n

show_string() {
  read -n 3 s
  echo len=${#s}
  echo -n "$s" | od -A n -t x1
}

printf '.\001.' | show_string

printf '.\000.' | show_string

printf '\000' | show_string

## STDOUT:
len=3
 2e 01 2e
len=2
 2e 2e
len=0
## END

#### Compare \x00 byte versus \x01 byte - mapfile builtin

{ 
  printf '.\000.\n'
  printf '.\000.\n'
} |
{ mapfile LINES
  echo len=${#LINES[@]}
  for line in ${LINES[@]}; do
    echo -n "$line" | od -A n -t x1
  done
}

# bash is INCONSISTENT:
# - it TRUNCATES at \0, with 'mapfile'
# - rather than just IGNORING \0, with 'read'

## STDOUT:
len=2
 2e
 2e
## END

#### Strip ops # ## % %% with NUL bytes

show_bytes() {
  echo -n "$1" | od -A n -t x1
}

s=$(printf '\000.\000')
echo len=${#s}
show_bytes "$s"

echo ---

t=${s#?}
echo len=${#t}
show_bytes "$t"

t=${s##?}
echo len=${#t}
show_bytes "$t"

t=${s%?}
echo len=${#t}
show_bytes "$t"

t=${s%%?}
echo len=${#t}
show_bytes "$t"

## STDOUT:
len=1
 2e
---
len=0
len=0
len=0
len=0
## END

#### Issue 2269 Reduction

show_bytes() {
  echo -n "$1" | od -A n -t x1
}

s=$(printf '\000x')
echo len=${#s}
show_bytes "$s"

# strip one char from the front
s=${s#?}
echo len=${#s}
show_bytes "$s"

echo ---

s=$(printf '\001x')
echo len=${#s}
show_bytes "$s"

# strip one char from the front
s=${s#?}
echo len=${#s}
show_bytes "$s"

## STDOUT:
len=1
 78
len=0
---
len=2
 01 78
len=1
 78
## END

#### Issue 2269 - Do NUL bytes match ? in ${a#?}

# https://github.com/oils-for-unix/oils/issues/2269

escape_arg() {
	a="$1"
	until [ -z "$a" ]; do
		case "$a" in
		(\'*) printf "'\"'\"'";;
		(*) printf %.1s "$a";;
		esac
		a="${a#?}"
    echo len=${#a} >&2
	done
}

# encode
phrase="$(escape_arg "that's it!")"
echo escaped "$phrase"

# decode
eval "printf '%s\\n' '$phrase'"

echo ---

# harder input: NUL surrounded with ::
arg="$(printf ':\000:')" 
#echo "arg=$arg"

echo escaped "$(escape_arg "$arg")"
#echo "arg=$arg"

## STDOUT:
escaped that'"'"'s it!
that's it!
---
escaped ::
## END

