#!/usr/bin/env bash
# Exercise deterministic input-side process substitution behavior.

set -euo pipefail

cat <(printf 'alpha\nbeta\n')

while IFS= read -r line; do
  printf 'loop:%s\n' "$line"
done < <(printf 'gamma\ndelta\n')
