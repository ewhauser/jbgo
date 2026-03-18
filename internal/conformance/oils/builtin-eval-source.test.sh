## compare_shells: bash
## oils_failures_allowed: 0

#### Eval
eval "a=3"
echo $a
## stdout: 3

#### eval accepts/ignores --
eval -- echo hi
## STDOUT:
hi
## END

#### eval usage
eval -
echo $?
eval -z
echo $?
## STDOUT:
127
2
## END

#### eval string with 'break continue return error'

set -e

sh_func_that_evals() {
  local code_str=$1
  for i in 1 2; do
    echo $i
    eval "$code_str"
  done
  echo 'end func'
}

for code_str in break continue return false; do
  echo "--- $code_str"
  sh_func_that_evals "$code_str"
done
echo status=$?

## status: 1
## STDOUT:
--- break
1
end func
--- continue
1
2
end func
--- return
1
--- false
1
## END

#### exit within eval (regression)
eval 'exit 42'
echo 'should not get here'
## stdout-json: ""
## status: 42

#### exit within source (regression)
cd $TMP
echo 'exit 42' > lib.sh
. ./lib.sh
echo 'should not get here'
## stdout-json: ""
## status: 42

#### Source
lib=$TMP/spec-test-lib.sh
echo 'LIBVAR=libvar' > $lib
. $lib
echo $LIBVAR
## stdout: libvar

#### source accepts/ignores --
echo 'echo foo' > $TMP/foo.sh
source -- $TMP/foo.sh
## STDOUT:
foo
## END

#### Source nonexistent
source /nonexistent/path
echo status=$?
## stdout: status=1

#### Source with no arguments
source
echo status=$?
## stdout: status=2

#### Source with arguments
. $REPO_ROOT/spec/testdata/show-argv.sh foo bar
## STDOUT:
show-argv: foo bar
## END

#### Source from a function, mutating argv and defining a local var
f() {
  . $REPO_ROOT/spec/testdata/source-argv.sh              # no argv
  . $REPO_ROOT/spec/testdata/source-argv.sh args to src  # new argv
  echo $@
  echo foo=$foo  # defined in source-argv.sh
}
f args to func
echo foo=$foo  # not defined
## STDOUT:
source-argv: args to func
source-argv: args to src
to func
foo=foo_val
foo=
## END

#### Source with syntax error
# Although set-o errexit handles this.  We don't want to break the invariant
# that a builtin like 'source' behaves like an external program.  An external
# program can't halt the shell!
echo 'echo >' > $TMP/syntax-error.sh
. $TMP/syntax-error.sh
echo status=$?
## stdout: status=2

#### Eval with syntax error
eval 'echo >'
echo status=$?
## stdout: status=2

#### Eval in does tilde expansion

x="~"
eval y="$x"  # scalar
test "$x" = "$y" || echo FALSE
[[ $x == /* ]] || echo FALSE  # doesn't start with /
[[ $y == /* ]] && echo TRUE

#argv "$x" "$y"

## STDOUT:
FALSE
FALSE
TRUE
## END

#### Eval in bash does tilde expansion in array

# the "make" plugin in bash-completion relies on this?  wtf?
x="~"

# UPSTREAM CODE

#eval array=( "$x" )

# FIXED CODE -- proper quoting.

eval 'array=(' "$x" ')'  # array

test "$x" = "${array[0]}" || echo FALSE
[[ $x == /* ]] || echo FALSE  # doesn't start with /
[[ "${array[0]}" == /* ]] && echo TRUE
## STDOUT:
FALSE
FALSE
TRUE
## END

#### source works for files in current directory (bash only)
cd $TMP
echo "echo current dir" > cmd
. cmd
echo status=$?
## STDOUT:
current dir
status=0
## END

# This is a special builtin so failure is fatal.

#### source looks in PATH for files
mkdir -p dir
echo "echo hi" > dir/cmd
PATH="dir:$PATH"
. cmd
rm dir/cmd
## STDOUT:
hi
## END

#### source finds files in PATH before current dir
cd $TMP
mkdir -p dir
echo "echo path" > dir/cmd
echo "echo current dir" > cmd
PATH="dir:$PATH"
. cmd
echo status=$?
## STDOUT:
path
status=0
## END

#### source works for files in subdirectory
mkdir -p dir
echo "echo path" > dir/cmd
. dir/cmd
rm dir/cmd
## STDOUT:
path
## END

#### source doesn't crash when targeting a directory
cd $TMP
mkdir -p dir
. ./dir/
echo status=$?
## stdout: status=1

#### sourcing along PATH should ignore directories

mkdir -p _tmp/shell
mkdir -p _tmp/dir/hello.sh
printf 'echo hi' >_tmp/shell/hello.sh

DIR=$PWD/_tmp/dir
SHELL=$PWD/_tmp/shell

# Should find the file hello.sh right away and source it
PATH="$SHELL:$PATH" . hello.sh
echo status=$?

# Should fail because hello.sh cannot be found
PATH="$DIR:$SHELL:$PATH" . hello.sh
echo status=$?

## STDOUT:
hi
status=0
hi
status=0
## END

