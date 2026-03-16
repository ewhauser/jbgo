#!/bin/bash

for name in "$@"; do
  if [[ ${!name+x} ]]; then
    printf '%s\n' "${!name}"
  else
    printf 'None\n'
  fi
done
