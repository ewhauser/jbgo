## oils_failures_allowed: 0
## compare_shells: bash

# We would have to validate all Lit_Chars tokens, and the like.
#
# single and double quoted strings.  Although there might be a global lexer
# hack for Id.Lit_Chars tokens.  Would that catch here docs though?

# Test all the lexing contexts
cat >unicode.sh << 'EOF'
echo μ 'μ' "μ" $'μ'
EOF

# Show that all lexer modes recognize unicode sequences
#
# Oh I guess we need to check here docs too?

#$SH -n unicode.sh

$SH unicode.sh

# Trim off the first byte of mu
sed 's/\xce//g' unicode.sh > not-unicode.sh

echo --
$SH not-unicode.sh | od -A n -t x1

## STDOUT:
μ μ μ μ
--
 bc 20 bc 20 bc 20 bc 0a
## END

#### Unicode escapes \u03bc \U000003bc in $'', echo -e, printf

echo $'\u03bc \U000003bc'

echo -e '\u03bc \U000003bc'

printf '\u03bc \U000003bc\n'

## STDOUT:
μ μ
μ μ
μ μ
## END

#### Max code point U+10ffff can escaped with $''  printf  echo -e

py-repr() {
  printf "'"
  printf '%s' "$1" | od -A n -t x1 | tr -d ' \n' | sed 's/\(..\)/\\x\1/g'
  printf "'\n"
}

py-repr $'\U0010ffff'
py-repr $(echo -e '\U0010ffff')
py-repr $(printf '\U0010ffff')

## STDOUT:
'\xf4\x8f\xbf\xbf'
'\xf4\x8f\xbf\xbf'
'\xf4\x8f\xbf\xbf'
## END

# Unicode replacement char 

#### $'' does NOT check that 0x110000 is too big at parse time

py-repr() {
  printf "'"
  printf '%s' "$1" | od -A n -t x1 | tr -d ' \n' | sed 's/\(..\)/\\x\1/g'
  printf "'\n"
}

py-repr $'\U00110000'

## STDOUT:
'\xf4\x90\x80\x80'
## END

#### $'' does not check for surrogate range at parse time

py-repr() {
  printf "'"
  printf '%s' "$1" | od -A n -t x1 | tr -d ' \n' | sed 's/\(..\)/\\x\1/g'
  printf "'\n"
}

py-repr $'\udc00'

py-repr $'\U0000dc00' 

## STDOUT:
'\xed\xb0\x80'
'\xed\xb0\x80'
## END

#### printf / echo -e do NOT check max code point at runtime

py-repr() {
  printf "'"
  printf '%s' "$1" | od -A n -t x1 | tr -d ' \n' | sed 's/\(..\)/\\x\1/g'
  printf "'\n"
}

e="$(echo -e '\U00110000')"
echo status=$?
py-repr "$e"

p="$(printf '\U00110000')"
echo status=$?
py-repr "$p"

## STDOUT:
status=0
'\xf4\x90\x80\x80'
status=0
'\xf4\x90\x80\x80'
## END

#### printf / echo -e do NOT check surrogates at runtime

py-repr() {
  printf "'"
  printf '%s' "$1" | od -A n -t x1 | tr -d ' \n' | sed 's/\(..\)/\\x\1/g'
  printf "'\n"
}

e="$(echo -e '\udc00')"
echo status=$?
py-repr "$e"

e="$(echo -e '\U0000dc00')"
echo status=$?
py-repr "$e"

p="$(printf '\udc00')"
echo status=$?
py-repr "$p"

p="$(printf '\U0000dc00')"
echo status=$?
py-repr "$p"

## STDOUT:
status=0
'\xed\xb0\x80'
status=0
'\xed\xb0\x80'
status=0
'\xed\xb0\x80'
status=0
'\xed\xb0\x80'
## END

