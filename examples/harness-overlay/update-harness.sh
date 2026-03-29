#!/usr/bin/env bash
set -euo pipefail

repo_url="${HARNESS_UPSTREAM_REPO:-https://github.com/wedow/harness}"
ref=""
cache_dir=""
export GIT_TERMINAL_PROMPT=0

usage() {
  echo "usage: $0 [--ref <git-ref>] [--cache-dir <dir>]" >&2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ref)
      if [[ $# -lt 2 ]]; then
        usage
        exit 1
      fi
      ref="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    --cache-dir)
      if [[ $# -lt 2 ]]; then
        usage
        exit 1
      fi
      cache_dir="$2"
      shift 2
      ;;
    *)
      usage
      exit 1
      ;;
  esac
done

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
overlay_dir="${script_dir}/workspace"

if [[ -z "${ref}" ]]; then
  ref="$(tr -d '\n' < "${script_dir}/UPSTREAM_COMMIT")"
fi
if [[ -z "${cache_dir}" ]]; then
  cache_dir="${HARNESS_OVERLAY_CACHE_DIR:-${script_dir}/.cache}"
fi

mkdir -p "${cache_dir}/workspaces"

clone_dir="${cache_dir}/repo"
if [[ ! -d "${clone_dir}/.git" ]]; then
  tmp_clone="$(mktemp -d)"
  cleanup_tmp_clone() {
    rm -rf "${tmp_clone}"
  }
  trap cleanup_tmp_clone EXIT
  git clone --quiet "${repo_url}" "${tmp_clone}"
  rm -rf "${clone_dir}"
  mkdir -p "$(dirname "${clone_dir}")"
  mv "${tmp_clone}" "${clone_dir}"
  trap - EXIT
else
  git -C "${clone_dir}" remote set-url origin "${repo_url}"
fi

git -C "${clone_dir}" fetch --quiet --tags origin
git -C "${clone_dir}" checkout --quiet "${ref}"

resolved_ref="$(git -C "${clone_dir}" rev-parse HEAD)"
workspace_dir="${cache_dir}/workspaces/${resolved_ref}"
tmp_workspace="$(mktemp -d)"
cleanup_tmp_workspace() {
  rm -rf "${tmp_workspace}"
}
trap cleanup_tmp_workspace EXIT

mkdir -p "${tmp_workspace}" "${tmp_workspace}/plugins"
cp -R "${clone_dir}/bin" "${tmp_workspace}/bin"
for plugin in auth core openai anthropic chatgpt skills subagents; do
  cp -R "${clone_dir}/plugins/${plugin}" "${tmp_workspace}/plugins/${plugin}"
done
cp "${clone_dir}/LICENSE" "${tmp_workspace}/LICENSE.harness"
cp -R "${overlay_dir}/." "${tmp_workspace}/"

rm -rf "${workspace_dir}"
mkdir -p "$(dirname "${workspace_dir}")"
mv "${tmp_workspace}" "${workspace_dir}"
trap - EXIT

printf '%s\n' "${workspace_dir}"
