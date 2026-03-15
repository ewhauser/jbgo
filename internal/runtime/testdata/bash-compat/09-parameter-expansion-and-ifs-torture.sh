#!/usr/bin/env bash
# Gnarly edge cases: nested parameter expansion, substring extraction,
# nested command substitution, pattern removal, return-value plumbing,
# and quoting hell.

set -euo pipefail

# ---------- 1. Nested default / alternate parameter expansion ----------
unset maybe || true
fallback=last-resort
printf 'nested-default:%s\n' "${maybe:-${also_missing:-$fallback}}"
set_var=present
printf 'alternate:%s\n' "${set_var:+replaced}"
printf 'alternate-unset:%s\n' "${maybe:+should-not-appear}"

# ---------- 2. Substring extraction with negative offsets ----------
long='abcdefghij'
printf 'substr-pos:%s\n' "${long:3:4}"
printf 'substr-neg:%s\n' "${long: -3}"
printf 'substr-neg-len:%s\n' "${long: -5:2}"

# ---------- 3. Nested command substitution with quoting ----------
inner='hello world'
printf 'nested-cmdsub:%s\n' "$(printf '%s' "$(printf '%s' "$inner" | tr ' ' '_')")"
printf 'arith-in-cmdsub:%s\n' "$(printf '%s' "$(( 2 ** 10 ))")"

# ---------- 4. Pattern removal (greedy vs lazy, front vs back) ----------
path='/usr/local/bin/program.tar.gz'
printf 'short-front:%s\n' "${path#*/}"
printf 'long-front:%s\n' "${path##*/}"
printf 'short-back:%s\n' "${path%.*}"
printf 'long-back:%s\n' "${path%%.*}"

# ---------- 5. Function return values, $?, and conditional pipelines ----------
maybe_fail() {
  return "$1"
}
maybe_fail 0
printf 'rc-zero:%s\n' "$?"
maybe_fail 42 || true
printf 'rc-after-or:%s\n' "$?"
if maybe_fail 1; then
  printf 'if-rc:unexpected\n'
else
  printf 'if-rc:caught\n'
fi

# ---------- 6. Quoting torture: escaped quotes, $'...' ANSI-C, mixed ----------
printf 'escaped-dq:%s\n' "He said \"hi\""
printf 'ansi-c:%s\n' $'tab\there'
printf 'ansi-newline-begin\n%s\nansi-newline-end\n' $'line1\nline2'
combined="it's a \"test\""
printf 'combined:%s\n' "$combined"
