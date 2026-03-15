#!/usr/bin/env bash
# Exercise sparse arrays created by unsetting individual indices.

set -euo pipefail

arr=(a b c d e)
arr+=(f g)
unset 'arr[2]'
unset 'arr[4]'
printf 'sparse-keys:'
printf '%s ' "${!arr[@]}"
printf '\n'
printf 'sparse-vals:'
printf '%s ' "${arr[@]}"
printf '\n'
# Re-pack into dense array
dense=("${arr[@]}")
printf 'dense-keys:'
printf '%s ' "${!dense[@]}"
printf '\n'
