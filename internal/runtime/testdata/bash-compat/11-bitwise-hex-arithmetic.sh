#!/usr/bin/env bash
# Exercise arithmetic with hex literals and bitwise operators.

set -euo pipefail

printf 'bitwise:%s\n' "$(( (0xFF & 0x0F) | 0x30 ))"
