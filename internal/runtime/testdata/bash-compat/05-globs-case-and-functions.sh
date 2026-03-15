#!/usr/bin/env bash
# Exercise case dispatch, functions, and local variables without relying on
# path expansion parity.

set -euo pipefail

emit_tags() {
  local base tag

  for base in a.txt b.log c.md; do
    case "$base" in
      *.txt) tag=text ;;
      *.log) tag=log ;;
      *) tag=other ;;
    esac
    printf '%s:%s\n' "$base" "$tag"
  done
}

emit_tags
