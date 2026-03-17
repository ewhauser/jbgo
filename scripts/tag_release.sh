#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." && pwd)"
VERSION="${1:-${RELEASE_VERSION:-}}"
REMOTE="${REMOTE:-origin}"
PUSH="${PUSH:-0}"
HEAD_COMMIT=""

if [[ -z "${VERSION}" ]]; then
	echo "usage: scripts/tag_release.sh vX.Y.Z" >&2
	exit 1
fi

if [[ "${VERSION}" != v* ]]; then
	echo "version must look like vX.Y.Z" >&2
	exit 1
fi

cd "${ROOT_DIR}"

if [[ "${ALLOW_NON_MAIN:-0}" != "1" ]]; then
	branch="$(git branch --show-current)"
	if [[ "${branch}" != "main" ]]; then
		echo "release tags must be created from main (set ALLOW_NON_MAIN=1 to override)" >&2
		exit 1
	fi
fi

if [[ -n "$(git status --short)" ]]; then
	echo "git worktree must be clean before tagging" >&2
	exit 1
fi

HEAD_COMMIT="$(git rev-parse HEAD)"
tags=("${VERSION}")
while IFS= read -r module_dir; do
	tags+=("${module_dir}/${VERSION}")
done < <(find contrib -mindepth 1 -maxdepth 1 -type d | sort)

created_tags=()
for tag in "${tags[@]}"; do
	if git rev-parse -q --verify "refs/tags/${tag}" > /dev/null; then
		tag_commit="$(git rev-list -n 1 "${tag}")"
		if [[ "${tag_commit}" != "${HEAD_COMMIT}" ]]; then
			echo "tag already exists on a different commit: ${tag}" >&2
			exit 1
		fi
	fi
done

for tag in "${tags[@]}"; do
	if git rev-parse -q --verify "refs/tags/${tag}" > /dev/null; then
		continue
	fi
	git tag -a "${tag}" -m "${tag}"
	created_tags+=("${tag}")
done

if [[ ${#created_tags[@]} -gt 0 ]]; then
	printf 'created tags:\n'
else
	printf 'all release tags already existed on %s:\n' "${HEAD_COMMIT}"
fi
for tag in "${tags[@]}"; do
	printf '  %s\n' "${tag}"
done

if [[ "${PUSH}" == "1" ]]; then
	git push "${REMOTE}" "${tags[@]}"
else
	printf '\nto push them:\n  git push %s' "${REMOTE}"
	for tag in "${tags[@]}"; do
		printf ' %s' "${tag}"
	done
	printf '\n'
fi
