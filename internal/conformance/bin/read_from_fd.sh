#!/bin/bash

for fd in "$@"; do
  printf '%s: ' "$fd"
  if ! eval "cat <&$fd"; then
    printf 'FATAL: Error reading from fd %s\n' "$fd" >&2
    exit 1
  fi
done
