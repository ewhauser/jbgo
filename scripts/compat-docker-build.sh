#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

IMAGE_NAME=${COMPAT_DOCKER_IMAGE:-gbash-compat-local}
BASE_IMAGE=${COMPAT_DOCKER_BASE_IMAGE:-}
PLATFORM=${COMPAT_DOCKER_PLATFORM:-linux/amd64}
PULL_MODE=${COMPAT_DOCKER_PULL:-0}
GO_VERSION=${COMPAT_DOCKER_GO_VERSION:-$(awk '/^go / { print $2; exit }' "$REPO_ROOT/go.mod")}

sync_from_base_image() {
  if [[ -z "$BASE_IMAGE" ]]; then
    return 1
  fi

  case "$PULL_MODE" in
    1|true|TRUE|always)
      docker pull --platform "$PLATFORM" "$BASE_IMAGE" || true
      ;;
    missing)
      if ! docker image inspect "$BASE_IMAGE" >/dev/null 2>&1; then
        docker pull --platform "$PLATFORM" "$BASE_IMAGE" || return 1
      fi
      ;;
    0|false|FALSE|"")
      return 1
      ;;
  esac

  if ! docker image inspect "$BASE_IMAGE" >/dev/null 2>&1; then
    return 1
  fi

  if [[ "$BASE_IMAGE" != "$IMAGE_NAME" ]]; then
    docker tag "$BASE_IMAGE" "$IMAGE_NAME"
  fi
  return 0
}

if sync_from_base_image; then
  exit 0
fi

exec docker build \
  --platform "$PLATFORM" \
  --build-arg "GO_VERSION=$GO_VERSION" \
  -t "$IMAGE_NAME" \
  -f "$REPO_ROOT/docker/compat/Dockerfile" \
  "$REPO_ROOT"
