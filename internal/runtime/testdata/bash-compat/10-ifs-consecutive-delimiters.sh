#!/usr/bin/env bash
# Exercise IFS splitting with consecutive delimiters — empty fields must be preserved.

set -euo pipefail

line='one::three:::six'
IFS=: read -ra parts <<<"$line"
printf 'ifs-count:%s\n' "${#parts[@]}"
for i in "${!parts[@]}"; do
  printf 'ifs[%s]=<%s>\n' "$i" "${parts[$i]}"
done
