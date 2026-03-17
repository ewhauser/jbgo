## compare_shells: dash bash-4.4 mksh

#### Sourcing a script that returns at the top level
echo one
. $REPO_ROOT/spec/testdata/return-helper.sh
echo $?
echo two
## STDOUT:
one
return-helper.sh
42
two
## END

#### top level control flow
$SH $REPO_ROOT/spec/testdata/top-level-control-flow.sh
## status: 0
## STDOUT:
SUBSHELL
BREAK
CONTINUE
RETURN
## OK bash STDOUT:
SUBSHELL
BREAK
CONTINUE
RETURN
DONE
## END

#### errexit and top-level control flow
$SH -o errexit $REPO_ROOT/spec/testdata/top-level-control-flow.sh
## status: 2
## OK bash status: 1
## STDOUT:
SUBSHELL
## END

#### return at top level is an error
return
echo "status=$?"
## stdout-json: ""
## OK bash STDOUT:
status=1
## END

#### continue at top level is NOT an error
# NOTE: bash and mksh both print warnings, but don't exit with an error.
continue
echo status=$?
## stdout: status=0

#### break at top level is NOT an error
break
echo status=$?
## stdout: status=0

#### empty argv default behavior
x=''
$x
echo status=$?

if $x; then
  echo VarSub
fi

if $(echo foo >/dev/null); then
  echo CommandSub
fi

if "$x"; then
  echo VarSub
else
  echo VarSub FAILED
fi

if "$(echo foo >/dev/null)"; then
  echo CommandSub
else
  echo CommandSub FAILED
fi

## STDOUT:
status=0
VarSub
CommandSub
VarSub FAILED
CommandSub FAILED
## END

#### automatically creating arrays by sparse assignment
undef[2]=x
undef[3]=y
argv.sh "${undef[@]}"
## STDOUT:
['x', 'y']
## END
## N-I dash status: 2
## N-I dash stdout-json: ""

#### automatically creating arrays are indexed, not associative
undef[2]=x
undef[3]=y
x='bad'
# bad gets coerced to zero as part of recursive arithmetic evaluation.
undef[$x]=zzz
argv.sh "${undef[@]}"
## STDOUT:
['zzz', 'x', 'y']
## END
## N-I dash status: 2
## N-I dash stdout-json: ""
