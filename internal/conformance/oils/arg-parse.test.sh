## compare_shells: bash
## oils_failures_allowed: 0

# TODO:
# - go through all of compgen -A builtin - which ones allow extra args?
# - cd builtin should be strict

#### true extra
true extra
## STDOUT:
## END

#### shift 1 extra
$SH -c '
set -- a b c
shift 1 extra
'
if test $? -eq 0; then
  echo fail
fi

## STDOUT:
## END

#### continue 1 extra, break, etc.
$SH -c '
for i in foo; do
  continue 1 extra
done
echo status=$?
'
if test $? -eq 0; then
  echo fail
fi

## STDOUT:
## END

