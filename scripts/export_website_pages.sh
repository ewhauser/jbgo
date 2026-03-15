#!/usr/bin/env bash

set -euo pipefail

repo_dir="${1:-}"
output_dir="${2:-}"
compat_tmp_dir=""

cleanup() {
  if [ -n "$compat_tmp_dir" ] && [ -d "$compat_tmp_dir" ]; then
    rm -rf "$compat_tmp_dir"
  fi
}
trap cleanup EXIT

if [ -z "$repo_dir" ] || [ -z "$output_dir" ]; then
  echo "usage: $0 <repo-dir> <output-dir>" >&2
  exit 1
fi

resolve_compat_asset() {
  local path_value="${1:-}"
  local url_value="${2:-}"
  local filename="${3:-}"

  if [ -n "$path_value" ]; then
    printf '%s\n' "$path_value"
    return 0
  fi

  if [ -z "$url_value" ]; then
    return 0
  fi

  if [ -z "$compat_tmp_dir" ]; then
    compat_tmp_dir="$(mktemp -d)"
  fi

  local dest="$compat_tmp_dir/$filename"
  if curl --fail --location --silent --show-error --output "$dest" "$url_value"; then
    printf '%s\n' "$dest"
    return 0
  fi

  rm -f "$dest"
  echo "warning: failed to fetch $url_value" >&2
}

resolved_summary_path="$(
  resolve_compat_asset \
    "${GBASH_WEBSITE_COMPAT_SUMMARY_PATH:-}" \
    "${GBASH_WEBSITE_COMPAT_SUMMARY_URL:-}" \
    "compat-summary.json"
)"
resolved_badge_path="$(
  resolve_compat_asset \
    "${GBASH_WEBSITE_COMPAT_BADGE_PATH:-}" \
    "${GBASH_WEBSITE_COMPAT_BADGE_URL:-}" \
    "compat-badge.svg"
)"

(
  cd "$repo_dir"
  GBASH_WEBSITE_EXPORT="${GBASH_WEBSITE_EXPORT:-1}" \
  GBASH_WEBSITE_COMPAT_SUMMARY_PATH="$resolved_summary_path" \
  GBASH_WEBSITE_COMPAT_BADGE_PATH="$resolved_badge_path" \
  pnpm --dir website build
)

rm -rf "$output_dir"
mkdir -p "$output_dir"
cp -a "$repo_dir/website/out/." "$output_dir"/
compat_output_dir="$output_dir/compat/latest"
if [ -n "$resolved_summary_path" ] || [ -n "$resolved_badge_path" ]; then
  mkdir -p "$compat_output_dir"
fi
if [ -n "$resolved_summary_path" ]; then
  cp "$resolved_summary_path" "$compat_output_dir/summary.json"
fi
if [ -n "$resolved_badge_path" ]; then
  cp "$resolved_badge_path" "$compat_output_dir/badge.svg"
fi
rm -f "$compat_output_dir/index.html"
touch "$output_dir/.nojekyll"
