## oils_failures_allowed: 1
## compare_shells: bash

# spec/bool-parse.test.sh
#
# [ and [[ share the BoolParser
#
# These test cases are for some bugs fixed
#
# See also
#   spec/builtin-bracket.test.sh for [
#   spec/dbracket.test.sh        for [[

#### test builtin - Unexpected trailing word '--' (#2409)

# Minimal repro of sqsh build error
set -- -o; test $# -ne 0 -a "$1" != "--"
echo status=$?

# Now hardcode $1
test $# -ne 0 -a "-o" != "--"
echo status=$?

# Remove quotes around -o
test $# -ne 0 -a -o != "--"
echo status=$?

# How about a different flag?
set -- -z; test $# -ne 0 -a "$1" != "--"
echo status=$?

# A non-flag?
set -- z; test $# -ne 0 -a "$1" != "--"
echo status=$?

## STDOUT:
status=0
status=0
status=0
status=0
status=0
## END

#### test builtin: ( = ) is confusing: equality test or non-empty string test

# here it's equality
test '(' = ')'
echo status=$?

# here it's like -n =
test 0 -eq 0 -a '(' = ')'
echo status=$?

## STDOUT:
status=1
status=0
## END

#### test builtin: ( == ) is confusing: equality test or non-empty string test

# here it's equality
test '(' == ')'
echo status=$?

# here it's like -n ==
test 0 -eq 0 -a '(' == ')'
echo status=$?

## STDOUT:
status=1
status=0
## END

#### Allowed: [[ = ]] and [[ == ]]

[[ = ]]
echo status=$?
[[ == ]]
echo status=$?

## STDOUT:
status=0
status=0
## END

#### Not allowed: [[ ) ]] and [[ ( ]]

[[ ) ]]
echo status=$?
[[ ( ]]
echo status=$?

## status: 2
## STDOUT:
## END

#### test builtin: ( x ) behavior is the same in both cases

test '(' x ')'
echo status=$?

test 0 -eq 0 -a '(' x ')'
echo status=$?

## STDOUT:
status=0
status=0
## END

#### [ -f = ] and [ -f == ]

[ -f = ]
echo status=$?
[ -f == ]
echo status=$?

## STDOUT:
status=1
status=1
## END

#### [[ -f -f ]] and [[ -f == ]]
[[ -f -f ]]
echo status=$?

[[ -f == ]]
echo status=$?

## STDOUT:
status=1
status=1
## END

