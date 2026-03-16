#!/bin/bash

quote_py_arg() {
  local arg=$1
  local hex

  printf "'"
  for hex in $(LC_ALL=C printf '%s' "$arg" | od -An -v -tx1); do
    case $hex in
      09)
        printf '\\t'
        ;;
      0a)
        printf '\\n'
        ;;
      0d)
        printf '\\r'
        ;;
      27)
        printf '%s' "\\'"
        ;;
      5c)
        printf '\\\\'
        ;;
      *)
        case $hex in
          2[0-6] | 2[8-9a-f] | [3-4][0-9a-f] | 5[0-9ab] | 5[d-f] | 6[0-9a-f] | 7[0-9a-e])
            printf '%b' "\\x$hex"
            ;;
          *)
            printf '\\x%s' "$hex"
            ;;
        esac
        ;;
    esac
  done
  printf "'"
}

printf '['
sep=''
for arg in "$@"; do
  printf '%s' "$sep"
  quote_py_arg "$arg"
  sep=', '
done
printf ']\n'
