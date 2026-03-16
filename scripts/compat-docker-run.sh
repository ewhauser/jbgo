#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

IMAGE_NAME=${COMPAT_DOCKER_IMAGE:-gbash-compat-local}
BASE_IMAGE=${COMPAT_DOCKER_BASE_IMAGE:-}
PLATFORM=${COMPAT_DOCKER_PLATFORM:-}
PULL_MODE=${COMPAT_DOCKER_PULL:-0}
GNU_CACHE_DIR=${GNU_CACHE_DIR:-.cache/gnu}
GNU_RESULTS_DIR=${GNU_RESULTS_DIR:-.cache/gnu/results/docker-latest}
GBASH_COMPAT_TRACE=${GBASH_COMPAT_TRACE:-0}

trace_enabled() {
  case "${GBASH_COMPAT_TRACE:-0}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
  esac
  return 1
}

enable_trace() {
  local label=$1
  PS4="+ $label: "
  export PS4
  set -x
}

print_disk_snapshot() {
  local container_name=$1
  local upper_dir

  printf '=== compat disk snapshot %s ===\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  df -h "$REPO_ROOT" / || true
  du -sh "$CACHE_DIR_HOST" "$RESULTS_DIR_HOST" 2>/dev/null || true
  docker system df || true

  upper_dir=$(docker inspect -f '{{ .GraphDriver.Data.UpperDir }}' "$container_name" 2>/dev/null || true)
  if [[ -n "$upper_dir" ]]; then
    echo "container upperdir: $upper_dir"
    if command -v sudo >/dev/null 2>&1; then
      sudo -n du -sh "$upper_dir" 2>/dev/null || du -sh "$upper_dir" 2>/dev/null || true
    else
      du -sh "$upper_dir" 2>/dev/null || true
    fi
  fi
}

watch_disk_usage() {
  local container_name=$1
  while ! docker inspect "$container_name" >/dev/null 2>&1; do
    sleep 1
  done
  while docker inspect "$container_name" >/dev/null 2>&1; do
    print_disk_snapshot "$container_name"
    sleep 30
  done
}

