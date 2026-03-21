## oils_failures_allowed: 0
## compare_shells: bash

#### history builtin usage
history
echo status=$?
history +5  # hm bash considers this valid
echo status=$?
history -5  # invalid flag
echo status=$?
history f 
echo status=$?
history too many args
echo status=$?
## status: 0
## STDOUT:
status=0
status=0
status=2
status=2
status=2
## END

#### Print shell strings with weird chars: set and printf %q and ${x@Q}

# Use printf here; /bin/echo -e varies across platforms.
# bash declare -p will print binary data, which makes this invalid UTF-8!
# let's test the easier \x01, which doesn't give bash problems.
foo=$(printf 'a\nb\001c'\''d')

#   only supports 'set'; prints it on multiple lines with binary data
#   switches to "'" for single quotes, not \'
#   print binary data all the time, except for printf %q
#   does print $'' strings
#   prints binary data for @Q
#   prints $'' strings

# All are very inconsistent.

set | grep -A1 foo

# Will print multi-line and binary data literally!
#declare -p foo

printf 'pf  %q\n' "$foo"

echo '@Q ' ${foo@Q}

## STDOUT:
foo=$'a\nb\001c\'d'
pf  $'a\nb\001c\'d'
@Q  $'a\nb\001c\'d'
## END

#### Print shell strings with normal chars: set and printf %q and ${x@Q}

# There are variations on whether quotes are printed

foo=spam

set | grep -A1 foo

# Will print multi-line and binary data literally!
typeset -p foo

printf 'pf  %q\n' "$foo"

echo '@Q ' ${foo@Q}

## STDOUT:
foo=spam
declare -- foo="spam"
pf  spam
@Q  'spam'
## END

#### time pipeline
time_err=$TMP/time-pipeline.err
{ time echo hi | wc -c; } 2>"$time_err"
sed -E -e '/^[[:space:]]*$/d' -e 's/[0-9]+m[0-9]+\.[0-9]+s/XmY.ZZZs/g' "$time_err" | tr '\t' ' '
## STDOUT:
       3
real XmY.ZZZs
user XmY.ZZZs
sys XmY.ZZZs
## END
## status: 0

#### shift
set -- 1 2 3 4
shift
echo "$@"
shift 2
echo "$@"
## STDOUT:
2 3 4
4
## END
## status: 0

#### Shifting too far
set -- 1
shift 2
## status: 1

#### Invalid shift argument
shift ZZZ
## status: 2
## OK bash status: 1
