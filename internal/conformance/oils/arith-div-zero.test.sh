## compare_shells: bash

# Tests for arithmetic division-by-zero error message format.
# The error message should include original source tokens, not evaluated values.
# Note: We use braces {} to group the command so the error is captured in the pipe.
# Note: We escape parentheses in the grep pattern for BRE compatibility.


#### Division by zero with expression divisor shows source token

# When the divisor is an expression like (2-2), the error message should
# include the original expression text "(2-2)", not the evaluated value "0".
{ echo $((1/(2-2))); } 2>&1 | grep -oE 'division by 0 \(error token is "[^"]*"\)'
echo status=$?

## STDOUT:
division by 0 (error token is "(2-2)")
status=0
## END


#### Division by zero with literal divisor shows literal token

{ echo $((10/0)); } 2>&1 | grep -oE 'division by 0 \(error token is "[^"]*"\)'
echo status=$?

## STDOUT:
division by 0 (error token is "0")
status=0
## END


#### Division by zero with variable divisor shows variable name

x=0
{ echo $((10/x)); } 2>&1 | grep -oE 'division by 0 \(error token is "[^"]*"\)'
echo status=$?

## STDOUT:
division by 0 (error token is "x")
status=0
## END


#### Modulo by zero with expression divisor shows source token

{ echo $((10%(1-1))); } 2>&1 | grep -oE 'division by 0 \(error token is "[^"]*"\)'
echo status=$?

## STDOUT:
division by 0 (error token is "(1-1)")
status=0
## END
