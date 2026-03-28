#!/usr/bin/env bash
set -euo pipefail

repo_url="${HARNESS_UPSTREAM_REPO:-https://github.com/wedow/harness}"
ref=""

usage() {
  echo "usage: $0 [--ref <git-ref>]" >&2
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
    *)
      usage
      exit 1
      ;;
  esac
done

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
workspace_dir="${script_dir}/workspace"

if [[ -z "${ref}" ]]; then
  ref="$(tr -d '\n' < "${script_dir}/UPSTREAM_COMMIT")"
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

repo_dir="${tmpdir}/harness"
git clone --quiet "${repo_url}" "${repo_dir}"
git -C "${repo_dir}" checkout --quiet "${ref}"
resolved_ref="$(git -C "${repo_dir}" rev-parse HEAD)"

rm -rf "${workspace_dir}/bin" "${workspace_dir}/plugins" "${workspace_dir}/LICENSE.harness"
mkdir -p "${workspace_dir}" "${workspace_dir}/plugins"

cp -R "${repo_dir}/bin" "${workspace_dir}/bin"
for plugin in auth core openai anthropic chatgpt skills subagents; do
  cp -R "${repo_dir}/plugins/${plugin}" "${workspace_dir}/plugins/${plugin}"
done
cp "${repo_dir}/LICENSE" "${workspace_dir}/LICENSE.harness"

printf '%s\n' "${resolved_ref}" > "${script_dir}/UPSTREAM_COMMIT"
