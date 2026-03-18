## compare_shells: bash
## oils_failures_allowed: 0

# forked from spec/ble-idioms
# the IFS= eval 'local x' bug

#### More eval 'local v='

set -u

f() {
  # The temp env messes it up
  tmp1= local x=x
  tmp2= eval 'local y=y'

  # similar to eval
  tmp3= . $REPO_ROOT/spec/testdata/define-local-var-z.sh

  # Bug does not appear with only eval
  #eval 'local v=hello'

  #declare -p v
  echo x=$x
  echo y=$y
  echo z=$z
}

f 

## STDOUT:
x=x
y=y
z=z
## END

#### Temp bindings with local

f() {
  local x=x
  tmp='' local tx=tx

  eval 'local y=y'
  tmp='' eval 'local ty=ty'

  # builtin
  if true; then
    x='X' unset x
    tx='TX' unset tx
    y='Y' unset y
    ty='TY' unset ty
  fi

  #unset y
  #unset ty

  echo x=$x
  echo tx=$tx
  echo y=$y
  echo ty=$ty
}

f

## STDOUT:
x=
tx=
y=
ty=
## END

#### Temp bindings with unset 

# key point:
# unset looks up the stack
# local doesn't though

x=42
unset x
echo x=$x

echo ---

x=42
tmp= unset x
echo x=$x

x=42
tmp= eval 'unset x'
echo x=$x

echo ---

shadow() {
  x=42
  x=tmp unset x
  echo x=$x
  
  x=42
  x=tmp eval 'unset x'
  echo x=$x
}

shadow

echo ---

set -o posix
shadow

# Now shadow

# unset is a special builtin
# type unset

## STDOUT:
x=
---
x=
x=
---
x=42
x=42
---
x=42
x=42
## END

#### FOO=bar $unset - temp binding, then empty argv from unquoted unset var (#2411)
foo=alive! $unset
echo $foo
## STDOUT:
alive!
## END
