#!/usr/bin/env bash
# Isolate brace+glob expansion parity so it cannot mask case/local regressions.

set -euo pipefail

mkdir -p tree/one tree/two
: > tree/one/a.txt
: > tree/one/b.log
: > tree/two/c.txt

for path in tree/*/*.{txt,log}; do
  printf '%s\n' "$path"
done
