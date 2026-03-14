#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

IMAGE_NAME=${COMPAT_DOCKER_IMAGE:-gbash-compat-local}
BASE_IMAGE=${COMPAT_DOCKER_BASE_IMAGE:-}
PLATFORM=${COMPAT_DOCKER_PLATFORM:-linux/amd64}
PULL_MODE=${COMPAT_DOCKER_PULL:-0}
GNU_CACHE_DIR=${GNU_CACHE_DIR:-.cache/gnu}
GNU_RESULTS_DIR=${GNU_RESULTS_DIR:-.cache/gnu/results/docker-latest}
GNU_FORCE_REBUILD=${GNU_FORCE_REBUILD:-0}
GNU_BUILD_CACHE_VERSION=${GNU_BUILD_CACHE_VERSION:-v3}
GNU_BUILD_CACHE_TAG=${GNU_BUILD_CACHE_TAG:-gnu-build-cache-v3}
GNU_BUILD_CACHE_REPO=${GNU_BUILD_CACHE_REPO:-ewhauser/gbash}

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
        docker pull --platform "$PLATFORM" "$BASE_IMAGE" >/dev/null 2>&1 || true
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
      if docker pull --platform "$PLATFORM" "$IMAGE_NAME" >/dev/null 2>&1; then
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
          docker pull --platform "$PLATFORM" "$BASE_IMAGE" || true
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
      if docker pull --platform "$PLATFORM" "$IMAGE_NAME"; then
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

mkdir -p \
  "$CACHE_DIR_HOST" \
  "$RESULTS_DIR_HOST" \
  "$REPO_ROOT/.cache/go-build" \
  "$REPO_ROOT/.cache/go-mod" \
  "$REPO_ROOT/.cache/pip"

ensure_image

docker run --rm --platform "$PLATFORM" \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp/gbash-home \
  -e GOCACHE=/workspace/.cache/go-build \
  -e GOMODCACHE=/workspace/.cache/go-mod \
  -e PIP_CACHE_DIR=/workspace/.cache/pip \
  -e GNU_CACHE_DIR="/workspace/$CACHE_DIR_REL" \
  -e GNU_GBASH_BIN="/workspace/$CACHE_DIR_REL/bin/gbash" \
  -e GNU_RESULTS_DIR="/workspace/$RESULTS_DIR_REL" \
  -e GNU_UTILS="${GNU_UTILS:-}" \
  -e GNU_TESTS="${GNU_TESTS:-}" \
  -e GNU_KEEP_WORKDIR="${GNU_KEEP_WORKDIR:-}" \
  -e GNU_FORCE_REBUILD="$GNU_FORCE_REBUILD" \
  -e GNU_BUILD_CACHE_VERSION="$GNU_BUILD_CACHE_VERSION" \
  -e GNU_BUILD_CACHE_TAG="$GNU_BUILD_CACHE_TAG" \
  -e GNU_BUILD_CACHE_REPO="$GNU_BUILD_CACHE_REPO" \
  -v "$REPO_ROOT:/workspace" \
  -w /workspace \
  "$IMAGE_NAME" \
  bash -lc '
    set -euo pipefail
    mkdir -p "$HOME" "$GOCACHE" "$GOMODCACHE" "$PIP_CACHE_DIR" "$GNU_RESULTS_DIR"
    make gnu-test
    if [ -f "$GNU_RESULTS_DIR/summary.json" ]; then
      go run ./scripts/compat-report --summary "$GNU_RESULTS_DIR/summary.json" --output "$GNU_RESULTS_DIR"
    fi
  '

echo "report: $RESULTS_DIR_HOST/index.html"
