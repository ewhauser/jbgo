#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/update_mvdan_sh.sh [--ref <upstream-ref>]

Refresh third_party/mvdan-sh from upstream and reapply local patch files.
EOF
}

ref_override=
while (($# > 0)); do
  case "$1" in
    --ref)
      if (($# < 2)); then
        echo "update_mvdan_sh.sh: --ref requires a value" >&2
        exit 2
      fi
      ref_override=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "update_mvdan_sh.sh: unexpected argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)
fork_dir="$repo_root/third_party/mvdan-sh"
patches_dir="$fork_dir/patches"
upstream_file="$fork_dir/UPSTREAM"

if [[ ! -f "$upstream_file" ]]; then
  echo "update_mvdan_sh.sh: missing $upstream_file" >&2
  exit 1
fi

# shellcheck disable=SC1090
source "$upstream_file"

: "${UPSTREAM_REPO:?UPSTREAM_REPO must be set in $upstream_file}"
: "${UPSTREAM_REF:?UPSTREAM_REF must be set in $upstream_file}"

if [[ -n "$ref_override" ]]; then
  UPSTREAM_REF=$ref_override
fi

temp_dir=$(mktemp -d)
cleanup() {
  rm -rf "$temp_dir"
}
trap cleanup EXIT

upstream_dir="$temp_dir/upstream"
stage_dir="$temp_dir/stage"
mkdir -p "$upstream_dir" "$stage_dir"

git -C "$upstream_dir" init -q
git -C "$upstream_dir" remote add origin "$UPSTREAM_REPO"
git -C "$upstream_dir" fetch --depth 1 origin "$UPSTREAM_REF"
git -C "$upstream_dir" checkout -q FETCH_HEAD

resolved_commit=$(git -C "$upstream_dir" rev-parse HEAD)

copy_entry() {
  local entry=$1
  if [[ ! -e "$upstream_dir/$entry" ]]; then
    echo "update_mvdan_sh.sh: upstream entry not found: $entry" >&2
    exit 1
  fi
  rsync -a \
    --exclude='*_test.go' \
    --exclude='testdata/' \
    --exclude='.git/' \
    "$upstream_dir/$entry" "$stage_dir/"
}

copy_entry LICENSE
copy_entry expand
copy_entry fileutil
copy_entry internal
copy_entry interp
copy_entry pattern
copy_entry syntax

python3 - "$stage_dir" <<'PY'
import pathlib
import sys

stage = pathlib.Path(sys.argv[1])
old = "mvdan.cc/sh/v3"
new = "github.com/ewhauser/gbash/third_party/mvdan-sh"

for path in stage.rglob("*.go"):
    data = path.read_text(encoding="utf-8")
    if old not in data:
        continue
    path.write_text(data.replace(old, new), encoding="utf-8")
PY

mkdir -p "$patches_dir"

if compgen -G "$patches_dir/*.patch" > /dev/null; then
  git -C "$stage_dir" init -q
  git -C "$stage_dir" add -A
  for patch in "$patches_dir"/*.patch; do
    echo "==> applying $(basename "$patch")"
    if ! git -C "$stage_dir" apply "$patch"; then
      echo "update_mvdan_sh.sh: failed to apply $patch" >&2
      exit 1
    fi
  done
fi

mkdir -p "$fork_dir"
find "$fork_dir" -mindepth 1 -maxdepth 1 \
  ! -name patches \
  ! -name AGENTS.md \
  ! -name UPSTREAM \
  -exec rm -rf {} +

rsync -a --exclude '.git' "$stage_dir"/ "$fork_dir"/

cat > "$upstream_file" <<EOF
# Managed by ./scripts/update_mvdan_sh.sh
UPSTREAM_REPO=$UPSTREAM_REPO
UPSTREAM_REF=$UPSTREAM_REF
UPSTREAM_COMMIT=$resolved_commit
EOF

echo "updated third_party/mvdan-sh to $resolved_commit"
