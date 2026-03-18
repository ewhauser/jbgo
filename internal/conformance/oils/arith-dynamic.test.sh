## compare_shells: bash
## oils_failures_allowed: 3

# Various tests for dynamic parsing of arithmetic substitutions.

#### Double quotes
echo $(( "1 + 2" * 3 ))
echo $(( "1+2" * 3 ))
## STDOUT:
7
7
## END

#### Single quotes
echo $(( '1' + '2' * 3 ))
echo status=$?

echo $(( '1 + 2' * 3 ))
echo status=$?
## STDOUT:
status=1
status=1
## END

#### Substitutions
x='1 + 2'
echo $(( $x * 3 ))
echo $(( "$x" * 3 ))
## STDOUT:
7
7
## END

#### Variable references
x='1'
echo $(( x + 2 * 3 ))
echo status=$?

# Expression like values are evaluated first (this is unlike double quotes)
x='1 + 2'
echo $(( x * 3 ))
echo status=$?
## STDOUT:
7
status=0
9
status=0
## END

