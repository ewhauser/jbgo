#!/bin/bash

shopt -s nullglob

for fd_path in /proc/self/fd/*; do
  fd=${fd_path##*/}
  if target=$(readlink "$fd_path" 2>/dev/null); then
    printf '%s %s\n' "$fd" "$target"
  else
    printf '%s %s\n' "$fd" 'unavailable'
  fi
done