abs_repo_path() {
  local path=$1
  if [[ "$path" = /* ]]; then
    printf '%s\n' "$path"
    return
  fi
  printf '%s/%s\n' "$REPO_ROOT" "$path"
}

require_repo_path() {
  local path=$1
  case "$path" in
    "$REPO_ROOT" | "$REPO_ROOT"/*) ;;
    *)
      echo "path must be inside the repo root for docker runs: $path" >&2
      exit 1
      ;;
  esac
}

ensure_image() {
  if [[ -n "$BASE_IMAGE" ]]; then
    case "$PULL_MODE" in
      1|true|TRUE|always)
        docker pull ${PLATFORM:+--platform "$PLATFORM"} "$BASE_IMAGE" >/dev/null 2>&1 || true
        if docker image inspect "$BASE_IMAGE" >/dev/null 2>&1; then
          if [[ "$BASE_IMAGE" != "$IMAGE_NAME" ]]; then
            docker tag "$BASE_IMAGE" "$IMAGE_NAME"
          fi
          return
        fi
        ;;
    esac
  fi
  case "$PULL_MODE" in
    1|true|TRUE|always)
      if docker pull ${PLATFORM:+--platform "$PLATFORM"} "$IMAGE_NAME" >/dev/null 2>&1; then
        return
      fi
      ;;
  esac
  if docker image inspect "$IMAGE_NAME" >/dev/null 2>&1; then
    return
  fi
  if [[ -n "$BASE_IMAGE" ]]; then
    case "$PULL_MODE" in
      1|true|TRUE|always|missing)
        if [[ "$PULL_MODE" == "missing" ]] && ! docker image inspect "$BASE_IMAGE" >/dev/null 2>&1; then
          docker pull ${PLATFORM:+--platform "$PLATFORM"} "$BASE_IMAGE" || true
        fi
        if docker image inspect "$BASE_IMAGE" >/dev/null 2>&1; then
          if [[ "$BASE_IMAGE" != "$IMAGE_NAME" ]]; then
            docker tag "$BASE_IMAGE" "$IMAGE_NAME"
          fi
          return
        fi
        ;;
    esac
  fi
  case "$PULL_MODE" in
    1|true|TRUE|always|missing)
      if docker pull ${PLATFORM:+--platform "$PLATFORM"} "$IMAGE_NAME"; then
        return
      fi
      ;;
  esac
  "$SCRIPT_DIR/compat-docker-build.sh"
}

CACHE_DIR_HOST=$(abs_repo_path "$GNU_CACHE_DIR")
RESULTS_DIR_HOST=$(abs_repo_path "$GNU_RESULTS_DIR")
require_repo_path "$CACHE_DIR_HOST"
require_repo_path "$RESULTS_DIR_HOST"

CACHE_DIR_REL=${CACHE_DIR_HOST#"$REPO_ROOT"/}
RESULTS_DIR_REL=${RESULTS_DIR_HOST#"$REPO_ROOT"/}
TMP_DIR_HOST="$CACHE_DIR_HOST/tmp"
HOME_DIR_HOST="$CACHE_DIR_HOST/home"
TMP_DIR_REL=${TMP_DIR_HOST#"$REPO_ROOT"/}
HOME_DIR_REL=${HOME_DIR_HOST#"$REPO_ROOT"/}
CONTAINER_NAME="gbash-compat-${GITHUB_RUN_ID:-$$}"

mkdir -p \
  "$CACHE_DIR_HOST" \
  "$RESULTS_DIR_HOST" \
  "$TMP_DIR_HOST" \
  "$HOME_DIR_HOST" \
  "$REPO_ROOT/.cache/go-build" \
  "$REPO_ROOT/.cache/go-mod"

if trace_enabled; then
  enable_trace "compat-docker-run.sh"
fi

ensure_image

watch_pid=
if trace_enabled; then
  watch_disk_usage "$CONTAINER_NAME" &
  watch_pid=$!
fi
cleanup_watchdog() {
  if [[ -n "${watch_pid:-}" ]]; then
    kill "$watch_pid" 2>/dev/null || true
    wait "$watch_pid" 2>/dev/null || true
  fi
}
trap cleanup_watchdog EXIT

docker run --rm --name "$CONTAINER_NAME" ${PLATFORM:+--platform "$PLATFORM"} \
  --user "$(id -u):$(id -g)" \
  -e HOME="/workspace/$HOME_DIR_REL" \
  -e TMPDIR="/workspace/$TMP_DIR_REL" \
  -e GOCACHE=/workspace/.cache/go-build \
  -e GOMODCACHE=/workspace/.cache/go-mod \
  -e GNU_CACHE_DIR="/workspace/$CACHE_DIR_REL" \
  -e GNU_RESULTS_DIR="/workspace/$RESULTS_DIR_REL" \
  -e GNU_UTILS="${GNU_UTILS:-}" \
  -e GNU_TESTS="${GNU_TESTS:-}" \
  -e GNU_KEEP_WORKDIR="${GNU_KEEP_WORKDIR:-}" \
  -e GBASH_COMPAT_TRACE="${GBASH_COMPAT_TRACE:-0}" \
  -v "$REPO_ROOT:/workspace" \
  -w /workspace \
  "$IMAGE_NAME" \
  bash -lc '
    set -euo pipefail
    case "${GBASH_COMPAT_TRACE:-0}" in
      1|true|TRUE|yes|YES|on|ON)
        PS4="+ compat-container: "
        export PS4
        set -x
        ;;
    esac
    mkdir -p "$HOME" "$TMPDIR" "$GOCACHE" "$GOMODCACHE"
    df -h / /workspace || true
    du -sh "$TMPDIR" "$GNU_CACHE_DIR" "$GNU_RESULTS_DIR" 2>/dev/null || true
    ./scripts/gnu-test.sh
  '

echo "report: $RESULTS_DIR_HOST/index.html"
