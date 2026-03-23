#!/bin/bash

set -euo pipefail

SCRIPT_PATH="${BASH_SOURCE[0]:-$0}"
ROOT_DIR="$(cd "${SCRIPT_PATH%/*}/.." && pwd)"
VERSION="${1:-${VERSION:-}}"
ROOT_MODULE="github.com/ewhauser/gbash"

if [[ -n "${VERSION}" && "${VERSION}" != v* ]]; then
	echo "version must look like vX.Y.Z" >&2
	exit 1
fi

sync_package_json_version() {
	local file="$1"
	if [[ -z "${VERSION}" || ! -f "${ROOT_DIR}/${file}" ]]; then
		return
	fi
	node - "${ROOT_DIR}/${file}" "${VERSION#v}" <<'EOF'
const fs = require("node:fs");

const [, , file, version] = process.argv;
const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
pkg.version = version;
fs.writeFileSync(file, `${JSON.stringify(pkg, null, 2)}\n`);
EOF
}

array_contains() {
	local needle="$1"
	local item

	shift
	for item in "$@"; do
		if [[ "${item}" == "${needle}" ]]; then
			return 0
		fi
	done

	return 1
}

list_target_module_dirs() {
	local go_mod

	for go_mod in "${ROOT_DIR}"/contrib/*/go.mod; do
		if [[ -f "${go_mod}" ]]; then
			printf '%s\n' "${go_mod#${ROOT_DIR}/}"
		fi
	done

	if [[ -f "${ROOT_DIR}/examples/go.mod" ]]; then
		printf 'examples/go.mod\n'
	fi
}

list_internal_requirements() {
	local go_mod="$1"
	local line=""
	local candidate=""
	local in_require_block=0

	while IFS= read -r line || [[ -n "${line}" ]]; do
		case "${line}" in
			'require (')
				in_require_block=1
				continue
				;;
			')')
				if ((in_require_block)); then
					in_require_block=0
					continue
				fi
				;;
		esac

		candidate=""
		if ((in_require_block)); then
			set -- ${line}
			candidate="${1:-}"
		else
			case "${line}" in
				require\ *)
					line="${line#require }"
					set -- ${line}
					candidate="${1:-}"
					;;
				*)
					continue
					;;
			esac
		fi

		case "${candidate}" in
			"${ROOT_MODULE}" | "${ROOT_MODULE}"/contrib/*)
				printf '%s\n' "${candidate}"
				;;
		esac
	done < "${go_mod}"
}

module_dir_from_path() {
	local module="$1"

	case "${module}" in
		"${ROOT_MODULE}")
			printf '.\n'
			;;
		"${ROOT_MODULE}"/contrib/*)
			printf 'contrib/%s\n' "${module#${ROOT_MODULE}/contrib/}"
			;;
		*)
			return 1
			;;
	esac
}

replace_target_for_module() {
	local from_dir="$1"
	local module="$2"
	local target_dir=""

	target_dir="$(module_dir_from_path "${module}")"
	if [[ "${from_dir}" == "examples" ]]; then
		if [[ "${target_dir}" == "." ]]; then
			printf '../\n'
		else
			printf '../%s\n' "${target_dir}"
		fi
		return
	fi

	if [[ "${from_dir}" == "." ]]; then
		printf '%s\n' "${target_dir}"
		return
	fi

	if [[ "${target_dir}" == "." ]]; then
		printf '../..\n'
	else
		printf '../%s\n' "${target_dir#contrib/}"
	fi
}

collect_replacement_modules() {
	local dir="$1"
	local queue=()
	local seen=()
	local module=""
	local dep=""
	local module_dir=""
	local idx=0

	while IFS= read -r module; do
		if [[ -n "${module}" ]] && ! array_contains "${module}" "${queue[@]-}"; then
			queue+=("${module}")
		fi
	done < <(list_internal_requirements "${ROOT_DIR}/${dir}/go.mod")

	while ((idx < ${#queue[@]})); do
		module="${queue[idx]}"
		((idx += 1))

		if array_contains "${module}" "${seen[@]-}"; then
			continue
		fi

		seen+=("${module}")
		module_dir="$(module_dir_from_path "${module}" || true)"
		if [[ -z "${module_dir}" ]]; then
			continue
		fi

		while IFS= read -r dep; do
			if [[ -n "${dep}" ]] && ! array_contains "${dep}" "${seen[@]-}" "${queue[@]-}"; then
				queue+=("${dep}")
			fi
		done < <(list_internal_requirements "${ROOT_DIR}/${module_dir}/go.mod")
	done

	if ((${#seen[@]} > 0)); then
		printf '%s\n' "${seen[@]}"
	fi
}

edit_module() {
	local dir="$1"
	local direct_modules=()
	local replacement_modules=()
	local module=""
	local replace_target=""

	while IFS= read -r module; do
		if [[ -n "${module}" ]] && ! array_contains "${module}" "${direct_modules[@]-}"; then
			direct_modules+=("${module}")
		fi
	done < <(list_internal_requirements "${ROOT_DIR}/${dir}/go.mod")

	while IFS= read -r module; do
		if [[ -n "${module}" ]] && ! array_contains "${module}" "${replacement_modules[@]-}"; then
			replacement_modules+=("${module}")
		fi
	done < <(collect_replacement_modules "${dir}")

	(
		cd "${ROOT_DIR}/${dir}"
		for module in "${direct_modules[@]-}"; do
			if [[ -z "${module}" ]]; then
				continue
			fi
			if [[ -n "${VERSION}" ]]; then
				go mod edit -require="${module}@${VERSION}"
			fi
		done
		for module in "${replacement_modules[@]-}"; do
			if [[ -z "${module}" ]]; then
				continue
			fi
			replace_target="$(replace_target_for_module "${dir}" "${module}")"
			go mod edit -replace="${module}=${replace_target}"
		done
		go mod tidy
	)
}

while IFS= read -r go_mod; do
	edit_module "${go_mod%/go.mod}"
done < <(list_target_module_dirs)

sync_package_json_version packages/gbash-wasm/package.json

(
	cd "${ROOT_DIR}"
	go list -m all > /dev/null
)
