## compare_shells: bash

touch one-bar

echo hi > one-*

cat one-bar

echo escaped > one-\*

cat one-\*

## STDOUT:
hi
escaped
## END

#### File redirect without matching any file

echo hi > zz-*-xx
echo status=$?

echo zz*

## STDOUT:
status=0
zz-*-xx
## END

echo hi > qq-*-zz
echo status=$?

echo qq*

## status: 1
## STDOUT:
## END

#### File redirect without matching any file, with failglob

shopt -s failglob

echo hi > zz-*-xx
echo status=$?

echo zz*
echo status=$?

## STDOUT:
status=1
status=1
## END

#### Redirect to $empty (in function body)
empty=''
fun() { echo hi; } > $empty
fun
echo status=$?
## STDOUT:
status=1
## END

#### Redirect to '' 
echo hi > ''
echo status=$?
## STDOUT:
status=1
## END

#### File redirect to $var with glob char

touch two-bar

star='*'

# This gets glob-expanded, as it does outside redirects
echo hi > two-$star
echo status=$?

head two-bar two-\*

## status: 1
## STDOUT:
status=0
==> two-bar <==
hi
## END

touch foo-bar
touch foo-spam

echo hi > foo-*
echo status=$?

head foo-bar foo-spam

## STDOUT:
status=1
==> foo-bar <==

==> foo-spam <==
## END

#### File redirect with extended glob

shopt -s extglob

touch foo-bar

echo hi > @(*-bar|other)
echo status=$?

cat foo-bar

## status: 0
## STDOUT:
status=0
hi
## END

#### Extended glob that doesn't match anything
shopt -s extglob
rm bad_*

# They actually write this literal file!  This is what EvalWordToString() does,
# as opposed to _EvalWordToParts.
echo foo > bad_@(*.cc|*.h)
echo status=$?

echo bad_*

shopt -s failglob

echo foo > bad_@(*.cc|*.h)
echo status=$?

## STDOUT:
status=0
bad_@(*.cc|*.h)
status=1
## END

#### Non-file redirects don't respect glob args (we differe from bash)

touch 10

exec 10>&1  # open stdout as descriptor 10

echo should-not-be-on-stdout >& 1*

echo stdout
echo stderr >&2

## status: 0

## STDOUT:
stdout
## END

## BUG bash STDOUT:
should-not-be-on-stdout
stdout
## END

#### Redirect with brace expansion isn't allowed

echo hi > a-{one,two}
echo status=$?

head a-*
echo status=$?

## STDOUT:
status=1
status=1
## END

#### File redirects have word splitting too!

file='foo bar'

echo hi > $file
echo status=$?

cat "$file"
echo status=$?

## STDOUT:
status=1
status=1
## END

