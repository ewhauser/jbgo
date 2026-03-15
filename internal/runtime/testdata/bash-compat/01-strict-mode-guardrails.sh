#!/usr/bin/env bash
# Exercise errexit/nounset/pipefail edges that often drift from Bash.

set -euo pipefail

printf 'case:strict-mode\n'

if grep -q beta <<<"alpha"; then
  printf 'if-branch:unexpected\n'
else
  printf 'if-branch:else\n'
fi

false || printf 'or-list:recovered\n'
! grep -q alpha <<<"beta"
printf 'bang-predicate:ok\n'

printf 'x\ny\n' | tail -n 1 | grep -q y
printf 'pipefail:pass\n'

count=0
if (( count == 0 )); then
  printf 'arith-if:zero\n'
fi

trap 'printf '\''exit-trap:%s\n'\'' "$?"' EXIT
