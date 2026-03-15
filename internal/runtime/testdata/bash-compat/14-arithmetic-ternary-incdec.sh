#!/usr/bin/env bash
# Exercise ternary, pre/post increment/decrement, and comma operator in
# arithmetic expansion.

set -euo pipefail

x=5
printf 'ternary:%s\n' "$(( x > 3 ? 10 : 20 ))"
printf 'pre-inc:%s\n' "$(( ++x ))"
printf 'post-dec:%s\n' "$(( x-- ))"
printf 'after-dec:%s\n' "$x"
printf 'comma:%s\n' "$(( x=100, x/4 ))"
