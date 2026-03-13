#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

GNU_BUILD_CACHE_VERSION=${GNU_BUILD_CACHE_VERSION:-v3}
GNU_BUILD_CACHE_REPO=${GNU_BUILD_CACHE_REPO:-ewhauser/gbash}
GNU_BUILD_CACHE_TAG=${GNU_BUILD_CACHE_TAG:-gnu-build-cache-v3}
GNU_CACHE_DIR=${GNU_CACHE_DIR:-.cache/gnu}
GNU_GBASH_BIN=${GNU_GBASH_BIN:-${GNU_CACHE_DIR}/bin/gbash}

if [[ "$GNU_CACHE_DIR" != /* ]]; then
  GNU_CACHE_DIR="$REPO_ROOT/$GNU_CACHE_DIR"
fi
if [[ "$GNU_GBASH_BIN" != /* ]]; then
  GNU_GBASH_BIN="$REPO_ROOT/$GNU_GBASH_BIN"
fi

GNU_VERSION=$(
  python3 - "$REPO_ROOT/cmd/gbash-gnu/manifest.json" <<'PY'
import json, sys
with open(sys.argv[1], "r", encoding="utf-8") as f:
    print(json.load(f)["gnu_version"])
PY
)

HOST_GOOS=$(cd "$REPO_ROOT" && go env GOOS)
HOST_GOARCH=$(cd "$REPO_ROOT" && go env GOARCH)
ASSET_BASENAME="gnu-build-cache_${GNU_BUILD_CACHE_VERSION}_coreutils-${GNU_VERSION}_${HOST_GOOS}_${HOST_GOARCH}"
ARCHIVE_NAME="${ASSET_BASENAME}.tar.gz"
SHA_NAME="${ARCHIVE_NAME}.sha256"
META_NAME="${ASSET_BASENAME}.json"
PREBUILT_DIR="${GNU_CACHE_DIR}/prebuilt"
ARCHIVE_PATH="${PREBUILT_DIR}/${ARCHIVE_NAME}"
SHA_PATH="${PREBUILT_DIR}/${SHA_NAME}"
META_PATH="${PREBUILT_DIR}/${META_NAME}"

usage() {
  cat <<'EOF'
Usage:
  scripts/gnu-build-cache.sh archive-dir
  scripts/gnu-build-cache.sh archive-path
  scripts/gnu-build-cache.sh cache-key
  scripts/gnu-build-cache.sh fetch
  scripts/gnu-build-cache.sh publish
  scripts/gnu-build-cache.sh run
EOF
}

need_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

compute_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

verify_sha256() {
  local archive_path=$1
  local sha_path=$2
  local expected actual
  expected=$(awk '{print $1}' "$sha_path")
  actual=$(compute_sha256 "$archive_path")
  if [[ "$expected" != "$actual" ]]; then
    echo "checksum mismatch for $archive_path" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

ensure_archive_dir() {
  mkdir -p "$PREBUILT_DIR"
}

write_metadata() {
  local archive_path=$1
  local sha256=$2
  cat > "$META_PATH" <<EOF
{
  "cache_version": "${GNU_BUILD_CACHE_VERSION}",
  "gnu_version": "${GNU_VERSION}",
  "goos": "${HOST_GOOS}",
  "goarch": "${HOST_GOARCH}",
  "archive_name": "${ARCHIVE_NAME}",
  "archive_path": "${archive_path}",
  "archive_sha256": "${sha256}",
  "created_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "release_repo": "${GNU_BUILD_CACHE_REPO}",
  "release_tag": "${GNU_BUILD_CACHE_TAG}"
}
EOF
}

fetch_archive() {
  ensure_archive_dir
  if [[ -f "$ARCHIVE_PATH" && -f "$SHA_PATH" ]]; then
    verify_sha256 "$ARCHIVE_PATH" "$SHA_PATH"
    echo "$ARCHIVE_PATH"
    return
  fi

  if ! command -v gh >/dev/null 2>&1; then
    echo "gh is not installed; cannot fetch prepared GNU build archive" >&2
    return 1
  fi

  local tmpdir
  tmpdir=$(mktemp -d)
  trap 'rm -rf "${tmpdir:-}"; trap - RETURN' RETURN

  gh release download "$GNU_BUILD_CACHE_TAG" \
    -R "$GNU_BUILD_CACHE_REPO" \
    -D "$tmpdir" \
    -p "$ARCHIVE_NAME" \
    -p "$SHA_NAME" \
    -p "$META_NAME"

  mv "$tmpdir/$ARCHIVE_NAME" "$ARCHIVE_PATH"
  mv "$tmpdir/$SHA_NAME" "$SHA_PATH"
  mv "$tmpdir/$META_NAME" "$META_PATH"
  verify_sha256 "$ARCHIVE_PATH" "$SHA_PATH"
  echo "$ARCHIVE_PATH"
}

publish_archive() {
  need_tool gh
  ensure_archive_dir

  local tmpdir archive sha256
  tmpdir=$(mktemp -d)
  trap 'rm -rf "${tmpdir:-}"; trap - RETURN' RETURN
  archive="$tmpdir/$ARCHIVE_NAME"

  (
    cd "$REPO_ROOT"
    go run ./cmd/gbash-gnu --cache-dir "$GNU_CACHE_DIR" --write-prepared-build-archive "$archive"
  )

  sha256=$(compute_sha256 "$archive")
  printf '%s  %s\n' "$sha256" "$ARCHIVE_NAME" > "$tmpdir/$SHA_NAME"
  cp "$archive" "$ARCHIVE_PATH"
  cp "$tmpdir/$SHA_NAME" "$SHA_PATH"
  write_metadata "$ARCHIVE_PATH" "$sha256"
  cp "$META_PATH" "$tmpdir/$META_NAME"

  if ! gh release view "$GNU_BUILD_CACHE_TAG" -R "$GNU_BUILD_CACHE_REPO" >/dev/null 2>&1; then
    gh release create "$GNU_BUILD_CACHE_TAG" \
      -R "$GNU_BUILD_CACHE_REPO" \
      --title "GNU build cache" \
      --notes "Prepared GNU coreutils build caches for the gbash compatibility harness."
  fi

  gh release upload "$GNU_BUILD_CACHE_TAG" \
    -R "$GNU_BUILD_CACHE_REPO" \
    --clobber \
    "$archive" \
    "$tmpdir/$SHA_NAME" \
    "$tmpdir/$META_NAME"

  echo "$ARCHIVE_PATH"
}

run_harness() {
  local use_archive=1

  ensure_archive_dir
  mkdir -p "$(dirname "$GNU_GBASH_BIN")"

  if [[ "${GNU_FORCE_REBUILD:-0}" == "1" ]]; then
    use_archive=0
  elif [[ ! -f "$ARCHIVE_PATH" ]]; then
    if ! fetch_archive >/dev/null 2>&1; then
      echo "prepared GNU build archive unavailable; building local archive" >&2
      publish_local_archive
    fi
  fi

  (
    cd "$REPO_ROOT"
    go build -o "$GNU_GBASH_BIN" ./cmd/gbash
  )

  local cmd=(go run ./cmd/gbash-gnu --cache-dir "$GNU_CACHE_DIR" --gbash-bin "$GNU_GBASH_BIN")
  if [[ -n "${GNU_RESULTS_DIR:-}" ]]; then
    cmd+=(--results-dir "$GNU_RESULTS_DIR")
  fi
  if [[ $use_archive -eq 1 ]]; then
    cmd+=(--prepared-build-archive "$ARCHIVE_PATH")
  fi

  (
    cd "$REPO_ROOT"
    "${cmd[@]}"
  )
}

publish_local_archive() {
  ensure_archive_dir
  (
    cd "$REPO_ROOT"
    go run ./cmd/gbash-gnu --cache-dir "$GNU_CACHE_DIR" --write-prepared-build-archive "$ARCHIVE_PATH"
  )
  local sha256
  sha256=$(compute_sha256 "$ARCHIVE_PATH")
  printf '%s  %s\n' "$sha256" "$ARCHIVE_NAME" > "$SHA_PATH"
  write_metadata "$ARCHIVE_PATH" "$sha256"
}

subcommand=${1:-}
case "$subcommand" in
  archive-dir)
    echo "$PREBUILT_DIR"
    ;;
  archive-path)
    echo "$ARCHIVE_PATH"
    ;;
  cache-key)
    echo "gnu-build-cache-${GNU_BUILD_CACHE_VERSION}-${HOST_GOOS}-${HOST_GOARCH}-coreutils-${GNU_VERSION}"
    ;;
  fetch)
    fetch_archive
    ;;
  publish)
    publish_archive
    ;;
  run)
    run_harness
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
