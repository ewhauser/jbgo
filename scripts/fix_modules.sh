#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-${VERSION:-}}"

if [[ -n "${VERSION}" && "${VERSION}" != v* ]]; then
	echo "version must look like vX.Y.Z" >&2
	exit 1
fi

edit_module() {
	local dir="$1"
	shift
	(
		cd "${ROOT_DIR}/${dir}"
		while (($#)); do
			local module="$1"
			local replace_target="$2"
			if [[ -n "${VERSION}" ]]; then
				go mod edit -require="${module}@${VERSION}"
			fi
			go mod edit -replace="${module}=${replace_target}"
			shift 2
		done
		go mod tidy
	)
}

edit_module contrib/awk github.com/ewhauser/gbash ../..
edit_module contrib/jq github.com/ewhauser/gbash ../..
edit_module contrib/sqlite3 github.com/ewhauser/gbash ../..
edit_module contrib/yq github.com/ewhauser/gbash ../..
edit_module \
	contrib/extras \
	github.com/ewhauser/gbash ../.. \
	github.com/ewhauser/gbash/contrib/awk ../awk \
	github.com/ewhauser/gbash/contrib/jq ../jq \
	github.com/ewhauser/gbash/contrib/sqlite3 ../sqlite3 \
	github.com/ewhauser/gbash/contrib/yq ../yq
edit_module \
	examples \
	github.com/ewhauser/gbash ../ \
	github.com/ewhauser/gbash/contrib/sqlite3 ../contrib/sqlite3

(
	cd "${ROOT_DIR}"
	go list -m all > /dev/null
)
