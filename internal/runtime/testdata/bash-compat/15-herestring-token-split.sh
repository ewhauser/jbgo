#!/usr/bin/env bash
# Exercise here-string token splitting via read.

set -euo pipefail

read -r a b c <<<"one two three"
printf 'herestr-split:%s|%s|%s\n' "$a" "$b" "$c"
