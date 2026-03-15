#!/usr/bin/env bash
# Isolate /dev/null parity so it cannot mask unrelated strict-mode regressions.

set -euo pipefail

printf 'case:null-redirection\n'
printf 'discard me\n' >/dev/null
printf 'after-dev-null\n'
