## oils_failures_allowed: 0
## compare_shells: bash

#### type -> keyword builtin 

type while cd

## STDOUT:
while is a shell keyword
cd is a shell builtin
## END

#### type -> alias external

mkdir -p _tmp
shopt -s expand_aliases || true  # bash

alias ll='ls -l'

touch _tmp/date
chmod +x _tmp/date
PATH=_tmp:/bin

normalize() {
  # ignore quotes and backticks
  # bash prints a left backtick
  quotes='"`'\'
  "${GBASH_CONFORMANCE_SED:-sed}" \
    -e "s/[$quotes]//g" \
    -e 's/shell function/function/' \
    -e 's/is aliased to/is an alias for/'
}

type ll date | normalize

# Note: both procs and funcs go in var namespace?  So they don't respond to
# 'type'?

## STDOUT:
ll is an alias for ls -l
date is _tmp/date
## END

#### type of relative path

mkdir -p _tmp
touch _tmp/file _tmp/ex
chmod +x _tmp/ex

type _tmp/file _tmp/ex

## status: 1
## STDOUT:
_tmp/ex is _tmp/ex
## END

#### type -> not found

type zz 2>err.txt
echo status=$?

fgrep -o 'zz: not found' err.txt || true

## STDOUT:
status=1
zz: not found
## END

## STDERR:
## END

#### special builtins are called out
type cd
type eval
type :
type true

echo
type export

## STDOUT:
cd is a shell builtin
eval is a special shell builtin
: is a special shell builtin
true is a shell builtin

export is a special shell builtin
## END

## N-I bash STDOUT:
cd is a shell builtin
eval is a shell builtin
: is a shell builtin
true is a shell builtin

export is a shell builtin
## END

#### more special builtins 
exit

type .
type source

# no agreement here!
# type local
# type typeset

## STDOUT:
. is a special shell builtin
source is a shell builtin
## END

